package switcher

import (
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/vars"
)

// RunPbufLoopService
func RunPbufLoopService(s *Server, ctx *Context) error {
	for {
		pbuf, err := ctx.ReadBuffer()
		ctx.SetLastReadTime(time.Now())
		if err != nil {
			return err
		}

		// Stats: Bytes Received
		atomic.AddInt64(&ctx.Stats.BytesReceived, int64(pbuf.PayloadSize()+packet.HeaderSz))

		// Stats: Stream Count (Approximate, based on Open/Close commands)
		cmd := pbuf.Cmd()
		if cmd == packet.CmdOpenStream {
			atomic.AddInt32(&ctx.Stats.StreamCount, 1)
		} else if cmd == packet.CmdCloseStream {
			atomic.AddInt32(&ctx.Stats.StreamCount, -1)
		}

		// 目标IP不是Switcher的，直接进入转发流程
		if pbuf.DistIP() != vars.SwitcherIP {
			// 需要保证发送顺序，不能使用协程并行
			s.HandleDefaultPbuf(pbuf)
			continue
		}

		// 无需保证顺序
		go s.HandleSwitcherPbuf(ctx, pbuf)
	}
}
