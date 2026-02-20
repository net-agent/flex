package switcher

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v2/handshake"
	"github.com/net-agent/flex/v2/packet"
)

var (
	errHandlePCWriteFailed = errors.New("write to packet.Conn failed")
)

type OnLoopStartHandler func(ctx *Context)
type OnLoopStopHandler func(ctx *Context, duration time.Duration)

// HandlePacketConn
func (s *Server) HandlePacketConn(pc packet.Conn, onStart OnLoopStartHandler, onStop OnLoopStopHandler) error {
	defer pc.Close()

	var resp handshake.Response

	// 第一步：交换版本和签名信息，保证版本一致与认证安全
	req, err := handshake.HandleUpgradeRequest(pc, s.password)
	if err != nil {
		resp.ErrCode = -1
		resp.ErrMsg = err.Error()
		resp.WriteToPacketConn(pc)
		return err
	}

	// 第二步：将ctx映射到map中
	ctx := NewContext(int(atomic.AddInt32(&s.nextCtxID, 1)), pc, req.Domain, req.Mac)
	err = s.AttachCtx(ctx)
	if err != nil {
		resp.ErrCode = -2
		resp.ErrMsg = err.Error()
		resp.WriteToPacketConn(pc)
		return err
	}
	defer s.DetachCtx(ctx)

	// 第三步：应答客户端
	resp.ErrCode = 0
	resp.IP = ctx.IP
	resp.Version = packet.VERSION
	err = resp.WriteToPacketConn(pc)
	if err != nil {
		s.PopupWarning("response client failed", err.Error())
		return errHandlePCWriteFailed
	}

	// 记录服务时长
	start := time.Now()
	if onStart != nil {
		onStart(ctx)
	}

	err = RunPbufLoopService(s, ctx)

	// 计算并打印时长
	dur := time.Since(start).Round(time.Second)
	if onStop != nil {
		onStop(ctx, dur)
	}

	return err
}
