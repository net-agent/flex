package stream

import (
	"errors"
	"sync/atomic"

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

// HandleCmdPushStreamData 处理对端发送过来的数据包。
//
// 所有权约定：本方法总是消费 pbuf（成功入队由流负责归还，
// 失败在本方法内归还），调用方不得再访问 pbuf。
//
// 入队永不阻塞，因此单条慢流的接收积压不会拖住 dispatcher goroutine，
// 也就不会阻塞同一节点上的其他流（队头阻塞）。
func (s *Stream) HandleCmdPushStreamData(pbuf *packet.Buffer) {
	atomic.AddInt32(&s.state.RecvBufferCount, 1)

	size := len(pbuf.Payload)
	if size == 0 {
		s.logger.Debug("drop empty data packet", "sid", pbuf.SIDStr())
		packet.PutBuffer(pbuf)
		return
	}

	if err := s.recv.append(pbuf); err != nil {
		packet.PutBuffer(pbuf)
		switch {
		case errors.Is(err, errRecvClosed):
			// 读侧已关闭：对端很快会收到 Close/CloseAck，丢弃即可
			s.logger.Debug("drop data packet on closed stream", "sid", pbuf.SIDStr())
		case errors.Is(err, ErrRecvBufferFull):
			// 对端在窗口之外继续发送（或本地对端实现有 bug）。
			// 静默丢包会造成两端窗口记账永久不一致，必须主动关闭并通知对端。
			s.logger.Error("recv buffer overflow, closing stream",
				"sid", pbuf.SIDStr(), "buffered", s.recv.pending(), "limit", s.recv.limit)
			s.failOnce.Do(func() { go s.failClose() })
		}
		return
	}

	atomic.AddInt64(&s.state.RecvDataSize, int64(size))
}

// failClose 在检测到协议级异常时主动断开并向对端发送 Close，
// 让两端都能感知失败，而不是一方静默丢弃数据、另一方继续写入。
func (s *Stream) failClose() {
	if s.logger != nil {
		s.logger.Warn("closing stream on protocol error", "reason", "overflow")
	}
	s.SetCloseReason("overflow")
	_ = s.sender.SendClose()
	_ = s.CloseWrite()
	_ = s.CloseRead()
	s.recv.discard()
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
	// 关闭由对端发起，预设原因（first-wins，不被后续 CloseRead/CloseWrite 的 "local" 覆盖）
	s.SetCloseReason("remote")

	// step1：响应对端的close请求，停止继续读数据。
	// 注意：对端可能"先发数据、后发 Close"，此时缓冲区里的数据仍应可读，
	// 因此这里不能丢弃未消费数据（discard=false）。
	_ = s.closeRead(false)

	// step2：停止继续写入
	_ = s.CloseWrite()

	// step3：回复closeAck，让对端停止读取
	_ = s.sender.SendCloseAck()
}

func (s *Stream) HandleAckCloseStream(pbuf *packet.Buffer) {
	atomic.AddInt32(&s.state.RecvBufferCount, 1)
	select {
	case s.closeAckCh <- struct{}{}:
	default:
		// Already signaled or full, ignore duplicate ACKs
	}
}
