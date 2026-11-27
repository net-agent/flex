package switcher

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewServerWithLogConfig(t *testing.T) {
	base := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cfg := &LogConfig{
		Server:   slog.LevelDebug,
		Registry: slog.LevelError,
		Router:   slog.LevelWarn,
		Context:  slog.LevelError,
		Presence: slog.LevelWarn,
	}
	s := NewServer("pw", base, cfg)
	t.Cleanup(func() { _ = s.Close() })

	require.NotNil(t, s.logger)
	require.True(t, s.logger.Enabled(context.Background(), slog.LevelDebug),
		"server logger should honor the configured debug level")
	require.False(t, s.ctxLogger.Enabled(context.Background(), slog.LevelInfo),
		"context logger should honor the configured error level")
	require.NotNil(t, s.router)
	require.NotNil(t, s.registry)
	require.NotNil(t, s.presence)
}

func TestServerLimitsZeroValueDisables(t *testing.T) {
	s := NewServer("", nil, nil)
	t.Cleanup(func() { _ = s.Close() })

	s.SetLimits(Limits{})

	require.Nil(t, s.connSem, "zero MaxConnections must disable the semaphore")
	require.Nil(t, s.hsLimiter, "zero handshake rate must disable the limiter")
	require.Zero(t, s.router.maxStreams.Load())
	require.Zero(t, s.presence.maxSubscriptions.Load())

	require.True(t, s.acquireConnSlot(), "unlimited server always admits")
	require.NotPanics(t, s.releaseConnSlot, "unlimited release must be a no-op")
}

func TestLinkTransportFallsBackToLegacyAdapter(t *testing.T) {
	local := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1234}
	raw := &testRawConn{local: local}
	pc := newTestPacketConn(raw)

	tr := linkTransport(pc)

	require.NotNil(t, tr)
	require.Equal(t, local, tr.LocalAddr(), "adapter must delegate address lookups")
}

func TestServerServeReturnsAcceptError(t *testing.T) {
	s := NewServer("", nil, nil)
	t.Cleanup(func() { _ = s.Close() })

	acceptErr := errors.New("forced accept error")
	l := &testAcceptErrListener{err: acceptErr}

	err := s.Serve(l)
	require.ErrorIs(t, err, acceptErr)

	require.NoError(t, s.Close())
	require.EqualValues(t, 1, l.closeCount.Load(), "Close must close the serving listener")
}

func TestServeConnRecoversFromPanic(t *testing.T) {
	s := NewServer("", nil, nil)
	t.Cleanup(func() { _ = s.Close() })

	pc := &panicGetRawConn{testPacketConn: newTestPacketConn(nil)}

	err := s.ServeConn(pc)
	require.ErrorContains(t, err, "recovered from panic")
	require.ErrorContains(t, err, "forced panic in GetRawConn")
	require.EqualValues(t, 1, pc.closeCount.Load(), "panicking conn must still be closed")
}

func TestServeConnHandshakeRateLimited(t *testing.T) {
	s := NewServer("", nil, nil)
	t.Cleanup(func() { _ = s.Close() })
	s.SetLimits(Limits{
		HandshakeRatePerIP:  1,
		HandshakeBurstPerIP: 1,
	})

	// Exhaust the only token for this IP, then ensure ServeConn rejects before
	// touching the connection (no handshake read).
	require.True(t, s.hsLimiter.allow("127.0.0.1"))
	raw := &testRawConn{remote: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40000}}
	pc := newTestPacketConn(raw)

	err := s.ServeConn(pc)
	require.ErrorIs(t, err, errRateLimited)
	require.Zero(t, pc.readCount.Load(), "rate-limited conn must not be read")
	require.EqualValues(t, 1, pc.closeCount.Load())
}

func TestIPLimiterBurstFloor(t *testing.T) {
	l := newIPLimiter(1, 0)
	require.Equal(t, 1.0, l.burst, "non-positive burst must be clamped to one token")

	require.True(t, l.allow("10.0.0.1"))
	require.False(t, l.allow("10.0.0.1"))
}

func TestIPLimiterTokenSaturation(t *testing.T) {
	l := newIPLimiter(1, 2)
	now := time.Unix(1000, 0)
	l.now = func() time.Time { return now }
	l.lastSweep = now
	l.mu.Lock()
	l.buckets["10.0.0.9"] = &ipBucket{tokens: 0, lastSeen: now.Add(-time.Hour)}
	l.mu.Unlock()

	require.True(t, l.allow("10.0.0.9"))

	l.mu.Lock()
	tokens := l.buckets["10.0.0.9"].tokens
	l.mu.Unlock()
	require.Equal(t, 1.0, tokens, "refill must clamp to burst, then spend one token")

	require.True(t, l.allow("10.0.0.9"))
	require.False(t, l.allow("10.0.0.9"), "bucket must be empty after burst")
}

func TestIPLimiterBucketTableFull(t *testing.T) {
	l := newIPLimiter(1, 1)
	now := time.Unix(1000, 0)
	l.now = func() time.Time { return now }
	l.lastSweep = now

	l.mu.Lock()
	for i := 0; i < ipLimiterMaxEntries; i++ {
		l.buckets[fmt.Sprintf("10.0.%d.%d", i/256, i%256)] = &ipBucket{tokens: 0, lastSeen: now}
	}
	l.mu.Unlock()

	require.False(t, l.allow("192.0.2.1"), "unknown IP must fail closed when the table is full")

	l.mu.Lock()
	size := len(l.buckets)
	l.mu.Unlock()
	require.Equal(t, ipLimiterMaxEntries, size, "table must not grow past its cap")
}

func TestRemoteIP(t *testing.T) {
	cases := []struct {
		name string
		conn net.Conn
		want string
	}{
		{name: "nil conn", conn: nil, want: ""},
		{name: "nil remote addr", conn: &testRawConn{}, want: ""},
		{name: "non host-port addr", conn: &testRawConn{remote: testTextAddr("pipe")}, want: ""},
		{
			name: "tcp addr",
			conn: &testRawConn{remote: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 40000}},
			want: "127.0.0.1",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, remoteIP(tc.conn))
		})
	}
}
