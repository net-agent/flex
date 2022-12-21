package stream

import "errors"

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

	return nil
}

func (s *Stream) CloseRead() error {
	s.rmut.Lock()
	defer s.rmut.Unlock()

	if s.rclosed {
		return errors.New("stream closed, pbuf of close ignored")
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
		return errors.New("close on closed conn")
	}

	s.wclosed = true
	return nil
}
