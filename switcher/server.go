package switcher

import (
	"errors"
	"sync"

	"github.com/net-agent/flex/packet"
)

type Server struct {
	nodeDomains sync.Map // map[string]*Context
	nodeIps     sync.Map // map[uint16]*Context
}

// AttachCtx 附加上下文
func (s *Server) AttachCtx(ctx *Context) error {
	_, loaded := s.nodeDomains.LoadOrStore(ctx.Domain, ctx)
	if loaded {
		return errors.New("domain exist")
	}

	_, loaded = s.nodeIps.LoadOrStore(ctx.IP, ctx)
	if loaded {
		s.nodeDomains.Delete(ctx.Domain)
		return errors.New("ip exist")
	}

	return nil
}

// DetachCtx 分离上下文
func (s *Server) DetachCtx(ctx *Context) {
	s.nodeDomains.Delete(ctx.Domain)
	s.nodeIps.Delete(ctx.IP)
}

// ServeConn
func (s *Server) ServeConn(pc packet.Conn) error {
	pbuf, err := pc.ReadBuffer()
	if err != nil {
		return err
	}

	ctx := &Context{
		Name:   "default",
		Domain: "sw-node-default",
		IP:     0,
		Conn:   pc,
	}

	err = s.AttachCtx(ctx)
	if err != nil {
		return err
	}
	defer s.DetachCtx(ctx)

	// response
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

		if pbuf.Head.Cmd() == packet.CmdAlive {
			continue
		}

		switch pbuf.Head.Cmd() {
		case packet.CmdOpenStream:
			go s.ResolveOpenCmd(ctx, pbuf)
		case packet.CmdPushStreamData:
			s.RouteData(pbuf)
		default:
			go s.Route(pbuf)
		}

	}
}

// ResolveOpenCmd 解析open命令
func (s *Server) ResolveOpenCmd(caller *Context, pbuf *packet.Buffer) {
	distDomain := string(pbuf.Payload)
	if distDomain == "" {
		pbuf.SetPayload([]byte(caller.Domain))
		s.Route(pbuf)
		return
	}

	//
	// 进行域名解析，确定目标节点
	//
	it, found := s.nodeDomains.Load(distDomain)
	if !found {
		return
	}
	distCtx := it.(*Context)

	pbuf.SetDistIP(distCtx.IP)
	pbuf.SetPayload([]byte(caller.Domain))
	distCtx.Conn.WriteBuffer(pbuf)
}

// Route 根据DistIP路由数据包，这些包可以无序
func (s *Server) Route(pbuf *packet.Buffer) {
	distIP := pbuf.GetDistIP()
	it, found := s.nodeIps.Load(distIP)
	if !found {
		// 此处记录找不到ctx的错误，但不退出循环
		return
	}
	ctx := it.(*Context)
	err := ctx.Conn.WriteBuffer(pbuf)
	if err != nil {
		// 此处记录写入失败的日志，但不退出循环
		return
	}
}

// RouteData 根据DistIP路由数据包，这些包需要保持有序
func (s *Server) RouteData(pbuf *packet.Buffer) {
	s.Route(pbuf) // todo
}
