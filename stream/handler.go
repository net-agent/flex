package stream

import (
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v2/packet"
)

// HandleCmdPushStreamData 处理对端发送过来的数据包
func (s *Stream) HandleCmdPushStreamData(pbuf *packet.Buffer) {
	atomic.AddInt32(&s.state.HandledBufferCount, 1)
	s.rmut.RLock()
	defer s.rmut.RUnlock()

	if s.rclosed {
		s.PopupInfo("stream closed")
		return
	}

	if pbuf.PayloadSize() <= 0 {
		s.PopupInfo("payload is empty")
		return
	}

	select {
	case s.bytesChan <- pbuf.Payload:
		atomic.AddInt64(&s.state.HandledDataSize, int64(pbuf.PayloadSize()))
		return
	case <-time.After(s.appendDataTimeout):
		s.PopupInfo("append data timeout")
		return
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

func (s *Stream) HandleAckCloseStream(pbuf *packet.Buffer) {
	atomic.AddInt32(&s.state.HandledBufferCount, 1)
	select {
	case s.closeAckCh <- struct{}{}:
	default:
		// Already signaled or full, ignore duplicate ACKs
	}
}
