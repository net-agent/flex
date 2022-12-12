package node

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetListenerByPort(t *testing.T) {
	hub := &ListenHub{}
	port1 := uint16(100)
	port2 := uint16(200)

	hub.listeners.Store(port1, 123)
	_, err := hub.GetListenerByPort(port1)
	assert.Equal(t, err, ErrConvertListenerFailed, "cover: convert listener failed")

	l := &Listener{}
	hub.listeners.Store(port2, l)
	ret, err := hub.GetListenerByPort(port2)
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

		l.Close()
		_, err = l.Accept()
		assert.NotNil(t, err, "test: accept close listener")
	}()

	c, err := node2.Dial("1:80")
	assert.Nil(t, err, "test dial")
	c.Close()
	wg.Wait()
}