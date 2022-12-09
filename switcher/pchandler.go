package switcher

import (
	"errors"
	"log"
	"time"

	"github.com/net-agent/flex/v2/packet"
)

var (
	errHandlePCWriteFailed = errors.New("write to packet.Conn failed")
)

// HandlePacketConn
func (s *Server) HandlePacketConn(pc packet.Conn) error {
	defer pc.Close()

	var resp Response

	// 第一步：交换版本和签名信息，保证版本一致与认证安全
	ctx, err := HandleUpgradeRequest(pc, s)
	if err != nil {
		resp.ErrCode = -1
		resp.ErrMsg = err.Error()
		resp.WriteToPacketConn(pc)
		return err
	}

	// 第二步：将ctx映射到map中
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
		log.Printf("resp.WriteToPacketConn failed: %v\n", err)
		return errHandlePCWriteFailed
	}

	// 记录服务时长
	start := time.Now()
	log.Printf("context loop start. domain='%v' id='%v'\n", ctx.Domain, ctx.id)

	err = RunPbufLoopService(s, ctx)

	// 计算并打印时长
	dur := time.Since(start).Round(time.Second)
	log.Printf("context loop stop. domain='%v' id='%v' dur='%v'\n", ctx.Domain, ctx.id, dur)

	return err
}
