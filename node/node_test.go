package node

import (
	"testing"
	"time"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/stream"
	"github.com/stretchr/testify/assert"
)

func TestNew(t *testing.T) {
	n := New(nil)
	assert.NotNil(t, n, "new node should return no nil object")
	assert.False(t, n.running, "running state should false")
	assert.Nil(t, n.Conn, "conn should be nil")
}

func TestSet(t *testing.T) {
	n := New(nil)
	domain := "test1"
	ip := uint16(1234)
	n.SetDomain(domain)
	n.SetIP(ip)
	assert.Equal(t, n.GetDomain(), domain, "call SetDomain")
	assert.Equal(t, n.GetIP(), ip, "call SetIP/GetIP")
}

func TestRun(t *testing.T) {
	n1, n2 := Pipe("test1", "test2")
	assert.NotNil(t, n1, "node1 should not be nil")
	assert.NotNil(t, n2, "node2 should not be nil")

	go n1.Run()
	go n2.Run()
	<-time.After(time.Millisecond * 50)

	assert.True(t, n1.running, "node1 running state should be true")
	assert.True(t, n2.running, "node2 running state should be true")
}

func TestAttachStream(t *testing.T) {
	n := New(nil)
	var err error

	sid := uint64(100)
	_, err = n.GetStreamBySID(sid, false)
	assert.Equal(t, err, errStreamNotFound, "test not found case")

	ctx := stream.New(nil, true)
	err = n.AttachStream(ctx, sid)
	assert.Nil(t, err, "want nil err")

	err = n.AttachStream(ctx, sid)
	assert.Equal(t, err, ErrSidIsAttached, "sid is attached")

	ret1, err := n.GetStreamBySID(sid, false)
	assert.Nil(t, err, "want nil err")
	assert.Equal(t, ret1, ctx, "return value should be ctx")

	ret2, err := n.GetStreamBySID(sid, true) // get and delete
	assert.Nil(t, err, "want nil err")
	assert.Equal(t, ret2, ctx, "return value should be ctx")

	_, err = n.GetStreamBySID(sid, false)
	assert.Equal(t, err, errStreamNotFound, "getAndDelete flag should work")
}

func TestPbufRouteLoop(t *testing.T) {
	ch := make(chan *packet.Buffer, 10)
	defer close(ch)

	n := New(nil)
	go n.pbufRouteLoop(ch)

	cmds := []byte{
		packet.CmdOpenStream,
		packet.CmdPushStreamData,
		packet.CmdCloseStream,
		packet.CmdPingDomain,
	}

	for _, cmd := range cmds {
		pbuf1 := packet.NewBuffer(nil)
		pbuf1.SetCmd(cmd)
		ch <- pbuf1

		pbuf2 := packet.NewBuffer(nil)
		pbuf2.SetCmd(cmd | packet.CmdACKFlag)
		ch <- pbuf2
	}
}

// func TestKeepalive(t *testing.T) {
// 	oldValue := DefaultHeartbeatInterval
// 	DefaultHeartbeatInterval = time.Millisecond * 400 // github上设置为100ms可能会失败
// 	defer func() {
// 		DefaultHeartbeatInterval = oldValue
// 	}()

// 	n1, n2 := Pipe("test1", "")

// 	// 等待几个间隔，触发keepalive的定时ping
// 	// 因为默认定时ping的对象是domain=""，所以n1的keepalive会失败，然后关闭连接
// 	// 有一方关闭连接后，后面的pingDomain都会应该返回失败
// 	<-time.After(DefaultHeartbeatInterval * 3)

// 	_, err := n1.PingDomain("", time.Second)
// 	assert.NotNil(t, err)
// 	_, err = n2.PingDomain("test1", time.Second)
// 	assert.NotNil(t, err)
// }

func TestCoverReadBufUntilFailed(t *testing.T) {
	n1, n2 := Pipe("test1", "test2")
	go func() {
		n2.WriteBuffer(packet.NewBuffer(nil))
	}()

	ch := make(chan *packet.Buffer)
	oldValue := DefaultWriteLocalTimeout
	DefaultWriteLocalTimeout = time.Millisecond * 200
	defer func() {
		DefaultWriteLocalTimeout = oldValue
	}()

	go n1.readBufferUntilFailed(ch)

	<-time.After(DefaultWriteLocalTimeout * 2)
}
