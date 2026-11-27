package node

import (
	"log/slog"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/link"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNodeLinkAccessors(t *testing.T) {
	assert.Nil(t, New(nil).Link(), "node without a packet conn has no link")

	c1, c2 := packet.Pipe()
	defer c2.Close()
	n := New(c1)
	defer n.Close()
	require.NotNil(t, n.Link())
	assert.Same(t, n.lnk, n.Link())
}

func TestNodeReadLoopRequiresLink(t *testing.T) {
	err := New(nil).readLoop()
	assert.ErrorIs(t, err, ErrWriterIsNil)
}

func TestNodeServeRejectsRepeatRun(t *testing.T) {
	n := New(nil)
	require.NoError(t, n.dispatcher.start())
	t.Cleanup(n.dispatcher.stop)

	assert.ErrorIs(t, n.Serve(), ErrRepeatRun)
}

func TestNodeReadLoopReturnsDispatcherError(t *testing.T) {
	c1, c2 := packet.Pipe()
	defer c2.Close()

	n := New(c1)
	n.SetIP(1)
	n.SetDomain("stopped-node")

	serveErr := make(chan error, 1)
	go func() { serveErr <- n.Serve() }()
	require.Eventually(t, n.IsRunning, 2*time.Second, time.Millisecond)

	// Stop only the dispatcher: the read loop stays alive but can no longer
	// route frames, so it must surface the error instead of spinning.
	n.dispatcher.stop()

	pbuf := packet.NewBufferWithCmd(packet.CmdPingDomain)
	pbuf.SetSrc(2, 9)
	pbuf.SetDist(1, 0)
	require.NoError(t, c2.WriteBuffer(pbuf))
	packet.PutBuffer(pbuf)

	select {
	case err := <-serveErr:
		assert.ErrorIs(t, err, ErrNodeIsStopped)
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after the dispatcher was stopped")
	}
}

func TestNodeServeRecoversReadPanic(t *testing.T) {
	h := &captureHandler{}
	n := NewFromLink(link.NewDirect(panicTransport{}))
	n.SetIP(1)
	n.SetLogger(slog.New(h))
	t.Cleanup(func() { _ = n.Close() })

	err := n.Serve()
	require.Error(t, err)
	assert.ErrorContains(t, err, "recovered from panic")
	assert.True(t, h.contains("panic recovered in Node.Serve"))
}

func TestNodeCloseFallsBackToPacketConn(t *testing.T) {
	c1, c2 := packet.Pipe()
	defer c2.Close()

	n := &Node{pconn: c1, done: make(chan struct{})}
	require.NoError(t, n.Close())

	// The peer read must fail immediately because the packet conn was closed.
	_, err := c2.ReadBuffer()
	assert.Error(t, err)
}

func TestNodeDialDomainRoutesRemoteAndLocal(t *testing.T) {
	n1, n2 := Pipe("test1", "test2")
	defer n1.Close()
	defer n2.Close()

	remote, err := n2.Listen(80)
	require.NoError(t, err)
	defer remote.Close()

	local, err := n1.Listen(81)
	require.NoError(t, err)
	defer local.Close()

	tests := []struct {
		name   string
		domain string
		port   uint16
	}{
		{name: "remote domain", domain: "test2", port: 80},
		{name: "own domain", domain: "test1", port: 81},
		{name: "localhost alias", domain: "localhost", port: 81},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, err := n1.DialDomain(tc.domain, tc.port)
			require.NoError(t, err)
			require.NotNil(t, s)
			assert.False(t, s.GetState().IsClosed)
			require.NoError(t, s.Close())
		})
	}
}

func TestNodeListPresenceView(t *testing.T) {
	n := New(nil)
	defer n.Close()

	assert.Empty(t, n.ListPresence())

	n.watcher.view.applySnapshot([]packet.PresenceState{
		{Domain: "a", Online: true, IP: 7, Version: 2},
		{Domain: "b", Online: false, IP: 8, Version: 1},
	})

	list := n.ListPresence()
	require.Len(t, list, 2)
	byDomain := make(map[string]packet.PresenceState, len(list))
	for _, st := range list {
		byDomain[st.Domain] = st
	}
	assert.True(t, byDomain["a"].Online)
	assert.Equal(t, uint16(7), byDomain["a"].IP)
	assert.Equal(t, uint64(2), byDomain["a"].Version)
	assert.False(t, byDomain["b"].Online)

	st, ok := n.GetPresence("b")
	assert.True(t, ok)
	assert.Equal(t, uint16(8), st.IP)
}

func TestNodeWriteBufferBranches(t *testing.T) {
	t.Run("nil and unknown command", func(t *testing.T) {
		n := New(nil)
		defer n.Close()

		assert.NoError(t, n.WriteBuffer(nil))

		err := n.WriteBuffer(packet.NewBufferWithCmd(0x7E))
		require.Error(t, err)
		assert.ErrorContains(t, err, "unknown command")
	})

	t.Run("oversized payload is rejected by the frame constructor", func(t *testing.T) {
		n := New(nil)
		defer n.Close()

		pbuf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
		pbuf.SetDist(2, 80)
		pbuf.Payload = make([]byte, wire.MaxPayloadSize+1) // bypass SetPayload's guard

		assert.ErrorIs(t, n.WriteBuffer(pbuf), wire.ErrPayloadOverflow)
	})

	t.Run("ack branch preserves ack size without payload", func(t *testing.T) {
		n, frames := newTappedNode(t)

		pbuf := packet.NewBufferWithCmd(packet.AckPushStreamData)
		pbuf.SetSrc(1, 9)
		pbuf.SetDist(2, 77)
		pbuf.SetDataACKSize(42)
		require.NoError(t, n.WriteBuffer(pbuf))

		f := requireFrame(t, frames)
		assert.Equal(t, wire.ClassData, f.class)
		assert.Equal(t, wire.Cmd(wire.AckPushStreamData), f.cmd)
		assert.Equal(t, uint16(42), f.ackSize)
		assert.Empty(t, f.payload)
		assert.Equal(t, wire.Addr{IP: 2, Port: 77}, f.dst)
	})

	t.Run("data branch copies the payload", func(t *testing.T) {
		n, frames := newTappedNode(t)

		pbuf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
		pbuf.SetSrc(1, 10)
		pbuf.SetDist(2, 78)
		require.NoError(t, pbuf.SetPayload([]byte("hello")))

		require.NoError(t, n.WriteBuffer(pbuf))
		pbuf.Payload[0] = 'X' // WriteBuffer copies; mutation must not leak into the wire frame

		f := requireFrame(t, frames)
		assert.Equal(t, wire.ClassData, f.class)
		assert.Equal(t, wire.Cmd(wire.CmdPushStreamData), f.cmd)
		assert.Equal(t, []byte("hello"), f.payload)
		assert.Equal(t, wire.Addr{IP: 2, Port: 78}, f.dst)
	})
}

func TestNodeSendFrameBranches(t *testing.T) {
	t.Run("nil frame", func(t *testing.T) {
		assert.NoError(t, New(nil).SendFrame(nil))
	})

	t.Run("invalid frame is rejected", func(t *testing.T) {
		assert.ErrorIs(t, New(nil).SendFrame(&wire.Frame{}), wire.ErrInvalidClass)
	})

	t.Run("valid frame is copied and counted", func(t *testing.T) {
		n, frames := newTappedNode(t)
		payload := []byte("copy-me")
		f, err := wire.NewData(wire.Addr{IP: 1, Port: 9}, wire.Addr{IP: 2, Port: 77}, payload)
		require.NoError(t, err)

		require.NoError(t, n.SendFrame(f))
		payload[0] = 'X' // caller may reuse the buffer immediately after SendFrame

		got := requireFrame(t, frames)
		assert.Equal(t, []byte("copy-me"), got.payload)

		read, written := n.GetReadWriteSize()
		assert.Equal(t, int64(0), read)
		assert.Equal(t, int64(len("copy-me")), written, "sent payload bytes should be counted")
	})
}
