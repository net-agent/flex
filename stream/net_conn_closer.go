package stream

import (
	"errors"
	"time"
)

var (
	ErrWriterIsClosed        = errors.New("writer is closed")
	ErrReaderIsClosed        = errors.New("reader is closed")
	ErrWaitCloseAckTimeout   = errors.New("wait close ack timeout")
	ErrPushToByteChanTimeout = errors.New("push to byte-chan timeout")
)

func (s *Stream) Close() error {
	var err error

	err = s.CloseWrite()
	if err != nil {
		return err
	}

	err = s.SendCmdClose()
	if err != nil {
		return err
	}

	select {
	case <-s.closeAckCh:
		return s.CloseRead()
	case <-time.After(s.closeAckTimeout):
		return ErrWaitCloseAckTimeout
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
