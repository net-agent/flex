package stream

import (
	"errors"
	"sync/atomic"

	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/wire"
)

// Handler 是协议路由层所需的旧接口。新代码应使用 Stream 的 HandleData /
// HandleAck / HandleClose / HandleCloseAck，直接传递 wire.Frame。
type Handler interface {
	HandleCmdPushStreamData(pbuf *packet.Buffer)
	HandleAckPushStreamData(pbuf *packet.Buffer)
	HandleCmdCloseStream(pbuf *packet.Buffer)
	HandleAckCloseStream(pbuf *packet.Buffer)
}

// HandleData processes an inbound data frame.
//
// Ownership: the method always consumes f (successful enqueue transfers it to
// the receive queue; on failure it is released here). The caller must not use f
// afterwards.
//
// Enqueue never blocks, so one slow stream cannot stall the dispatcher
// goroutine and head-of-line-block other streams on the same node.
func (s *Stream) HandleData(f *wire.Frame) {
	atomic.AddInt32(&s.state.RecvBufferCount, 1)

	size := len(f.Payload())
	if size == 0 {
		s.logger.Debug("drop empty data packet", "sid", f.SID().String())
		f.Release()
		return
	}

	if err := s.recv.append(f); err != nil {
		sid := f.SID().String()
		f.Release()
		switch {
		case errors.Is(err, errRecvClosed):
			// 读侧已关闭：对端很快会收到 Close/CloseAck，丢弃即可
			s.logger.Debug("drop data packet on closed stream", "sid", sid)
		case errors.Is(err, ErrRecvBufferFull):
			// 对端在窗口之外继续发送（或本地对端实现有 bug）。
			// 静默丢包会造成两端窗口记账永久不一致，必须主动关闭并通知对端。
			s.logger.Error("recv buffer overflow, closing stream",
				"sid", sid, "buffered", s.recv.pending(), "limit", s.recv.limit)
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

// HandleAck processes a data ACK frame. The acknowledged byte count lives in
// the header; the frame has no payload.
func (s *Stream) HandleAck(f *wire.Frame) {
	atomic.AddInt32(&s.state.RecvBufferCount, 1)
	size := f.AckSize()
	f.Release()
	s.window.Release(int32(size))
	atomic.AddInt64(&s.state.RecvAckTotal, int64(size))
}

// HandleClose 收到对端的 close 请求：
//   - 对端不会再发送数据，可以安全关闭读状态；
//   - 本地应尽快关闭 write 状态，并回复 CloseAck。
func (s *Stream) HandleClose(f *wire.Frame) {
	atomic.AddInt32(&s.state.RecvBufferCount, 1)
	f.Release()

	// 关闭由对端发起，预设原因（first-wins，不被后续 CloseRead/CloseWrite 的 "local" 覆盖）
	s.SetCloseReason("remote")

	// step1：回复 CloseAck，让对端停止读取。
	// 必须先于 closeRead/CloseWrite：对端可能已经先调用 Close()（写侧已关），
	// 此时 closeRead 会立即触发 onDetach；测试/兼容层的 Pipe 可能在回调中
	// 关闭底层连接，ack 尚未写出就会让对端等到 close-ack 超时。
	_ = s.sender.SendCloseAck()

	// step2：停止继续读数据。对端可能“先发数据、后发 Close”，
	// 缓冲区里的数据仍应可读，故 discard=false。
	_ = s.closeRead(false)

	// step3：停止继续写入
	_ = s.CloseWrite()
}

// HandleCloseAck processes the peer's acknowledgement of our Close.
func (s *Stream) HandleCloseAck(f *wire.Frame) {
	atomic.AddInt32(&s.state.RecvBufferCount, 1)
	f.Release()
	select {
	case s.closeAckCh <- struct{}{}:
	default:
		// Already signaled or full, ignore duplicate ACKs
	}
}

// --- legacy packet.Buffer adapters -----------------------------------------

func bufferAddrs(pbuf *packet.Buffer) (src, dst wire.Addr) {
	return wire.Addr{IP: pbuf.SrcIP(), Port: pbuf.SrcPort()},
		wire.Addr{IP: pbuf.DistIP(), Port: pbuf.DistPort()}
}

func (s *Stream) HandleCmdPushStreamData(pbuf *packet.Buffer) {
	if pbuf == nil {
		return
	}
	p := append([]byte(nil), pbuf.Payload...)
	src, dst := bufferAddrs(pbuf)
	packet.PutBuffer(pbuf)
	f, err := wire.NewData(src, dst, p)
	if err != nil {
		return
	}
	s.HandleData(f)
}

// HandleAckPushStreamData keeps the legacy ownership contract: the caller
// (dispatcher / routeAck) puts pbuf after the handler returns.
func (s *Stream) HandleAckPushStreamData(pbuf *packet.Buffer) {
	if pbuf == nil {
		return
	}
	size := pbuf.DataACKSize()
	s.HandleAck(wire.NewAckData(size, wire.Addr{}, wire.Addr{}))
}

// HandleCmdCloseStream keeps the legacy ownership contract: the caller puts
// pbuf after the handler returns.
func (s *Stream) HandleCmdCloseStream(pbuf *packet.Buffer) {
	f, err := wire.NewEmpty(wire.CmdCloseStream, wire.Addr{}, wire.Addr{})
	if err != nil {
		return
	}
	s.HandleClose(f)
}

// HandleAckCloseStream keeps the legacy ownership contract: the caller puts
// pbuf after the handler returns.
func (s *Stream) HandleAckCloseStream(pbuf *packet.Buffer) {
	f, err := wire.NewEmpty(wire.AckCloseStream, wire.Addr{}, wire.Addr{})
	if err != nil {
		return
	}
	s.HandleCloseAck(f)
}
