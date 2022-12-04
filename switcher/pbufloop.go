package switcher

import (
	"log"
	"time"

	"github.com/net-agent/flex/v2/vars"
)

// RunPbufLoopService
func RunPbufLoopService(s *Server, ctx *Context) error {
	// 记录服务时长
	start := time.Now()
	log.Printf("context loop start. domain='%v' id='%v'\n", ctx.Domain, ctx.id)
	defer func() {
		dur := time.Since(start).Round(time.Second)
		log.Printf("context loop stop. domain='%v' id='%v' dur='%v'\n", ctx.Domain, ctx.id, dur)
	}()

	// 服务循环
	for {
		pbuf, err := ctx.Conn.ReadBuffer()
		ctx.LastReadTime = time.Now()
		if err != nil {
			return err
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
