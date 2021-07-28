package switcher

import (
	"encoding/json"

	"github.com/net-agent/flex/packet"
)

type Request struct {
	Domain string
	Mac    string
}

type Response struct {
	IP uint16
}

// ServeConn
func (s *Server) ServeConn(pc packet.Conn) error {
	defer pc.Close()

	pbuf, err := pc.ReadBuffer()
	if err != nil {
		return err
	}

	var req Request
	err = json.Unmarshal(pbuf.Payload, &req)
	if err != nil {
		return err
	}

	ip, err := s.GetIP(req.Domain)
	if err != nil {
		return err
	}
	defer s.FreeIP(ip)

	ctx := NewContext("default", req.Domain, ip, pc)

	err = s.AttachCtx(ctx)
	if err != nil {
		return err
	}
	defer s.DetachCtx(ctx)

	// response
	var resp Response
	resp.IP = ip
	respBuf, err := json.Marshal(&resp)
	if err != nil {
		return err
	}

	pbuf.SetPayload(respBuf)
	err = pc.WriteBuffer(pbuf)
	if err != nil {
		return err
	}

	// read loop
	for {
		pbuf, err = pc.ReadBuffer()
		if err != nil {
			return err
		}

		if pbuf.Cmd() == packet.CmdAlive {
			continue
		}
		if pbuf.Cmd() == packet.CmdOpenStream {
			go s.ResolveOpenCmd(ctx, pbuf)
			continue
		}

		s.RouteBuffer(pbuf)
	}
}
