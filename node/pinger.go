package node

import (
	"errors"
	"time"

	"github.com/net-agent/flex/v2/internal/idpool"
	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/internal/pending"
)

var (
	ErrPingDomainTimeout = errors.New("ping domain timeout")
)

type Pinger struct {
	host  *Node
	portm *idpool.Pool
	pending pending.Requests[struct{}]
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

	ch, err := p.pending.Register(port)
	if err != nil {
		return 0, err
	}
	defer p.pending.Remove(port)

	pingStart := time.Now()

	pbuf := packet.NewBuffer()
	pbuf.SetCmd(packet.CmdPingDomain)
	pbuf.SetSrc(p.host.GetIP(), port)
	pbuf.SetDist(packet.SwitcherIP, 0) // 忽略
	_ = pbuf.SetPayload([]byte(domain))
	if err = p.host.WriteBuffer(pbuf); err != nil {
		return 0, err
	}

	select {
	case res, ok := <-ch:
		if !ok {
			return 0, pending.ErrTimeout
		}
		if res.Err != nil {
			return 0, res.Err
		}
		return time.Since(pingStart), nil
	case <-time.After(timeout):
		return 0, pending.ErrTimeout
	}
}

// handleCmdPingDomain 处理远端的PingDomain请求
func (p *Pinger) handleCmdPingDomain(pbuf *packet.Buffer) {
	if p.ignorePing {
		p.host.logger.Warn("ignore ping request", "srcip", pbuf.SrcIP())
		return
	}
	if string(pbuf.Payload) != p.host.domain {
		_ = pbuf.SetPayload([]byte("domain not match"))
	} else {
		_ = pbuf.SetPayload(nil)
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
		p.pending.Complete(port, struct{}{}, errors.New(msg))
	} else {
		p.pending.Complete(port, struct{}{}, nil)
	}
}
