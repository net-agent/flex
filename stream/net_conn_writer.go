package stream

import (
	"errors"
	"runtime"
	"sync/atomic"
	"time"
)

var (
	ErrWaitForDataAckTimeout = errors.New("wait for dataAck timeout")
)

func (s *Stream) Write(buf []byte) (int, error) {
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

			case <-time.After(s.waitDataAckTimeout):
				return wn, ErrWaitForDataAckTimeout
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
func (s *Stream) write(buf []byte) (int, error) {
	s.wmut.Lock()
	defer s.wmut.Unlock()

	if s.wclosed {
		return 0, ErrWriterIsClosed
	}

	err := s.SendCmdData(buf)
	if err != nil {
		return 0, err
	}

	atomic.AddInt64(&s.state.ConnWriteSize, int64(len(buf)))
	return len(buf), nil
}
