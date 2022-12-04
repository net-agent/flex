package switcher

import (
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/vars"
)

// GetContextByDomain 根据domain获取已经连接的上下文
func (s *Server) GetContextByDomain(domain string) (*Context, error) {
	domain = strings.ToLower(domain)
	if domain == "" {
		return nil, errors.New("empty domain string")
	}

	it, found := s.nodeDomains.Load(domain)
	if !found {
		return nil, fmt.Errorf("resolve domain='%v' failed", domain)
	}
	ctx, ok := it.(*Context)
	if !ok {
		return nil, errors.New("convert domain context failed")
	}

	return ctx, nil
}

// GetContextByIP 根据IP获取已经连接的上下文
func (s *Server) GetContextByIP(ip uint16) (*Context, error) {
	if ip == vars.LocalIP || ip == vars.SwitcherIP {
		return nil, fmt.Errorf("invalid context ip='%v'", ip)
	}

	it, found := s.nodeIps.Load(ip)
	if !found {
		return nil, fmt.Errorf("context not found with ip='%v'", ip)
	}
	ctx, ok := it.(*Context)
	if !ok {
		return nil, errors.New("convert as context failed")
	}
	return ctx, nil
}

// HandleDefaultPbuf 直接转发不需要特殊处理的数据包
func (s *Server) HandleDefaultPbuf(pbuf *packet.Buffer) {
	dist, err := s.GetContextByIP(pbuf.DistIP())
	if err != nil {
		log.Printf("handle default pbuf failed: %v\n", err)
		return
	}

	dist.WriteBuffer(pbuf)
}

// HandleSwitcherPbuf 处理发送给switcher的数据包
func (s *Server) HandleSwitcherPbuf(ctx *Context, pbuf *packet.Buffer) {
	// 针对发送给Switcher的包进行处理
	// 主要处理的类型有：CmdAlive、CmdOpenStream、CmdPingDomain
	switch pbuf.CmdType() {
	case packet.CmdOpenStream:
		if pbuf.IsACK() {
		} else {
			s.HandleCmdOpenStream(ctx, pbuf)
		}

	case packet.CmdAlive:
		if pbuf.IsACK() {
			// 类型二：处理服务端探活请求的应答
			it, found := ctx.pingBack.Load(pbuf.DistPort())
			if !found {
				break
			}
			ch, ok := it.(chan error)
			if !ok {
				break
			}
			ch <- nil
		} else {
			// 类型一：处理客户端发送过来的探活包
			beatBuf := packet.NewBuffer(nil)
			beatBuf.SetCmd(packet.CmdAlive | packet.CmdACKFlag)
			ctx.WriteBuffer(beatBuf)
		}

	case packet.CmdPingDomain:
		if pbuf.IsACK() {
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
		pbuf.SetPayload([]byte("domain not found"))
		caller.WriteBuffer(pbuf)
		return
	}

	pbuf.SetDistIP(dist.IP)
	dist.WriteBuffer(pbuf)
}

// HandleCmdOpenStream 处理OpenStream命令，需要根据domain信息进行转发
func (s *Server) HandleCmdOpenStream(caller *Context, pbuf *packet.Buffer) {
	distDomain := string(pbuf.Payload)

	distCtx, err := s.GetContextByDomain(distDomain)
	if err != nil {
		errInfo := fmt.Sprintf("resolve failed, domain='%v'", distDomain)
		log.Println(errInfo)
		pbuf.SetOpenACK(errInfo)
		pbuf.SetSrcIP(0)
		caller.WriteBuffer(pbuf)
		return
	}

	pbuf.SetDistIP(distCtx.IP)
	pbuf.SetPayload([]byte(caller.Domain))
	distCtx.WriteBuffer(pbuf)
}
