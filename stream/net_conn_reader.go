package stream

import (
	"errors"
	"io"
	"time"
)

var (
	ErrReadFromStreamTimeout = errors.New("read from stream timeout")
)

func (s *Stream) Read(dist []byte) (int, error) {
	if len(s.currBuf) == 0 {
		select {
		case buf, ok := <-s.bytesChan:
			if !ok {
				return 0, io.EOF
			}
			s.currBuf = buf

		case <-time.After(s.readTimeout):
			return 0, ErrReadFromStreamTimeout
		}
	}

	n := copy(dist, s.currBuf)
	s.counter.Read += int64(n)
	s.currBuf = s.currBuf[n:]
	if n > 0 {
		go s.SendCmdDataAck(uint16(n))
	}
	return n, nil
}
