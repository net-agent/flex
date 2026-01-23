package stream

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v2/packet"
)

// HandleCmdPushStreamData 处理对端发送过来的数据包
func (s *Stream) HandleCmdPushStreamData(pbuf *packet.Buffer) error {
	atomic.AddInt32(&s.state.HandledBufferCount, 1)
	s.rmut.RLock()
	defer s.rmut.RUnlock()

	if s.rclosed {
		return ErrReaderIsClosed
	}

	if pbuf.PayloadSize() <= 0 {
		s.PopupInfo("payload is empty")
		return nil
	}

	timeoutTick := time.NewTicker(s.appendDataTimeout)
	defer timeoutTick.Stop()

	select {
	case s.bytesChan <- pbuf.Payload:
		atomic.AddInt64(&s.state.HandledDataSize, int64(pbuf.PayloadSize()))
		return nil
	case <-timeoutTick.C:
		s.PopupInfo(fmt.Sprintf("append data timeout. index=%v rclosed=%v wclosed=%v",
			s.state.Index, s.rclosed, s.wclosed))
		// 这里实际代表了严重的数据拥塞，需要告诉发送方停止发送，并且关闭连接
		// 因为运行到此处代码即代表数据包存在丢失，继续维持连接已无意义
		return ErrPushToByteChanTimeout
	}
}

func (s *Stream) HandleAckPushStreamData(pbuf *packet.Buffer) {
	atomic.AddInt32(&s.state.HandledBufferCount, 1)
	size := pbuf.ACKInfo()
	atomic.AddInt32(&s.bucketSz, int32(size))
	atomic.AddInt64(&s.state.HandledDataAckSum, int64(size))

	select {
	case s.bucketEv <- struct{}{}:
	default:
	}
}

// HandleCmdCloseStream 收到对端的close请求
// * 说明对端已经不会再发送数据，可以安全关闭读状态
// * 由于对端已经请求关闭，所以本地应该尽快关闭write状态，并告知对面
func (s *Stream) HandleCmdCloseStream(pbuf *packet.Buffer) {
	atomic.AddInt32(&s.state.HandledBufferCount, 1)
	// step1：响应对端的close请求，停止继续读数据
	s.CloseRead()

	// step2：停止继续写入
	s.CloseWrite()

	// step3：回复closeAck，让对端停止读取
	s.SendCmdCloseAck()
}

// HandleCmdCloseStream 收到对端的closeAck应答
// * 说明对端已经响应本地发出的close请求，并且停止继续写入数据，所以可以安全关闭读状态
func (s *Stream) HandleAckCloseStream(pbuf *packet.Buffer) {
	atomic.AddInt32(&s.state.HandledBufferCount, 1)
	s.closeAckCh <- struct{}{}
}
