package stream

import (
	"runtime"
	"sync/atomic"
)

func (s *Stream) Write(buf []byte) (int, error) {
	if len(buf) == 0 {
		return 0, nil
	}

	var wn int
	for len(buf) > 0 {
		// Non-blocking check: deadline and close at every iteration,
		// even when window has capacity. (Fix: deadline not checked when window > 0)
		select {
		case <-s.closeCh:
			return wn, ErrWriterIsClosed
		case <-s.writeDeadline.Done():
			return wn, ErrTimeout
		default:
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
				return wn, ErrWriterIsClosed
			case <-s.writeDeadline.Done():
				// Could be a real timeout OR a deadline reset (Set closed old channel).
				// Re-read Done(): if the NEW channel is still closed → real timeout.
				// If the new channel is open → deadline was reset, continue loop.
				select {
				case <-s.writeDeadline.Done():
					return wn, ErrTimeout
				default:
					// Deadline was reset, continue.
				}
			}
		} else {
			runtime.Gosched()
		}
	}
	return wn, nil
}

// write sends at most one chunk under writeMu.
// It atomically checks window capacity and consumes it, eliminating the
// TOCTOU race where concurrent writers could both see Available() > 0
// and drive the window negative.
//
// Returns (0, nil) when window is exhausted — the caller should block
// on window.Event() before retrying.
func (s *Stream) write(buf []byte) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if s.writeClosed {
		return 0, ErrWriterIsClosed
	}

	avail := int(s.window.Available())
	if avail <= 0 {
		return 0, nil // signal caller to wait for window
	}

	sliceSize := len(buf)
	if sliceSize > DefaultSplitSize {
		sliceSize = DefaultSplitSize
	}
	if sliceSize > avail {
		sliceSize = avail
	}

	err := s.sender.SendData(buf[:sliceSize])
	if err != nil {
		return 0, err
	}

	atomic.AddInt64(&s.state.BytesWritten, int64(sliceSize))
	s.window.Consume(int32(sliceSize))
	return sliceSize, nil
}
