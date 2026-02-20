package switcher

import (
	"errors"
	"fmt"
	"strings"

	"github.com/net-agent/flex/v2/handshake"
	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/vars"
)

var (
	errDomainNotFound    = errors.New("domain not found")
	errInvalidContextIP  = errors.New("invalid context ip")
	errContextIPNotFound = errors.New("context ip not found")
	errResolveDomainFailed = errors.New("resolve domain failed")
)

// GetContextByDomain 根据domain获取已经连接的上下文
func (s *Server) GetContextByDomain(domain string) (*Context, error) {
	domain = strings.ToLower(domain)
	if handshake.IsInvalidDomain(domain) {
		return nil, handshake.ErrInvalidDomain
	}

	s.domainMu.Lock()
	ctx, found := s.nodeDomains[domain]
	s.domainMu.Unlock()
	if !found {
		return nil, errDomainNotFound
	}

	return ctx, nil
}

// GetContextByIP 根据IP获取已经连接的上下文
func (s *Server) GetContextByIP(ip uint16) (*Context, error) {
	if ip == vars.LocalIP || ip == vars.SwitcherIP {
		return nil, errInvalidContextIP
	}

	s.ipMu.Lock()
	ctx, found := s.nodeIps[ip]
	s.ipMu.Unlock()
	if !found {
		return nil, errContextIPNotFound
	}
	return ctx, nil
}

// HandleDefaultPbuf 直接转发不需要特殊处理的数据包
func (s *Server) HandleDefaultPbuf(pbuf *packet.Buffer) {
	dist, err := s.GetContextByIP(pbuf.DistIP())
	if err != nil {
		s.PopupWarning("route pbuf failed", err.Error())
		return
	}

	// 有一定的失败可能，但对于服务端的状态来说不关键
	err = dist.WriteBuffer(pbuf)
	if err != nil {
		s.PopupWarning("write to dist failed", err.Error())
	}
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
			s.HandleAckPingDomain(ctx, pbuf)
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

// HandleAckPingDomain
func (s *Server) HandleAckPingDomain(caller *Context, pbuf *packet.Buffer) {
	port := pbuf.DistPort()
	it, found := caller.pingBack.Load(port)
	if !found {
		s.PopupWarning("port not found", fmt.Sprintf("port=%v", port))
		return
	}
	ch, ok := it.(chan *packet.Buffer)
	if !ok {
		return
	}

	// Defensive send: channel may be closed or full if ping timed out
	select {
	case ch <- pbuf:
	default:
	}
}

// HandleCmdOpenStream 处理OpenStream命令，需要根据domain信息进行转发
func (s *Server) HandleCmdOpenStream(caller *Context, pbuf *packet.Buffer) {
	distDomain := string(pbuf.Payload)

	distCtx, err := s.GetContextByDomain(distDomain)
	if err != nil {
		s.PopupWarning(fmt.Sprintf("resove domain='%v' failed", distDomain), err.Error())
		pbuf.SetOpenACK(errResolveDomainFailed.Error())
		pbuf.SetSrcIP(0)
		caller.WriteBuffer(pbuf)
		return
	}

	pbuf.SetDistIP(distCtx.IP)
	pbuf.SetPayload([]byte(caller.Domain))
	distCtx.WriteBuffer(pbuf)
}
