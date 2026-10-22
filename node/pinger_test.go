package node

import (
	"log"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/internal/pending"
	"github.com/net-agent/flex/v3/packet"
	"github.com/stretchr/testify/assert"
)

func TestPingDomain(t *testing.T) {
	node1, _ := Pipe("test1", "test2")
	var err error

	_, err = node1.PingDomain("test2", time.Second)
	assert.Nil(t, err, "test PingDomain")

	_, err = node1.PingDomain("notexists", time.Second)
	assert.NotNil(t, err, "test PingDomain error")
	log.Println(err)
}

func TestPingDomainErr_GetFreeNum(t *testing.T) {
	n1, _ := Pipe("test1", "test2")
	var err error

	// 耗尽portm的资源
	for {
		_, err = n1.pinger.portm.Allocate()
		if err != nil {
			break
		}
	}

	_, err = n1.PingDomain("test2", time.Second)
	assert.NotNil(t, err, "test pingDomain error ")
}

func TestPingDomainErr_Write(t *testing.T) {
	n1, n2 := Pipe("test1", "test2")
	n2.Close()

	_, err := n1.PingDomain("test2", time.Second)
	assert.NotNil(t, err, "test ping writebuffer error")
}

func TestPingDomainErr_timeout(t *testing.T) {
	n1, n2 := Pipe("test1", "test2")
	n2.pinger.SetIgnorePing(true)

	_, err := n1.PingDomain("test2", time.Millisecond*20)
	assert.Equal(t, ErrPingDomainTimeout, err, "test: ping timeout")
}

func TestPingErr(t *testing.T) {
	n1, _ := Pipe("test1", "test2")

	// 提前监听：ping port 是从1开始计数
	n1.pinger.pending.Register(uint16(1))
	_, err := n1.PingDomain("test2", time.Second)
	assert.Equal(t, pending.ErrAlreadyRegistered, err)
}

// 空域名 ping 是通用存活探测，任何节点都必须应答 OK。
// 直连模式的心跳依赖这一分支，旧实现会返回 "domain not match" 导致误判断链。
func TestPingDomain_EmptyDomainLiveness(t *testing.T) {
	n1, _ := Pipe("test1", "test2")
	defer n1.Close()

	rtt, err := n1.PingDomain("", time.Second)
	assert.NoError(t, err, "empty-domain ping should be answered by any peer")
	assert.Greater(t, rtt, time.Duration(0))
}

// 回归：README 的直连模式（无 switcher）在空闲一个心跳周期后会被误关闭。
func TestDirectModeHeartbeatKeepsNodeAlive(t *testing.T) {
	old := DefaultHeartbeatInterval
	DefaultHeartbeatInterval = 50 * time.Millisecond
	defer func() { DefaultHeartbeatInterval = old }()

	c1, c2 := packet.Pipe()
	n1 := New(c1)
	n1.SetDomain("direct-a")
	n1.SetIP(1)
	n2 := New(c2)
	n2.SetDomain("direct-b")
	n2.SetIP(2)
	defer n1.Close()
	defer n2.Close()

	go n1.Serve()
	go n2.Serve()

	// 跨越 10 个心跳周期，期间没有任何业务流量
	time.Sleep(500 * time.Millisecond)

	assert.True(t, n1.IsRunning(), "direct-mode node should stay alive across heartbeat probes")
	assert.True(t, n2.IsRunning(), "direct-mode peer should stay alive across heartbeat probes")
	assert.Greater(t, n1.GetLastRTT(), time.Duration(0), "heartbeat should record RTT")
}
