package node

import (
	"testing"
	"time"

	"github.com/net-agent/flex/v3/link"
	"github.com/net-agent/flex/v3/packet"
	"github.com/stretchr/testify/assert"
)

func TestConnect(t *testing.T) {
	c1, c2 := packet.Pipe()

	go func() {
		acceptor := link.NewAcceptor(link.AcceptConfig{PSK: testPassword})
		pending, _, err := acceptor.Accept(transportFor(c2))
		if err != nil {
			c2.Close()
			return
		}
		if _, err := pending.Respond(1); err != nil {
			c2.Close()
		}
	}()

	n, err := Connect(c1, "testclient", "", testPassword)
	assert.Nil(t, err, "connect should ok")
	assert.NotNil(t, n, "node should not be nil")
	assert.Equal(t, uint16(1), n.GetIP(), "ip should be assigned by switcher")
	assert.Equal(t, "testclient", n.GetDomain(), "domain should be set")
}

func TestConnectHandshakeFailed(t *testing.T) {
	c1, c2 := packet.Pipe()

	go func() {
		// 密码不匹配时 Accept 失败，直接关闭连接
		acceptor := link.NewAcceptor(link.AcceptConfig{PSK: "wrong-password"})
		pending, _, err := acceptor.Accept(transportFor(c2))
		if err != nil {
			c2.Close()
			return
		}
		_, _ = pending.Respond(1)
	}()

	n, err := Connect(c1, "testclient", "", testPassword)
	assert.NotNil(t, err, "handshake with wrong password should fail")
	assert.Nil(t, n, "node should be nil")
}

func TestNewWithOptions(t *testing.T) {
	n, err := NewWithOptions(nil, 1000, 0xFFFF, time.Second)
	assert.Nil(t, err, "valid port range should ok")
	assert.NotNil(t, n, "valid port range should return node")

	_, err = NewWithOptions(nil, 2000, 1000, time.Second)
	assert.NotNil(t, err, "invalid port range should return error")
}
