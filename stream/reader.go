package stream

import "io"

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
		s.currBuf = <-s.bytesChan
	}

	if len(s.currBuf) == 0 {
		return 0, io.EOF
	}

	n := copy(dist, s.currBuf)
	s.currBuf = s.currBuf[n:]
	if n > 0 {
		go s.writeACK(uint16(n))
	}
	return n, nil
}
