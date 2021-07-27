package stream

import (
	"fmt"
	"io"
	"time"
)

func (s *Conn) AppendData(buf []byte) {
	if len(buf) > 0 && !s.rclosed {
		s.bytesChan <- buf
	}
}

func (s *Conn) AppendEOF() {
	if s.rclosed {
		return
	}
	s.rclosed = true
	close(s.bytesChan)
}

func (s *Conn) Read(dist []byte) (int, error) {
	if len(s.currBuf) == 0 {
		select {
		case buf, ok := <-s.bytesChan:
			if !ok {
				return 0, io.EOF
			}
			s.currBuf = buf

		case <-time.After(time.Second * 5):
			return 0, fmt.Errorf("read timeout. %v:%v -> %v:%v",
				s.localIP, s.localPort, s.remoteIP, s.remotePort)
		}
	}

	n := copy(dist, s.currBuf)
	s.currBuf = s.currBuf[n:]
	if n > 0 {
		s.writeACK(uint16(n))
	}
	return n, nil
}
