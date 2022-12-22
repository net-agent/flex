package stream

import (
	"log"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v2/packet"
)

func (s *Stream) HandleCmdPushStreamData(pbuf *packet.Buffer) {
	s.rmut.RLock()
	defer s.rmut.RUnlock()

	if s.rclosed {
		log.Println("stream closed, pbuf of data ignored")
		return
	}

	if pbuf.PayloadSize() <= 0 {
		log.Println("payload is empty")
		return
	}

	select {
	case s.bytesChan <- pbuf.Payload:
		atomic.AddInt64(&s.counter.HandlePushDataSize, int64(pbuf.PayloadSize()))
		return
	case <-time.After(s.appendDataTimeout):
		log.Println("append data timeout")
		return
	}
}

func (s *Stream) HandleCmdPushStreamDataAck(pbuf *packet.Buffer) {
	size := pbuf.ACKInfo()
	atomic.AddInt32(&s.bucketSz, int32(size))
	atomic.AddInt64(&s.counter.HandlePushDataAckSum, int64(size))

	select {
	case s.bucketEv <- struct{}{}:
	default:
	}
}

// HandleCmdCloseStream 收到对端的close请求
// * 说明对端已经不会再发送数据，可以安全关闭读状态
// * 由于对端已经请求关闭，所以本地应该尽快关闭write状态，并告知对面
func (s *Stream) HandleCmdCloseStream(pbuf *packet.Buffer) {
	// step1：响应对端的close请求，停止继续读数据
	s.CloseRead()

	// step2：停止继续写入
	s.CloseWrite()

	// step3：回复closeAck，让对端停止读取
	s.SendCmdCloseAck()
}

// HandleCmdCloseStream 收到对端的closeAck应答
// * 说明对端已经响应本地发出的close请求，并且停止继续写入数据，所以可以安全关闭读状态
func (s *Stream) HandleCmdCloseStreamAck(pbuf *packet.Buffer) {
	s.closeAckCh <- struct{}{}
}
