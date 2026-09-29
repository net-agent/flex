package node

import (
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/stream"
	"github.com/stretchr/testify/assert"
)

func TestNew(t *testing.T) {
	n := New(nil)
	assert.NotNil(t, n, "new node should return no nil object")
	assert.False(t, n.dispatcher.running, "running state should false")
	assert.Nil(t, n.pconn, "conn should be nil")
}

func TestSet(t *testing.T) {
	n := New(nil)
	domain := "test1"
	ip := uint16(1234)
	n.SetDomain(domain)
	n.SetIP(ip)
	assert.Equal(t, n.GetDomain(), domain, "call SetDomain")
	assert.Equal(t, n.GetIP(), ip, "call SetIP/GetIP")

	network := "hellowork"
	n.SetNetwork(network)
	assert.Equal(t, network, n.GetNetwork())
}

func TestServe(t *testing.T) {
	n1, n2 := Pipe("test1", "test2")
	assert.NotNil(t, n1, "node1 should not be nil")
	assert.NotNil(t, n2, "node2 should not be nil")

	<-time.After(time.Millisecond * 50)

	assert.True(t, n1.dispatcher.isRunning(), "node1 running state should be true")
	assert.True(t, n2.dispatcher.isRunning(), "node2 running state should be true")
}

func TestAttachStream(t *testing.T) {
	n := New(nil)
	var err error

	sid := uint64(100)
	_, err = n.streamHub.getStream(sid)
	assert.Equal(t, err, errStreamNotFound, "test not found case")

	ctx := stream.New(nil, 0)
	err = n.streamHub.attachStream(ctx, sid)
	assert.Nil(t, err, "want nil err")

	err = n.streamHub.attachStream(ctx, sid)
	assert.Equal(t, err, ErrSidIsAttached, "sid is attached")

	ret1, err := n.streamHub.getStream(sid)
	assert.Nil(t, err, "want nil err")
	assert.Equal(t, ret1, ctx, "return value should be ctx")

	ret2, err := n.streamHub.detachStream(sid)
	assert.Nil(t, err, "want nil err")
	assert.Equal(t, ret2, ctx, "return value should be ctx")

	_, err = n.streamHub.getStream(sid)
	assert.Equal(t, err, errStreamNotFound, "getAndDelete flag should work")
}

func TestKeepalive(t *testing.T) {
	beat := time.Millisecond * 50
	c1, c2 := net.Pipe()
	pc1 := packet.NewWithConn(c1)
	pc2 := packet.NewWithConn(c2)
	n1 := New(pc1)
	n2 := New(pc2)
	n1.heartbeat.interval = beat * 5
	n2.heartbeat.interval = beat * 5

	count := 0
	n2.heartbeat.SetChecker(func() error {
		count++
		if count < 2 {
			return nil
		}
		return errors.New("failed")
	})

	var waiter sync.WaitGroup
	waiter.Add(2)
	go func() {
		n1.heartbeat.run(time.NewTicker(beat*1), n1.done, func() { n1.Close() })
		waiter.Done()
	}()
	go func() {
		n2.heartbeat.run(time.NewTicker(beat*1), n2.done, func() { n2.Close() })
		waiter.Done()
	}()

	waiter.Wait()

	n1.heartbeat.SetChecker(nil)
	n1.heartbeat.run(time.NewTicker(beat*1), n1.done, func() { n1.Close() })
}

func TestCoverWriteBuffer(t *testing.T) {
	n := New(nil)

	// 直接向为设置packet.Conn的node调用WriteBuffer，触发指定错误
	pbuf := packet.NewBufferWithCmd(packet.CmdPingDomain)
	pbuf.SetDist(100, 100)
	err := n.WriteBuffer(pbuf)
	assert.Equal(t, ErrWriterIsNil, err)

	// n.running是false，触发dispatchBuffer的错误
	n.WriteBuffer(packet.NewBuffer())
}

// 覆盖route的default分支测试
func TestCoverRoutePbufDefaultBranch(t *testing.T) {
	n := New(nil)
	n.dispatcher.start()
	go n.dispatcher.processCmdChan()
	go n.dispatcher.processDataChan()

	n.dispatcher.dispatch(packet.NewBufferWithCmd(0))
	n.dispatcher.dispatch(packet.NewBufferWithCmd(packet.CmdOpenStream))

	// give goroutines time to process
	<-time.After(time.Millisecond * 50)
	n.dispatcher.stop()
}
