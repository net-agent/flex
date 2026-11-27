package switcher

import (
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/wire"
	"github.com/stretchr/testify/require"
)

func TestContextSendFrameNil(t *testing.T) {
	ctx := NewContext(1, nil, "test", "", nil)
	t.Cleanup(ctx.release)

	require.NoError(t, ctx.SendFrame(nil))
	require.Zero(t, ctx.Stats.BytesSent)
	require.Zero(t, ctx.Stats.BytesReceived)
}

// TestContextSendFrameClosedContext covers the done-before-ready arm of
// SendFrame. release() closes both channels, which makes that select
// non-deterministic, so this test closes done while ready stays open.
func TestContextSendFrameClosedContext(t *testing.T) {
	ctx := newContext(1, nil, "closed", "", nil)
	ctx.closeOnce.Do(func() { close(ctx.done) })

	var released atomic.Bool
	f, err := wire.NewFrameWithRelease(
		wire.ClassControl,
		wire.Cmd(wire.CmdPingDomain),
		wire.Addr{IP: 1},
		wire.Addr{IP: 2},
		nil,
		0,
		func([]byte) { released.Store(true) },
	)
	require.NoError(t, err)

	require.ErrorIs(t, ctx.SendFrame(f), errContextClosed)
	require.True(t, released.Load(), "closed context must release the frame")
	require.Zero(t, ctx.Stats.BytesSent)
}

func TestContextSendControl(t *testing.T) {
	ctx := NewContext(1, nil, "test", "", nil)
	t.Cleanup(ctx.release)

	// Wrong class for a control constructor must be rejected before sending.
	err := ctx.SendControl(
		wire.ControlCmd(wire.CmdPushStreamData),
		wire.Addr{IP: 1}, wire.Addr{IP: 2}, nil,
	)
	require.ErrorIs(t, err, wire.ErrClassMismatch)

	// A valid control frame on a context without a link fails at send time.
	err = ctx.SendControl(
		wire.CmdPingDomain,
		wire.Addr{IP: 1}, wire.Addr{IP: 2},
		[]byte("payload"),
	)
	require.ErrorIs(t, err, errNilContextConn)
	require.Zero(t, ctx.Stats.BytesSent, "send failure must not count bytes")
}

// TestNewContextWrapsLegacyConn covers the legacy packet.Conn adapter branch in
// NewContext and verifies a frame actually reaches the legacy writer.
func TestNewContextWrapsLegacyConn(t *testing.T) {
	pc := newTestPacketConn(nil)
	ctx := NewContext(7, pc, "legacy", "mac", nil)
	require.NotNil(t, ctx.getLink())
	select {
	case <-ctx.ready:
	default:
		t.Fatal("NewContext must mark the context ready")
	}

	f, err := wire.NewControl(
		wire.CmdPingDomain,
		wire.Addr{IP: 3, Port: 4},
		wire.Addr{IP: wire.MaxIP},
		[]byte("hi"),
	)
	require.NoError(t, err)
	require.NoError(t, ctx.SendFrame(f))

	var got testPacketWrite
	require.Eventually(t, func() bool {
		select {
		case got = <-pc.writes:
			return true
		default:
			return false
		}
	}, time.Second, time.Millisecond)

	require.Equal(t, byte(wire.CmdPingDomain), got.head[0])
	require.Equal(t, []byte("hi"), got.payload)

	ctx.release()
	require.EqualValues(t, 1, pc.closeCount.Load(), "release must close the legacy conn")
}

func TestContextAddStreamClamp(t *testing.T) {
	ctx := NewContext(1, nil, "test", "", nil)

	require.EqualValues(t, 0, ctx.addStream(-5), "stream count must clamp at zero")
	require.EqualValues(t, 1, ctx.addStream(1))
	require.EqualValues(t, 1, ctx.streamCount())
	require.EqualValues(t, 0, ctx.addStream(-1))
}

func TestContextDeliverPingResponseFullChannel(t *testing.T) {
	ctx := NewContext(1, nil, "test", "", nil)

	ch := make(chan *wire.Frame, 1)
	first, err := wire.NewControl(wire.AckPingDomain, wire.Addr{}, wire.Addr{}, []byte("first"))
	require.NoError(t, err)
	ch <- first
	ctx.pingBack.Store(uint16(7), ch)

	second, err := wire.NewControl(wire.AckPingDomain, wire.Addr{}, wire.Addr{}, []byte("second"))
	require.NoError(t, err)

	require.False(t, ctx.deliverPingResponse(7, second), "full delivery channel must reject")
	require.Len(t, ch, 1, "only the pre-filled frame should be queued")

	second.Release()
	first.Release()
}

func TestContextPingBuildFailure(t *testing.T) {
	ctx := NewContext(1, nil, "test", "", nil)
	t.Cleanup(ctx.release)
	// A domain longer than the wire payload limit makes NewControl fail before
	// any write is attempted.
	ctx.Domain = strings.Repeat("a", wire.MaxPayloadSize+1)

	dur, err := ctx.ping(time.Second)
	require.Zero(t, dur)
	require.ErrorIs(t, err, errPingWriteFailed)
	require.False(t, errors.Is(err, errPingTimeout))
}

// TestContextReadBufferSuccess covers readBuffer's legacy conversion path.
func TestContextReadBufferSuccess(t *testing.T) {
	ctx, peer := newContextWithPeerLink(t, 3, "reader", 5)

	f, err := wire.NewControl(
		wire.CmdPingDomain,
		wire.Addr{IP: 5, Port: 1},
		wire.Addr{IP: wire.MaxIP, Port: 2},
		[]byte("hello"),
	)
	require.NoError(t, err)
	require.NoError(t, peer.SendFrame(f))

	buf, err := ctx.readBuffer()
	require.NoError(t, err)
	require.NotNil(t, buf)
	defer packet.PutBuffer(buf)

	require.Equal(t, byte(wire.CmdPingDomain), buf.Cmd())
	require.Equal(t, []byte("hello"), buf.Payload)
	require.EqualValues(t, 1, buf.SrcPort())
	require.EqualValues(t, wire.MaxIP, buf.DistIP())
	require.EqualValues(t, 2, buf.DistPort())
}
