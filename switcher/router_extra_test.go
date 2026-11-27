package switcher

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/net-agent/flex/v4/packet"
	"github.com/net-agent/flex/v4/wire"
	"github.com/stretchr/testify/require"
)

// TestRouterServeDropsSpoofedSource drives serve with a spoofed source frame
// followed by a valid frame. The spoofed frame must be dropped before stats are
// recorded, and the valid frame must be forwarded (and fail lookup) normally.
func TestRouterServeDropsSpoofedSource(t *testing.T) {
	s := NewServer("", nil, nil)
	t.Cleanup(func() { _ = s.Close() })

	const callerIP = uint16(2)
	ctx, peer := newContextWithPeerLink(t, 1, "caller", callerIP)

	serveErr := make(chan error, 1)
	go func() { serveErr <- s.router.serve(ctx) }()

	spoof, err := wire.NewControl(
		wire.CmdPingDomain,
		wire.Addr{IP: callerIP + 1, Port: 1},
		wire.Addr{IP: wire.MaxIP, Port: 2},
		nil,
	)
	require.NoError(t, err)
	require.NoError(t, peer.SendFrameSync(spoof))

	payload := []byte("abc")
	forward, err := wire.NewData(
		wire.Addr{IP: callerIP, Port: 1},
		wire.Addr{IP: 200, Port: 80},
		payload,
	)
	require.NoError(t, err)
	require.NoError(t, peer.SendFrameSync(forward))
	require.NoError(t, peer.Close())

	select {
	case err := <-serveErr:
		require.Error(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("router.serve did not return after peer close")
	}

	// Only the valid frame may be accounted: source check happens before
	// recordIncoming, so the spoofed header must not contribute bytes.
	require.EqualValues(t, wire.HeaderSize+len(payload), ctx.Stats.BytesReceived)
	require.Zero(t, ctx.Stats.BytesSent)
}

// TestRouterDispatchUnknownCommandReleasesFrame verifies that frames the
// dispatcher does not handle (here an ACK open-stream frame) are released.
func TestRouterDispatchUnknownCommandReleasesFrame(t *testing.T) {
	s := NewServer("", nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	caller := newBrokenContext(t, 1, "caller", 10)

	var released atomic.Bool
	f, err := wire.NewFrameWithRelease(
		wire.ClassControl,
		wire.Cmd(wire.AckOpenStream),
		wire.Addr{IP: wire.MaxIP},
		wire.Addr{IP: caller.IP},
		nil,
		0,
		func([]byte) { released.Store(true) },
	)
	require.NoError(t, err)

	s.router.dispatch(caller, f)

	require.True(t, released.Load(), "dispatch must release unhandled frames")
	require.Zero(t, caller.Stats.BytesSent, "unhandled frame must not produce a reply")
}

// TestRouterHandlerSendFailures covers the reply / forward write-error branches
// of the switcher-local command handlers. A closed link makes SendFrame return
// an error while still recording the attempted bytes in Stats.BytesSent.
func TestRouterHandlerSendFailures(t *testing.T) {
	type testCase struct {
		name       string
		prepare    func(t *testing.T, rt *packetRouter, caller *Context) *wire.Frame
		invoke     func(rt *packetRouter, caller *Context, f *wire.Frame)
		wantCaller bool
		wantDist   bool
		check      func(t *testing.T, caller *Context)
	}

	pingFrame := func(t *testing.T, payload []byte) *wire.Frame {
		t.Helper()
		f, err := wire.NewControl(
			wire.CmdPingDomain,
			wire.Addr{IP: 10, Port: 1},
			wire.Addr{IP: wire.MaxIP, Port: 2},
			payload,
		)
		require.NoError(t, err)
		return f
	}
	openFrame := func(t *testing.T, domain string) *wire.Frame {
		t.Helper()
		req := packet.OpenStreamRequest{Domain: domain, WindowSize: 1024}
		f, err := wire.NewControl(
			wire.CmdOpenStream,
			wire.Addr{IP: 10, Port: 1},
			wire.Addr{IP: wire.MaxIP, Port: 80},
			req.Encode(),
		)
		require.NoError(t, err)
		return f
	}

	cases := []testCase{
		{
			name:       "ping empty domain reply write fails",
			prepare:    func(t *testing.T, _ *packetRouter, _ *Context) *wire.Frame { return pingFrame(t, nil) },
			invoke:     func(rt *packetRouter, caller *Context, f *wire.Frame) { rt.handlePingDomain(caller, f) },
			wantCaller: true,
		},
		{
			name:       "ping lookup failure reply write fails",
			prepare:    func(t *testing.T, _ *packetRouter, _ *Context) *wire.Frame { return pingFrame(t, []byte("missing")) },
			invoke:     func(rt *packetRouter, caller *Context, f *wire.Frame) { rt.handlePingDomain(caller, f) },
			wantCaller: true,
		},
		{
			name:     "ping forward write fails",
			prepare:  func(t *testing.T, _ *packetRouter, _ *Context) *wire.Frame { return pingFrame(t, []byte("target")) },
			invoke:   func(rt *packetRouter, caller *Context, f *wire.Frame) { rt.handlePingDomain(caller, f) },
			wantDist: true,
		},
		{
			name: "open stream quota reply write fails",
			prepare: func(t *testing.T, rt *packetRouter, caller *Context) *wire.Frame {
				rt.maxStreams.Store(1)
				caller.addStream(2)
				return openFrame(t, "target")
			},
			invoke:     func(rt *packetRouter, caller *Context, f *wire.Frame) { rt.handleOpenStream(caller, f) },
			wantCaller: true,
			check: func(t *testing.T, caller *Context) {
				require.EqualValues(t, 2, caller.streamCount(), "quota rejection must not change stream count")
			},
		},
		{
			name:       "open stream lookup failure reply write fails",
			prepare:    func(t *testing.T, _ *packetRouter, _ *Context) *wire.Frame { return openFrame(t, "missing") },
			invoke:     func(rt *packetRouter, caller *Context, f *wire.Frame) { rt.handleOpenStream(caller, f) },
			wantCaller: true,
		},
		{
			name:     "open stream forward write fails",
			prepare:  func(t *testing.T, _ *packetRouter, _ *Context) *wire.Frame { return openFrame(t, "target") },
			invoke:   func(rt *packetRouter, caller *Context, f *wire.Frame) { rt.handleOpenStream(caller, f) },
			wantDist: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := NewServer("", nil, nil)
			t.Cleanup(func() { _ = s.Close() })

			caller := newBrokenContext(t, 1, "caller", 10)
			dist := newBrokenContext(t, 2, "target", 20)
			s.registry.domainMu.Lock()
			s.registry.domainIndex[dist.Domain] = dist
			s.registry.domainMu.Unlock()
			s.registry.ipMu.Lock()
			s.registry.ipIndex[dist.IP] = dist
			s.registry.ipMu.Unlock()

			f := tc.prepare(t, s.router, caller)
			tc.invoke(s.router, caller, f)

			if tc.wantCaller {
				require.NotZero(t, caller.Stats.BytesSent, "caller should have a failed reply attempt")
			} else {
				require.Zero(t, caller.Stats.BytesSent, "caller should not receive a reply")
			}
			if tc.wantDist {
				require.NotZero(t, dist.Stats.BytesSent, "destination should have a failed forward attempt")
			} else {
				require.Zero(t, dist.Stats.BytesSent, "destination should not be contacted")
			}
			if tc.check != nil {
				tc.check(t, caller)
			}
		})
	}
}
