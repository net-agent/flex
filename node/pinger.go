package node

import (
	"errors"
	"time"

	"github.com/net-agent/flex/v2/event"
	"github.com/net-agent/flex/v2/idpool"
	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/vars"
)

var (
	ErrPingDomainTimeout = errors.New("ping domain timeout")
)

type Pinger struct {
	host  *Node
	portm *idpool.Pool
	evbus event.Bus
	ignorePing bool // 为 true 时忽略所有 ping 请求
}

func (p *Pinger) init(host *Node) {
	p.host = host
	p.portm, _ = idpool.New(1, 0xffff)
}

// SetIgnorePing 设置为 true 时，不响应任何 ping 请求，做丢包处理
func (p *Pinger) SetIgnorePing(val bool) { p.ignorePing = val }

// PingDomain 对指定的节点进行连通性测试并返回RTT。domain为空时，返回到中转节点的RTT
func (p *Pinger) PingDomain(domain string, timeout time.Duration) (time.Duration, error) {
	port, err := p.portm.Allocate()
	if err != nil {
		return 0, err
	}
	defer p.portm.Release(port)

	w, err := p.evbus.ListenOnce(port)
	if err != nil {
		return 0, err
	}

	pingStart := time.Now()

	pbuf := packet.NewBuffer(nil)
	pbuf.SetCmd(packet.CmdPingDomain)
	pbuf.SetSrc(p.host.GetIP(), port)
	pbuf.SetDist(vars.SwitcherIP, 0) // 忽略
	pbuf.SetPayload([]byte(domain))
	err = p.host.WriteBuffer(pbuf)
	if err != nil {
		return 0, err
	}

	_, err = w.Wait(timeout)
	if err != nil {
		return 0, err
	}
	return time.Since(pingStart), nil
}

// handleCmdPingDomain 处理远端的PingDomain请求
func (p *Pinger) handleCmdPingDomain(pbuf *packet.Buffer) {
	if p.ignorePing {
		p.host.logger.Warn("ignore ping request", "srcip", pbuf.SrcIP())
		return
	}
	if string(pbuf.Payload) != p.host.domain {
		pbuf.SetPayload([]byte("domain not match"))
	} else {
		pbuf.SetPayload(nil)
	}

	pbuf.SetCmd(packet.AckPingDomain)
	pbuf.SwapSrcDist()
	pbuf.SetSrc(p.host.GetIP(), 0)
	if err := p.host.WriteBuffer(pbuf); err != nil {
		p.host.logger.Warn("write ping response failed", "error", err)
	}
}

// handleAckPingDomain 处理远端的应答
func (p *Pinger) handleAckPingDomain(pbuf *packet.Buffer) {
	port := pbuf.DistPort()
	msg := string(pbuf.Payload)
	if msg != "" {
		p.evbus.Dispatch(port, errors.New(msg), nil)
	} else {
		p.evbus.Dispatch(port, nil, nil)
	}
}
