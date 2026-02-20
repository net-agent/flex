package node

import (
	"sync"
	"testing"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/stream"
	"github.com/net-agent/flex/v2/vars"
	"github.com/stretchr/testify/assert"
)

func TestGetListenerByPort(t *testing.T) {
	hub := &ListenHub{}
	port1 := uint16(100)
	port2 := uint16(200)

	hub.listeners.Store(port1, 123)
	_, err := hub.getListenerByPort(port1)
	assert.Equal(t, err, ErrConvertListenerFailed, "cover: convert listener failed")

	l := &Listener{}
	hub.listeners.Store(port2, l)
	ret, err := hub.getListenerByPort(port2)
	assert.Nil(t, err)
	assert.Equal(t, ret, l)
}

func TestNodeListen(t *testing.T) {
	node1, node2 := Pipe("test1", "test2")

	l, err := node1.Listen(80)
	assert.Nil(t, err, "listen should be ok")
	assert.NotNil(t, l, "listener should not be nil")
	assert.Equal(t, l.Addr().Network(), "flex", "test Addr().Network()")
	assert.Equal(t, l.Addr().String(), "1:80", "test Addr().String()")

	_, err = node1.Listen(80)
	assert.Equal(t, err, ErrListenPortIsUsed, "listen on one port twice")

	var wg sync.WaitGroup

	// accept test
	wg.Add(1)
	go func() {
		defer wg.Done()
		// 第一个连接为正常连接
		// 第二个为关闭错误

		_, err := l.Accept()
		assert.Nil(t, err, "accept should be ok")

		err = l.Close()
		assert.Nil(t, err)
		err = l.Close()
		assert.Equal(t, ErrListenerClosed, err)

		_, err = l.Accept()
		assert.NotNil(t, err, "test: accept close listener")
	}()

	c, err := node2.Dial("1:80")
	assert.Nil(t, err, "test dial")
	c.Close()
	wg.Wait()
}

func TestHandleCmdOpenErr(t *testing.T) {
	n := New(nil)
	pbuf := packet.NewBuffer(nil)
	pbuf.SetSrc(100, 100)
	n.handleCmdOpenStream(pbuf)

	// 覆盖测试：SID已经存在的情况
	n1, n2 := Pipe("test1", "test2")
	l, err := n2.Listen(80)
	assert.Nil(t, err)
	assert.NotNil(t, l)
	go func() {
		for {
			_, err := l.Accept()
			if err != nil {
				return
			}
		}
	}()

	pbuf.SetCmd(packet.CmdOpenStream)
	pbuf.SetDist(vars.SwitcherIP, 80)
	pbuf.SetSrc(1, 1000)
	pbuf.SetPayload([]byte("test2"))

	s, err := n1.dialPbuf(pbuf)
	assert.Nil(t, err)
	assert.NotNil(t, s)

	//用重复的pbuf去创建连接，触发attachStream错误
	n1.WriteBuffer(pbuf)
}

func TestAcceptErr(t *testing.T) {
	l := &Listener{}

	l.streams = make(chan *stream.Stream, 10)
	l.streams <- nil
	_, err := l.Accept()
	assert.Equal(t, ErrListenerClosed, err)
}
