package switcher

import (
	"encoding/json"
	"log"

	"github.com/net-agent/flex/v2/packet"
)

// ServeConn
func (s *Server) ServeConn(pc packet.Conn) (retErr error) {
	defer func() {
		if retErr != nil {
			var resp Response
			resp.ErrCode = -1
			resp.ErrMsg = retErr.Error()
			resp.IP = 0
			data, err := json.Marshal(&resp)
			if err == nil {
				pbuf := packet.NewBuffer(nil)
				pbuf.SetPayload(data)
				pc.WriteBuffer(pbuf)
			}
		}
		pc.Close()
	}()

	// 第一步：交换版本和签名信息，保证版本一致与认证安全
	ctx, err := UpgradeHandler(pc, s)
	if err != nil {
		return err
	}

	// 第二步：将ctx映射到map中
	err = s.AttachCtx(ctx)
	if err != nil {
		return err
	}
	defer s.DetachCtx(ctx)

	// 第三步：应答客户端
	var resp Response
	resp.IP = ctx.IP
	resp.Version = packet.VERSION
	err = resp.WriteToPacketConn(pc)
	if err != nil {
		log.Printf("node handshake failed, WriteBuffer err: %v\n", err)
		return nil
	}

	return RunPbufLoopService(s, ctx)
}
