package node

import (
	"log"
	"testing"
	"time"

	"github.com/net-agent/flex/v2/event"
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
		_, err = n1.Pinger.portm.Allocate()
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
	n2.SetIgnorePing(true)

	_, err := n1.PingDomain("test2", time.Millisecond*20)
	assert.Equal(t, err, event.ErrWaitReplyTimeout, "test: ping timeout")
}

func TestPingErr(t *testing.T) {
	n1, _ := Pipe("test1", "test2")

	// 提前监听：ping port 是从1开始计数
	n1.Pinger.evbus.Listen(uint16(1))
	_, err := n1.PingDomain("test2", time.Second)
	assert.Equal(t, event.ErrEventHasListened, err)
}
