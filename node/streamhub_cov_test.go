package node

import (
	"log/slog"
	"testing"
	"time"

	"github.com/net-agent/flex/v4/packet"
	"github.com/net-agent/flex/v4/stream"
	"github.com/net-agent/flex/v4/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamHubInvalidEntries(t *testing.T) {
	hub := &StreamHub{}
	hub.streams.Store(uint64(1), "not-a-stream")

	_, err := hub.getStream(1)
	assert.ErrorIs(t, err, errInvalidStreamType)

	_, err = hub.detachStream(2)
	assert.ErrorIs(t, err, errStreamNotFound)

	_, err = hub.detachStream(1)
	assert.ErrorIs(t, err, errInvalidStreamType)
	_, loaded := hub.streams.Load(uint64(1))
	assert.False(t, loaded, "detachStream must remove the invalid entry")

	hub.streams.Store(uint64(3), 42)
	assert.False(t, hub.closeStreamByIndex(0), "non-stream entries must be ignored")
}

func TestStreamHubMissingStreamConsumesFrames(t *testing.T) {
	src, dst := wire.Addr{IP: 2, Port: 3}, wire.Addr{IP: 1, Port: 4}
	makeFrame := func(t *testing.T, class wire.Class, cmd wire.Cmd, payload []byte, ack uint16, released *bool) *wire.Frame {
		t.Helper()
		f, err := wire.NewFrameWithRelease(class, cmd, src, dst, payload, ack, func([]byte) {
			*released = true
		})
		require.NoError(t, err)
		return f
	}

	tests := []struct {
		name   string
		host   bool
		frame  func(t *testing.T, released *bool) *wire.Frame
		handle func(*StreamHub, *wire.Frame) error
	}{
		{
			name: "data without host",
			frame: func(t *testing.T, released *bool) *wire.Frame {
				return makeFrame(t, wire.ClassData, wire.Cmd(wire.CmdPushStreamData), []byte("x"), 0, released)
			},
			handle: func(h *StreamHub, f *wire.Frame) error { return h.handleData(f) },
		},
		{
			name: "data with host",
			host: true,
			frame: func(t *testing.T, released *bool) *wire.Frame {
				return makeFrame(t, wire.ClassData, wire.Cmd(wire.CmdPushStreamData), []byte("x"), 0, released)
			},
			handle: func(h *StreamHub, f *wire.Frame) error { return h.handleData(f) },
		},
		{
			name: "ack data with host",
			host: true,
			frame: func(t *testing.T, released *bool) *wire.Frame {
				return makeFrame(t, wire.ClassData, wire.Cmd(wire.AckPushStreamData), nil, 9, released)
			},
			handle: func(h *StreamHub, f *wire.Frame) error { return h.handleAckData(f) },
		},
		{
			name: "close ack without host",
			frame: func(t *testing.T, released *bool) *wire.Frame {
				return makeFrame(t, wire.ClassControl, wire.Cmd(wire.AckCloseStream), nil, 0, released)
			},
			handle: func(h *StreamHub, f *wire.Frame) error { return h.handleCloseAck(f) },
		},
		{
			name: "close with host still replies then releases",
			host: true,
			frame: func(t *testing.T, released *bool) *wire.Frame {
				return makeFrame(t, wire.ClassControl, wire.Cmd(wire.CmdCloseStream), nil, 0, released)
			},
			handle: func(h *StreamHub, f *wire.Frame) error { return h.handleClose(f) },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hub := &StreamHub{}
			if tc.host {
				n := New(nil) // no link: the AckClose write is dropped, frame still released
				n.SetLogger(slog.New(slog.DiscardHandler))
				hub.host = n
				t.Cleanup(func() { _ = n.Close() })
			}
			var released bool
			f := tc.frame(t, &released)
			require.NoError(t, tc.handle(hub, f))
			assert.True(t, released, "missing-stream path must release the frame")
		})
	}
}

// Regression: a Close for a stream that already detached must still be
// acknowledged, otherwise the peer waits for its full close-ack timeout.
func TestStreamHubLateCloseAfterDetachRepliesAckClose(t *testing.T) {
	c1, c2 := packet.Pipe()
	n := New(c1)
	n.SetIP(1)
	n.SetDomain("late-close")
	n.SetDetachRetention(0) // detach removes the stream immediately
	defer n.Close()
	defer c2.Close()

	port, err := n.streamHub.portm.Allocate()
	require.NoError(t, err)
	s := stream.NewDialStreamWithEndpoint(n, "late-close", 1, port, "peer", 2, 100, 0)
	s.SetBoundPort(port)
	sid := wire.SID{Src: wire.Addr{IP: 1, Port: port}, Dst: wire.Addr{IP: 2, Port: 100}}.Uint64()
	require.NoError(t, n.streamHub.attachStream(s, sid))

	// Fully close locally: the detach callback removes the stream from the hub.
	require.NoError(t, s.CloseRead())
	require.NoError(t, s.CloseWrite())
	_, err = n.streamHub.getStream(sid)
	assert.ErrorIs(t, err, errStreamNotFound, "stream must be detached before the late Close arrives")

	// The peer's Close arrives after detach and must still be acknowledged.
	f, err := wire.NewEmpty(wire.CmdCloseStream,
		wire.Addr{IP: 2, Port: 100}, wire.Addr{IP: 1, Port: port})
	require.NoError(t, err)
	require.NoError(t, n.streamHub.handleClose(f))

	require.NoError(t, c2.SetReadTimeout(2*time.Second))
	ack, err := c2.ReadBuffer()
	require.NoError(t, err)
	defer packet.PutBuffer(ack)

	assert.Equal(t, packet.AckCloseStream, ack.Cmd())
	assert.Equal(t, uint16(1), ack.SrcIP(), "ack source must be the original destination")
	assert.Equal(t, port, ack.SrcPort())
	assert.Equal(t, uint16(2), ack.DistIP(), "ack destination must be the original source")
	assert.Equal(t, uint16(100), ack.DistPort())
	assert.Empty(t, ack.Payload)
}

func TestStreamHubLegacyCloseForMissingStreamRepliesAck(t *testing.T) {
	n, frames := newTappedNode(t)

	pbuf := packet.NewBufferWithCmd(packet.CmdCloseStream)
	pbuf.SetSrc(2, 100)
	pbuf.SetDist(1, 200)
	n.streamHub.handleCmdCloseStream(pbuf)
	packet.PutBuffer(pbuf)

	ack := requireFrame(t, frames)
	assert.Equal(t, wire.Cmd(wire.AckCloseStream), ack.cmd)
	assert.Equal(t, wire.Addr{IP: 1, Port: 200}, ack.src)
	assert.Equal(t, wire.Addr{IP: 2, Port: 100}, ack.dst)
}

func TestStreamHubLegacyHandlersWithLiveStream(t *testing.T) {
	hub := &StreamHub{}
	hub.init(nil, nil)

	s := stream.New(nil, 0)
	key := packet.NewBufferWithCmd(packet.CmdPushStreamData)
	key.SetSrc(2, 100)
	key.SetDist(1, 200)
	sid := key.SID()
	hub.streams.Store(sid, s)

	// data
	data := packet.NewBufferWithCmd(packet.CmdPushStreamData)
	data.SetSrc(2, 100)
	data.SetDist(1, 200)
	require.NoError(t, data.SetPayload([]byte("hello")))
	hub.handleCmdPushStreamData(data) // consumes the buffer
	st := s.GetState()
	assert.Equal(t, int32(1), st.RecvBufferCount)
	assert.Equal(t, int64(5), st.RecvDataSize)

	// data ack
	ack := packet.NewBufferWithCmd(packet.AckPushStreamData)
	ack.SetSrc(2, 100)
	ack.SetDist(1, 200)
	ack.SetDataACKSize(7)
	hub.handleAckPushStreamData(ack)
	packet.PutBuffer(ack)
	st = s.GetState()
	assert.Equal(t, int32(2), st.RecvBufferCount)
	assert.Equal(t, int64(7), st.RecvAckTotal)

	// close
	closing := packet.NewBufferWithCmd(packet.CmdCloseStream)
	closing.SetSrc(2, 100)
	closing.SetDist(1, 200)
	hub.handleCmdCloseStream(closing)
	packet.PutBuffer(closing)
	st = s.GetState()
	assert.Equal(t, int32(3), st.RecvBufferCount)
	assert.True(t, st.IsClosed)
	assert.Equal(t, "remote", st.CloseReason)

	// close ack
	closingAck := packet.NewBufferWithCmd(packet.AckCloseStream)
	closingAck.SetSrc(2, 100)
	closingAck.SetDist(1, 200)
	hub.handleAckCloseStream(closingAck)
	packet.PutBuffer(closingAck)
	assert.Equal(t, int32(4), s.GetState().RecvBufferCount)
}

func TestStreamHubLegacyPushFromMissingStreamLogs(t *testing.T) {
	h := &captureHandler{}
	n := New(nil)
	n.SetLogger(slog.New(h))
	defer n.Close()

	pbuf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
	pbuf.SetSrc(2, 100)
	pbuf.SetDist(1, 200)
	n.streamHub.handleCmdPushStreamData(pbuf) // consumes pbuf

	ack := packet.NewBufferWithCmd(packet.AckPushStreamData)
	ack.SetSrc(2, 100)
	ack.SetDist(1, 200)
	n.streamHub.handleAckPushStreamData(ack) // legacy ownership: caller puts the buffer
	packet.PutBuffer(ack)

	assert.True(t, h.contains("push data: stream not found, possibly closed"))
	assert.True(t, h.contains("push data ack: stream not found, possibly closed"))
	assert.Empty(t, n.streamHub.GetStreamStates())
}
