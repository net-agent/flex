package node

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v4/internal/idpool"
	"github.com/net-agent/flex/v4/internal/pending"
	"github.com/net-agent/flex/v4/packet"
	"github.com/net-agent/flex/v4/wire"
)

var (
	ErrPingDomainTimeout = errors.New("ping domain timeout")
)

type Pinger struct {
	host       *Node
	portm      *idpool.Pool
	pending    pending.Requests[struct{}]
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
		p.host.tracePortExhausted("pinger", err) // pinger 使用独立的端口池
		return 0, err
	}
	defer p.portm.Release(port)

	ch, err := p.pending.Register(port)
	if err != nil {
		return 0, err
	}
	defer p.pending.Remove(port)

	pingStart := time.Now()

	f, err := wire.NewControl(
		wire.CmdPingDomain,
		wire.Addr{IP: p.host.GetIP(), Port: port},
		wire.SwitcherAddr,
		[]byte(domain),
	)
	if err != nil {
		return 0, err
	}
	if err = p.host.SendFrame(f); err != nil {
		atomic.AddInt64(&p.host.failures.PingFailed, 1)
		return 0, err
	}

	select {
	case res, ok := <-ch:
		if !ok {
			atomic.AddInt64(&p.host.failures.PingFailed, 1)
			return 0, ErrPingDomainTimeout
		}
		if res.Err != nil {
			atomic.AddInt64(&p.host.failures.PingFailed, 1)
			return 0, res.Err
		}
		return time.Since(pingStart), nil
	case <-time.After(timeout):
		atomic.AddInt64(&p.host.failures.PingFailed, 1)
		return 0, ErrPingDomainTimeout
	}
}

// handlePingFrame handles a PingDomain request. It is a control-lane handler:
// the dispatcher releases f after it returns.
func (p *Pinger) handlePingFrame(f *wire.Frame) error {
	if p.ignorePing {
		p.host.logger.Warn("ignore ping request", "srcip", f.Src().IP)
		return nil
	}
	probeDomain := string(f.Payload())
	var payload []byte
	if probeDomain != "" && probeDomain != p.host.domain {
		payload = []byte("domain not match")
	}
	reply, err := wire.NewControl(
		wire.AckPingDomain,
		wire.Addr{IP: p.host.GetIP()},
		f.Src(),
		payload,
	)
	if err != nil {
		return err
	}
	if err := p.host.SendFrame(reply); err != nil {
		p.host.logger.Warn("write ping response failed", "error", err)
	}
	return nil
}

// handleAckFrame handles a PingDomain response. It is a control-lane handler.
func (p *Pinger) handleAckFrame(f *wire.Frame) error {
	port := f.Dst().Port
	msg := string(f.Payload())
	if msg != "" {
		_ = p.pending.Complete(port, struct{}{}, errors.New(msg))
	} else {
		_ = p.pending.Complete(port, struct{}{}, nil)
	}
	return nil
}

// handleCmdPingDomain is the legacy packet.Buffer adapter.
func (p *Pinger) handleCmdPingDomain(pbuf *packet.Buffer) {
	f, err := bufferToFrame(pbuf)
	if err != nil {
		return
	}
	_ = p.handlePingFrame(f)
	f.Release()
}

// handleAckPingDomain is the legacy packet.Buffer adapter.
func (p *Pinger) handleAckPingDomain(pbuf *packet.Buffer) {
	f, err := bufferToFrame(pbuf)
	if err != nil {
		return
	}
	_ = p.handleAckFrame(f)
	f.Release()
}
