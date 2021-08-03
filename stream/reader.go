package stream

import (
	"fmt"
	"io"
	"log"
	"sync/atomic"
	"time"
)

func (s *Conn) AppendData(buf []byte) {
	if len(buf) > 0 && !s.rclosed {
		select {
		case s.bytesChan <- buf:
			atomic.AddInt64(&s.counter.AppendData, int64(len(buf)))
		case <-time.After(DefaultAppendDataTimeout):
			log.Printf("append data timeout")
		}

	} else {
		log.Printf("append data failed\n")
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

		case <-time.After(DefaultReadTimeout):
			return 0, fmt.Errorf("read timeout. %v", s.State())
		}
	}

	n := copy(dist, s.currBuf)
	s.counter.Read += int64(n)
	s.currBuf = s.currBuf[n:]
	if n > 0 {
		go s.writeACK(uint16(n))
	}
	return n, nil
}
