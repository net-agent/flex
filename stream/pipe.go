package stream

import (
	"errors"
	"io"
	"log/slog"
	"sync"

	"github.com/net-agent/flex/v3/packet"
)

func Pipe() (*Stream, *Stream) {
	pc1, pc2 := packet.Pipe()

	copybuf := func(s *Stream, pc packet.Reader) {
		cmdCh := make(chan *packet.Buffer, 1024)
		ackCh := make(chan *packet.Buffer, 1024)

		go routeCmd(s, cmdCh)
		go routeAck(s, ackCh)
		demuxPackets(cmdCh, ackCh, pc)
	}

	s1 := NewDialStream(pc1, "test1", 1, 1, "test2", 2, 2, 0)
	go copybuf(s1, pc1)

	s2 := NewAcceptStream(pc2, "test2", 2, 2, "test1", 1, 1, 0)
	go copybuf(s2, pc2)

	// Pipe is primarily a test/legacy helper. When both directions of either
	// stream are closed, tear down the underlying packet conns so demux/route
	// goroutines exit. Without this, the goroutines leak and can race with the
	// next test through the shared packet.Buffer pool.
	var (
		closeOnce sync.Once
		detachMu  sync.Mutex
		detached  int
	)
	closeBoth := func() {
		closeOnce.Do(func() {
			_ = pc1.Close()
			_ = pc2.Close()
		})
	}
	onDetach := func() {
		detachMu.Lock()
		detached++
		both := detached == 2
		detachMu.Unlock()
		// Close only after both ends have detached. Closing the transport when
		// the passive side detaches can drop the CloseAck that the active side
		// is still waiting for.
		if both {
			closeBoth()
		}
	}
	s1.SetOnDetach(onDetach)
	s2.SetOnDetach(onDetach)

	return s1, s2
}

func routeCmd(h Handler, ch chan *packet.Buffer) {
	for pbuf := range ch {
		switch pbuf.Cmd() {
		case packet.CmdPushStreamData:
			// 数据包所有权转移给流，由流负责归还
			h.HandleCmdPushStreamData(pbuf)
		case packet.CmdCloseStream:
			h.HandleCmdCloseStream(pbuf)
			packet.PutBuffer(pbuf)
		default:
			slog.Warn("unexpected pbuf cmd", "header", pbuf.HeaderString())
			packet.PutBuffer(pbuf)
		}
	}
}
func routeAck(h Handler, ch chan *packet.Buffer) {
	for pbuf := range ch {
		switch pbuf.Cmd() {
		case packet.AckPushStreamData:
			h.HandleAckPushStreamData(pbuf)
			packet.PutBuffer(pbuf)
		case packet.AckCloseStream:
			h.HandleAckCloseStream(pbuf)
			packet.PutBuffer(pbuf)
		default:
			slog.Warn("unexpected pbuf ack", "header", pbuf.HeaderString())
			packet.PutBuffer(pbuf)
		}
	}
}
func demuxPackets(cmdCh, ackCh chan *packet.Buffer, pc packet.Reader) {
	if cmdCh == nil || ackCh == nil || pc == nil {
		return
	}
	defer close(cmdCh)
	defer close(ackCh)

	for {
		pbuf, err := pc.ReadBuffer()
		if err != nil {
			if !errors.Is(err, io.EOF) {
				slog.Warn("demuxPackets read error", "error", err)
			}
			return
		}
		// Hand ownership of a private copy to the routing goroutine. The next
		// ReadBuffer may immediately reuse the pooled buffer's backing array;
		// sending the pooled buffer itself would race with routeCmd/routeAck
		// copying it (this helper is used by tests and legacy callers).
		own := packet.NewBuffer()
		own.Head = pbuf.Head
		own.Payload = append(own.Payload[:0], pbuf.Payload...)
		packet.PutBuffer(pbuf)
		if own.IsACK() {
			ackCh <- own
		} else {
			cmdCh <- own
		}
	}
}
