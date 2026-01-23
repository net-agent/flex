package switcher

import (
	"fmt"
	"time"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/vars"
)

// RunPbufLoopService
func RunPbufLoopService(s *Server, ctx *Context) error {
	for {
		pbuf, err := ctx.Conn.ReadBuffer()
		ctx.LastReadTime = time.Now()
		if err != nil {
			return err
		}

		if ctx.IsResetSID(pbuf.SID()) {
			s.PopupInfo(fmt.Sprintf("reset sid='%v'", pbuf.SID()))
			continue
		}

		if pbuf.Cmd() == packet.CmdReset {
			ctx.SetResetSID(pbuf.SID())
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
