package stream

import (
	"errors"
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
		return err // defer will run CloseRead
	}

	// 3. Notify Remote
	if err := s.SendCmdClose(); err != nil {
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
	s.rmut.Lock()
	defer s.rmut.Unlock()

	if s.rclosed {
		return ErrReaderIsClosed
	}

	s.rclosed = true
	close(s.bytesChan)

	return nil
}

// CloseWrite 设置写状态为不可写，并且告诉对端
func (s *Stream) CloseWrite() error {
	s.wmut.Lock()
	defer s.wmut.Unlock()

	if s.wclosed {
		return ErrWriterIsClosed
	}

	s.wclosed = true
	s.state.Closed = time.Now()
	s.state.IsClosed = true
	return nil
}
