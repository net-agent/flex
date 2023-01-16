package node

import (
	"errors"
	"log"
	"time"

	"github.com/net-agent/flex/v2/event"
	"github.com/net-agent/flex/v2/numsrc"
	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/vars"
)

var (
	ErrPingDomainTimeout = errors.New("ping domain timeout")
)

type Pinger struct {
	host  *Node
	portm *numsrc.Manager
	evbus event.Bus
	// ackwaiter  sync.Map // map[srcport]chan string
	ignorePing bool // 是否相应ping请求
}

func (p *Pinger) Init(host *Node) {
	p.host = host
	p.portm, _ = numsrc.NewManager(1, 1, 0xffff)
}

// SetPingHandleFlag 设置未false时，不响应任何ping请求，做丢包处理
func (p *Pinger) SetIgnorePing(val bool) { p.ignorePing = val }

// PingDomain 对指定的节点进行连通性测试并返回RTT。domain为空时，返回到中转节点的RTT
func (p *Pinger) PingDomain(domain string, timeout time.Duration) (time.Duration, error) {
	port, err := p.portm.GetFreeNumberSrc()
	if err != nil {
		return 0, err
	}
	defer p.portm.ReleaseNumberSrc(port)

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

// HandleCmdPingDomain 处理远端的PingDomain请求
func (p *Pinger) HandleCmdPingDomain(pbuf *packet.Buffer) {
	if p.ignorePing {
		log.Printf("ignored the ping request from '%v'\n", pbuf.SrcIP())
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
	p.host.WriteBuffer(pbuf)
}

// HandleAckPingDomain 处理远端的应答
func (p *Pinger) HandleAckPingDomain(pbuf *packet.Buffer) {
	port := pbuf.DistPort()
	msg := string(pbuf.Payload)
	if msg != "" {
		p.evbus.Dispatch(port, errors.New(msg), nil)
	} else {
		p.evbus.Dispatch(port, nil, nil)
	}
}
