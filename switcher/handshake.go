package switcher

import (
	"encoding/json"

	"github.com/net-agent/flex/packet"
)

type Request struct {
	Domain string
	Mac    string
	IV     packet.IV
}

type Response struct {
	IP uint16
	IV packet.IV
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

	iv := packet.GetIV()

	// response
	var resp Response
	resp.IP = ip
	resp.IV = iv
	respBuf, err := json.Marshal(&resp)
	if err != nil {
		return err
	}

	pbuf.SetPayload(respBuf)
	err = pc.WriteBuffer(pbuf)
	if err != nil {
		return err
	}

	packet.Xor(&iv, &req.IV)

	pc, err = packet.UpgradeCipher(pc, s.password, iv)
	if err != nil {
		return err
	}

	pbufChan := make(chan *packet.Buffer, 128)
	defer close(pbufChan)
	go s.routeLoop(ctx, pbufChan)

	// read loop
	for {
		pbuf, err = pc.ReadBuffer()
		if err != nil {
			return err
		}

		pbufChan <- pbuf
	}
}

func (s *Server) routeLoop(ctx *Context, pbufChan <-chan *packet.Buffer) {
	for pbuf := range pbufChan {
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
