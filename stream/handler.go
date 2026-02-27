package stream

import (
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v3/packet"
)

// Handler 是协议路由层所需的接口。
// StreamHub 和 pipe 中的路由函数通过此接口与 Stream 交互，
// 而不是依赖 *Stream 的全部方法集。
type Handler interface {
	HandleCmdPushStreamData(pbuf *packet.Buffer)
	HandleAckPushStreamData(pbuf *packet.Buffer)
	HandleCmdCloseStream(pbuf *packet.Buffer)
	HandleAckCloseStream(pbuf *packet.Buffer)
}

// HandleCmdPushStreamData 处理对端发送过来的数据包
func (s *Stream) HandleCmdPushStreamData(pbuf *packet.Buffer) {
	atomic.AddInt32(&s.state.RecvBufferCount, 1)
	s.rchanMu.RLock()
	defer s.rchanMu.RUnlock()

	if s.readClosed {
		s.logger.Info("stream closed")
		return
	}

	if pbuf.PayloadSize() <= 0 {
		s.logger.Info("payload is empty")
		return
	}

	timer := time.NewTimer(s.recvPushTimeout)
	defer timer.Stop()

	select {
	case s.recvQueue <- pbuf.Payload:
		atomic.AddInt64(&s.state.RecvDataSize, int64(pbuf.PayloadSize()))
		return
	case <-timer.C:
		s.logger.Error("append data timeout, closing stream to prevent window desync")
		// 数据丢失会导致流控窗口不一致（发送端窗口无法恢复），
		// 必须主动关闭 stream 让两端都能感知异常。
		go func() {
			s.CloseWrite()
			s.CloseRead()
		}()
		return
	}
}

func (s *Stream) HandleAckPushStreamData(pbuf *packet.Buffer) {
	atomic.AddInt32(&s.state.RecvBufferCount, 1)
	size := pbuf.DataACKSize()
	s.window.Release(int32(size))
	atomic.AddInt64(&s.state.RecvAckTotal, int64(size))
}

// HandleCmdCloseStream 收到对端的close请求
// * 说明对端已经不会再发送数据，可以安全关闭读状态
// * 由于对端已经请求关闭，所以本地应该尽快关闭write状态，并告知对面
func (s *Stream) HandleCmdCloseStream(pbuf *packet.Buffer) {
	atomic.AddInt32(&s.state.RecvBufferCount, 1)
	// step1：响应对端的close请求，停止继续读数据
	s.CloseRead()

	// step2：停止继续写入
	s.CloseWrite()

	// step3：回复closeAck，让对端停止读取
	s.sender.SendCloseAck()
}

func (s *Stream) HandleAckCloseStream(pbuf *packet.Buffer) {
	atomic.AddInt32(&s.state.RecvBufferCount, 1)
	select {
	case s.closeAckCh <- struct{}{}:
	default:
		// Already signaled or full, ignore duplicate ACKs
	}
}
