package node

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/net-agent/flex/v2/numsrc"
	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/vars"
)

var (
	ErrPingDomainTimeout = errors.New("ping domain timeout")
)

type Pinger struct {
	host       *Node
	portm      *numsrc.Manager
	ackwaiter  sync.Map // map[srcport]chan string
	ignorePing bool     // 是否相应ping请求
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

	ch := make(chan string) // for response
	p.ackwaiter.Store(port, ch)
	defer func() {
		// 此处先delete，然后再close，可以确保ack时不会panic
		p.ackwaiter.Delete(port)
		close(ch)
	}()

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

	select {
	case info := <-ch:
		if info != "" {
			return 0, fmt.Errorf("ping response: %v", info)
		}
	case <-time.After(timeout):
		return 0, ErrPingDomainTimeout
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

	pbuf.SetCmd(packet.CmdPingDomain | packet.CmdACKFlag)
	pbuf.SwapSrcDist()
	pbuf.SetSrc(p.host.GetIP(), 0)
	p.host.WriteBuffer(pbuf)
}

// HandleCmdPingDomainAck 处理远端的应答
func (p *Pinger) HandleCmdPingDomainAck(pbuf *packet.Buffer) {
	it, found := p.ackwaiter.Load(pbuf.DistPort())
	if !found {
		log.Printf("HandleCmdPingDomainAck failed, port='%v' not found\n", pbuf.DistPort())
		return
	}
	ch, ok := it.(chan string)
	if !ok {
		log.Printf("HandleCmdPingDomainAck failed, convert pbuf chan failed\n")
		return
	}

	// note1: 此处可能会panic，需要确保ch是未closed状态
	// note2: 由于在PingDomain处是先delete sync.map，然后再close chan，所以此处是安全的？？
	// note3: delete和close是非原子操作，会有问题（todo）
	ch <- string(pbuf.Payload)
}