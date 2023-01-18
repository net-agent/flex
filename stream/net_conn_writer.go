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
	if len(buf) == 0 {
		return 0, nil
	}

	var wn, bufSize, bucketSize int

	// 按照一定大小对待发送的buf进行分片
	// 直到分片数据全部传输为止
	for {

		// 首先判断对端的传输桶大小
		// 当传输桶为零时，则代表对端无法消化当前的传输速度
		// 等待对端回传接收确认消息
		if atomic.LoadInt32(&s.bucketSz) <= 0 {
			select {
			case <-s.bucketEv: // 等待Bucket事件，事件到达后继续
			case <-time.After(s.waitDataAckTimeout):
				return wn, ErrWaitForDataAckTimeout
			}
		}

		// 确定本次传输的数据，从以下值中选取最小值进行分片：
		// * bucketSize 是对端的接收窗口剩余容量
		// * DefaultSplitSize 是默认的分片大小，不应该超过pbuf的最大长度65535
		// * bufSize 是剩余缓冲区的长度
		sliceSize := DefaultSplitSize

		bufSize = len(buf)
		if bufSize < sliceSize {
			sliceSize = bufSize
		}

		bucketSize = int(atomic.LoadInt32(&s.bucketSz))
		if bucketSize < sliceSize {
			sliceSize = bucketSize
		}

		n, err := s.write(buf[:sliceSize])
		if n > 0 {
			wn += n
			buf = buf[n:]
		}

		// 出现写错误立即返回
		if err != nil {
			return wn, err
		}

		// 如果buf的剩余长度已经为零，则代表所有数据已经发送完成
		if len(buf) <= 0 {
			return wn, nil
		}

		// 在继续下一个slice的传输前，主动让出调度权
		// 需要验证这样做是否有必要
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

	wn := len(buf)

	atomic.AddInt64(&s.state.ConnWriteSize, int64(wn))
	atomic.AddInt32(&s.bucketSz, -int32(wn))
	return wn, nil
}
