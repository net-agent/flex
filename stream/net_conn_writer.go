package stream

import (
	"errors"
	"net"
	"runtime"
	"sync/atomic"
)

func (s *Stream) Write(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}

	var wn int
	for len(buf) > 0 {
		if err := s.checkWriteInterruption(); err != nil {
			return wn, err
		}

		n, err := s.write(buf)
		if n > 0 {
			wn += n
			buf = buf[n:]
		}
		if err != nil {
			return wn, err
		}
		if len(buf) == 0 {
			return wn, nil
		}

		if n == 0 {
			// Window exhausted — block until capacity restored or interrupted.
			select {
			case <-s.window.Event():
				// Capacity may have been restored, retry.
			case <-s.closeCh:
			case <-s.writeDeadline.Done():
			}
			if err := s.checkWriteInterruption(); err != nil {
				return wn, err
			}
		} else {
			runtime.Gosched()
		}
	}
	return wn, nil
}

// write sends at most one chunk.
// It reserves window under writeMu (state critical section) and sends without
// holding writeMu, so CloseWrite can proceed even if underlying I/O blocks.
//
// Returns (0, nil) when window is exhausted — the caller should block
// on window.Event() before retrying.
func (s *Stream) write(buf []byte) (int, error) {
	sliceSize, err := s.reserveWriteChunk(len(buf))
	if err != nil || sliceSize == 0 {
		return 0, err
	}

	// Re-check interruption after reserve but before send.
	if err := s.checkWriteInterruption(); err != nil {
		s.window.Release(int32(sliceSize))
		return 0, err
	}

	stopInterruptWatch := s.startSendInterrupter()
	defer close(stopInterruptWatch)

	err = s.sender.SendData(buf[:sliceSize])
	if err != nil {
		s.window.Release(int32(sliceSize))
		// Deterministic precedence: close > timeout > transport error.
		if s.isWriteClosed() {
			return 0, ErrWriterIsClosed
		}
		if s.isWriteDeadlineExceeded() {
			return 0, ErrTimeout
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return 0, ErrTimeout
		}
		return 0, err
	}

	atomic.AddInt64(&s.state.BytesWritten, int64(sliceSize))
	return sliceSize, nil
}

func (s *Stream) reserveWriteChunk(bufLen int) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if s.writeClosed {
		return 0, ErrWriterIsClosed
	}

	avail := int(s.window.Available())
	if avail <= 0 {
		return 0, nil // signal caller to wait for window
	}

	sliceSize := bufLen
	if sliceSize > DefaultSplitSize {
		sliceSize = DefaultSplitSize
	}
	if sliceSize > avail {
		sliceSize = avail
	}

	s.window.Consume(int32(sliceSize))
	return sliceSize, nil
}

func (s *Stream) checkWriteInterruption() error {
	if s.isWriteClosed() {
		return ErrWriterIsClosed
	}
	if s.isWriteDeadlineExceeded() {
		return ErrTimeout
	}
	return nil
}

func (s *Stream) isWriteClosed() bool {
	s.writeMu.Lock()
	closed := s.writeClosed
	s.writeMu.Unlock()
	return closed
}

// isWriteDeadlineExceeded performs double-read to distinguish true timeout
// from deadline-reset wakeups (old done channel closed by Set()).
func (s *Stream) isWriteDeadlineExceeded() bool {
	select {
	case <-s.writeDeadline.Done():
		select {
		case <-s.writeDeadline.Done():
			return true
		default:
			return false
		}
	default:
		return false
	}
}

// startSendInterrupter starts a per-send watcher that interrupts the underlying
// writer if close/deadline happens while SendData is blocked.
//
// NOTE(shared-conn):
// In the default shared net.Conn multiplexing model, sender.CanInterruptWrite()
// is intentionally false to avoid cross-stream side effects. That means this
// hook is currently inactive for connWriter and send-stage blocking is not
// preempted per stream yet.
func (s *Stream) startSendInterrupter() chan struct{} {
	stop := make(chan struct{})
	if !s.sender.CanInterruptWrite() {
		return stop
	}
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-s.closeCh:
				s.sender.InterruptWrite()
				return
			case <-s.writeDeadline.Done():
				// Ignore deadline-reset wakeups (old done channel closed by Set()).
				if !s.isWriteDeadlineExceeded() {
					continue
				}
				s.sender.InterruptWrite()
				return
			}
		}
	}()
	return stop
}
