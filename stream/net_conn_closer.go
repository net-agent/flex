package stream

import (
	"errors"
	"net"
	"time"
)

var (
	ErrWriterIsClosed      = errors.New("writer is closed")
	ErrReaderIsClosed      = errors.New("reader is closed")
	ErrWaitCloseAckTimeout = errors.New("wait close ack timeout")
)

func (s *Stream) Close() error {
	// 1. Ensure absolute resource cleanup locally
	defer s.CloseRead()

	// 2. Mark Write as closed locally
	if err := s.CloseWrite(); err != nil {
		// Second call to Close: return net.ErrClosed per net.Conn contract (P3 fix)
		if errors.Is(err, ErrWriterIsClosed) {
			return net.ErrClosed
		}
		return err // defer will run CloseRead
	}

	// 3. Notify Remote
	if err := s.sender.SendClose(); err != nil {
		return err // defer will run CloseRead
	}

	// 4. Wait for confirmation (Graceful period)
	select {
	case <-s.closeAckCh:
		return nil
	case <-time.After(s.closeAckTimeout):
		return ErrWaitCloseAckTimeout // defer will run CloseRead
	}
}

func (s *Stream) CloseRead() error {
	s.rchanMu.Lock()
	defer s.rchanMu.Unlock()

	if s.readClosed {
		return ErrReaderIsClosed
	}

	s.readClosed = true
	close(s.recvQueue)

	return nil
}

// CloseWrite sets the write state to closed and signals blocked Write operations
// to wake up immediately via closeCh.
func (s *Stream) CloseWrite() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if s.writeClosed {
		return ErrWriterIsClosed
	}

	s.writeClosed = true
	s.state.Closed = time.Now()
	s.state.IsClosed = true

	// Signal blocked Write to wake up immediately (P1 fix)
	close(s.closeCh)

	return nil
}
