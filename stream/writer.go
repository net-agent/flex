package stream

import (
	"errors"
	"fmt"
	"log"
	"runtime"
	"sync/atomic"
	"time"
)

func (s *Conn) CloseRead() error {
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
func (s *Conn) CloseWrite() error {
	s.wmut.Lock()
	defer s.wmut.Unlock()

	if s.wclosed {
		return errors.New("close on closed conn")
	}

	s.wclosed = true
	return nil
}

func (s *Conn) Write(buf []byte) (int, error) {
	var buflen = len(buf)
	var wn int
	const sz = DefaultSplitSize
	var slice []byte

	for {
		if len(buf) <= 0 {
			return wn, nil
		}
		if atomic.LoadInt32(&s.bucketSz) <= 0 {
			select {
			case <-s.bucketEv:
				continue
			case <-time.After(time.Second * 5):

				return wn, fmt.Errorf("bucket dry. writed=%v/%v state=%v", wn, buflen, s.State())
			}
		}

		// 确定本次传输的数据
		// MIN(sz, bucketSz, len(buf))
		maxSz := atomic.LoadInt32(&s.bucketSz)
		if sz < maxSz {
			maxSz = sz
		}
		if len(buf) > int(maxSz) {
			slice = buf[:maxSz]
		} else {
			slice = buf
		}

		n, err := s.write(slice)
		atomic.AddInt32(&s.bucketSz, -int32(n))
		wn += n
		if err != nil {
			return wn, err
		}
		buf = buf[n:]

		// 让其它stream有机会传输
		runtime.Gosched()
	}
}

// write 一次调用最大支持64KB传输
func (s *Conn) write(buf []byte) (int, error) {
	s.wmut.Lock()
	defer s.wmut.Unlock()

	if s.wclosed {
		return 0, errors.New("write on closed conn")
	}

	err := s.SendCmdData(buf)
	if err != nil {
		return 0, err
	}

	atomic.AddInt64(&s.counter.Write, int64(len(buf)))
	return len(buf), nil
}

func (s *Conn) writeACK(n uint16) {
	s.wmut.Lock()
	defer s.wmut.Unlock()

	if s.wclosed {
		return
	}

	err := s.SendCmdDataAck(n)
	if err != nil {
		log.Println("write push-ack failed")
	}

	atomic.AddInt64(&s.counter.WriteAck, int64(n))
}
