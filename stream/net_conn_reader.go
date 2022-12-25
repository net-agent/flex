package stream

import (
	"errors"
	"io"
	"log"
	"sync/atomic"
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
	atomic.AddInt64(&s.state.ConnReadSize, int64(n))
	s.currBuf = s.currBuf[n:]
	if n > 0 {
		go func() {
			err := s.SendCmdDataAck(uint16(n))
			if err != nil {
				log.Println("SendCmdDataAck failed:", err)
				return
			}
			atomic.AddInt64(&s.state.SendDataAckSum, int64(n))
		}()
	}
	return n, nil
}
