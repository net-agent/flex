package switcher

import (
	"errors"
	"log"
	"strings"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/vars"
)

var (
	errDomainNotFound       = errors.New("domain not found")
	errConvertContextFailed = errors.New("convert domain context failed")
	errInvalidContextIP     = errors.New("invalid context ip")
	errContextIPNotFound    = errors.New("context ip not found")
	errResolveDomainFailed  = errors.New("resolve domain failed")
)

// GetContextByDomain 根据domain获取已经连接的上下文
func (s *Server) GetContextByDomain(domain string) (*Context, error) {
	domain = strings.ToLower(domain)
	if IsInvalidDomain(domain) {
		return nil, errInvalidDomain
	}

	it, found := s.nodeDomains.Load(domain)
	if !found {
		return nil, errDomainNotFound
	}
	ctx, ok := it.(*Context)
	if !ok {
		return nil, errConvertContextFailed
	}

	return ctx, nil
}

// GetContextByIP 根据IP获取已经连接的上下文
func (s *Server) GetContextByIP(ip uint16) (*Context, error) {
	if ip == vars.LocalIP || ip == vars.SwitcherIP {
		return nil, errInvalidContextIP
	}

	it, found := s.nodeIps.Load(ip)
	if !found {
		return nil, errContextIPNotFound
	}
	ctx, ok := it.(*Context)
	if !ok {
		return nil, errConvertContextFailed
	}
	return ctx, nil
}

// HandleDefaultPbuf 直接转发不需要特殊处理的数据包
func (s *Server) HandleDefaultPbuf(pbuf *packet.Buffer) {
	dist, err := s.GetContextByIP(pbuf.DistIP())
	if err != nil {
		log.Printf("route pbuf failed: %v\n", err)
		return
	}

	// 有一定的失败可能，但对于服务端的状态来说不关键
	dist.WriteBuffer(pbuf)
}

// HandleSwitcherPbuf 处理发送给switcher的数据包
func (s *Server) HandleSwitcherPbuf(ctx *Context, pbuf *packet.Buffer) {
	// 针对发送给Switcher的包进行处理
	// 主要处理的类型有：CmdAlive、CmdOpenStream、CmdPingDomain
	switch pbuf.CmdType() {
	case packet.CmdOpenStream:
		if !pbuf.IsACK() {
			s.HandleCmdOpenStream(ctx, pbuf)
		}
		// 当前情况下不应该出现应答给switcher的open ack

	case packet.CmdPingDomain:
		if pbuf.IsACK() {
			s.HandleCmdPingDomainAck(ctx, pbuf)
		} else {
			s.HandleCmdPingDomain(ctx, pbuf)
		}
	}
}

// HandleCmdPingDomain 处理PingDomain命令，需要根据domain信息进行转发
func (s *Server) HandleCmdPingDomain(caller *Context, pbuf *packet.Buffer) {
	domain := string(pbuf.Payload)
	if domain == "" {
		pbuf.SwapSrcDist()
		pbuf.SetCmd(pbuf.Cmd() | packet.CmdACKFlag)
		pbuf.SetPayload(nil)
		caller.WriteBuffer(pbuf)
		return
	}

	dist, err := s.GetContextByDomain(string(pbuf.Payload))
	if err != nil {
		pbuf.SwapSrcDist()
		pbuf.SetCmd(pbuf.Cmd() | packet.CmdACKFlag)
		pbuf.SetPayload([]byte(err.Error()))
		caller.WriteBuffer(pbuf)
		return
	}

	pbuf.SetDistIP(dist.IP)
	dist.WriteBuffer(pbuf)
}

// HandleCmdPingDomainAck
func (s *Server) HandleCmdPingDomainAck(caller *Context, pbuf *packet.Buffer) {
	port := pbuf.DistPort()
	it, found := caller.pingBack.Load(port)
	if !found {
		return
	}
	ch, ok := it.(chan *packet.Buffer)
	if !ok {
		return
	}

	ch <- pbuf
}

// HandleCmdOpenStream 处理OpenStream命令，需要根据domain信息进行转发
func (s *Server) HandleCmdOpenStream(caller *Context, pbuf *packet.Buffer) {
	distDomain := string(pbuf.Payload)

	distCtx, err := s.GetContextByDomain(distDomain)
	if err != nil {
		log.Printf("%v. domain='%v'\n", errResolveDomainFailed, distDomain)
		pbuf.SetOpenACK(errResolveDomainFailed.Error())
		pbuf.SetSrcIP(0)
		caller.WriteBuffer(pbuf)
		return
	}

	pbuf.SetDistIP(distCtx.IP)
	pbuf.SetPayload([]byte(caller.Domain))
	distCtx.WriteBuffer(pbuf)
}
