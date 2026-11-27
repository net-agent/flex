package node

import (
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/net-agent/flex/v3/link"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// advanceFakeClock moves the current synctest bubble's fake clock forward.
// It must only be called from inside synctest.Test: it never blocks in real
// time, the bubble's clock jumps instead.
func advanceFakeClock(d time.Duration) { <-time.After(d) }

func TestSessionFrameTapLifecycle(t *testing.T) {
	connector, getServers := newTestConnector()
	s := NewSession(connector, testSessionConfig())
	s.SetLogger(slog.New(slog.DiscardHandler))

	// No node exists yet: both the nil and non-nil setters only record intent.
	s.SetFrameTap(nil)
	var first atomic.Int64
	s.SetFrameTap(func(write bool, f *wire.Frame) {
		if write {
			first.Add(1)
		}
	})

	serveErr := make(chan error, 1)
	s.ensureServing()
	go func() { serveErr <- s.Serve() }()
	t.Cleanup(func() {
		s.Close()
		select {
		case <-serveErr:
		case <-time.After(2 * time.Second):
			t.Error("Serve did not return after Close")
		}
	})

	require.NoError(t, s.WaitReady(2*time.Second))
	servers := getServers()
	require.NotEmpty(t, servers)
	_, err := servers[0].Listen(80)
	require.NoError(t, err)

	// The tap was installed on the node created after Serve started.
	_, err = s.PingDomain("", time.Second)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return first.Load() > 0 },
		2*time.Second, time.Millisecond, "cold-start tap should observe the first frames")

	oldNode := s.GetNode()
	require.NotNil(t, oldNode)

	// Replacing the tap updates the live node immediately.
	var second atomic.Int64
	s.SetFrameTap(func(write bool, f *wire.Frame) {
		if write {
			second.Add(1)
		}
	})
	_, err = s.PingDomain("", time.Second)
	require.NoError(t, err)
	require.Eventually(t, func() bool { return second.Load() > 0 },
		2*time.Second, time.Millisecond, "replacement tap should observe frames on the live node")
	firstBeforeReconnect := first.Load()

	// Force a reconnect: the tap must be re-injected into the rebuilt node.
	servers[0].Close()
	require.Eventually(t, func() bool {
		n := s.GetNode()
		return n != nil && n != oldNode
	}, 5*time.Second, 5*time.Millisecond, "session should rebuild its node after disconnect")
	require.NoError(t, s.WaitReady(2*time.Second))

	reconnectBaseline := second.Load()
	require.Eventually(t, func() bool {
		if _, err := s.PingDomain("", time.Second); err != nil {
			return false
		}
		return second.Load() > reconnectBaseline
	}, 5*time.Second, 10*time.Millisecond, "tap must be re-injected after reconnect")

	assert.Equal(t, firstBeforeReconnect, first.Load(), "replaced tap must stop observing frames")

	// nil must also detach the tap from the live node, not just future reconnects.
	s.SetFrameTap(nil)
	assert.NotNil(t, s.GetNode())
}

func TestSessionPingDomainWaitErrors(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(*Session)
		wantErr error
	}{
		{
			name:    "cold start times out",
			prepare: func(s *Session) { s.SetConnectTimeout(20 * time.Millisecond) },
			wantErr: ErrSessionConnectTimeout,
		},
		{
			name:    "closed session",
			prepare: func(s *Session) { s.Close() },
			wantErr: ErrSessionClosed,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewSession(nil, SessionConfig{})
			tc.prepare(s)
			_, err := s.PingDomain("target", time.Second)
			assert.ErrorIs(t, err, tc.wantErr)
		})
	}
}

func TestSessionWatchUnwatchErrorPassThrough(t *testing.T) {
	connector, _ := newTestConnector()
	s := NewSession(connector, testSessionConfig())
	s.SetLogger(slog.New(slog.DiscardHandler))

	serveErr := make(chan error, 1)
	s.ensureServing()
	go func() { serveErr <- s.Serve() }()
	t.Cleanup(func() {
		s.Close()
		select {
		case <-serveErr:
		case <-time.After(2 * time.Second):
			t.Error("Serve did not return after Close")
		}
	})
	require.NoError(t, s.WaitReady(2*time.Second))

	domains := manyDomains(maxWatchDomains + 1)

	_, err := s.Watch(time.Second, domains...)
	assert.ErrorIs(t, err, ErrTooManyDomains, "node-level watch error must be passed through")

	s.presenceMu.RLock()
	watched := len(s.watched)
	s.presenceMu.RUnlock()
	assert.Equal(t, len(domains), watched, "intent is recorded even when the subscribe fails")

	err = s.Unwatch(time.Second, domains...)
	assert.ErrorIs(t, err, ErrTooManyDomains, "node-level unwatch error must be passed through")

	s.presenceMu.RLock()
	assert.Empty(t, s.watched)
	s.presenceMu.RUnlock()
}

func TestSessionResyncPresenceSnapshot(t *testing.T) {
	swCh := make(chan *link.Link, 1)
	s := NewSession(newPresenceConnector(swCh), testSessionConfig())
	s.SetLogger(slog.New(slog.DiscardHandler))

	serveErr := make(chan error, 1)
	s.ensureServing()
	go func() { serveErr <- s.Serve() }()
	t.Cleanup(func() {
		s.Close()
		select {
		case <-serveErr:
		case <-time.After(2 * time.Second):
			t.Error("Serve did not return after Close")
		}
	})
	require.NoError(t, s.WaitReady(2*time.Second))

	sw := recvSwitcher(t, swCh)
	defer sw.Close()
	fsw := newFakePresenceSwitcher(t, sw, packet.PresenceState{
		Domain: "target", Online: true, IP: 7, Version: 1,
	})

	_, err := s.Watch(3*time.Second, "target")
	require.NoError(t, err)
	assert.Equal(t, uint64(1), waitSessionView(t, s, "target", 1).Version)

	var events int32
	s.SetPresenceHandler(func(packet.PresenceEvent) { atomic.AddInt32(&events, 1) })

	// The switcher truth moves to v3 without a notify event: only the resync
	// snapshot can recover it.
	fsw.mu.Lock()
	fsw.st = packet.PresenceState{Domain: "target", Online: false, IP: 7, Version: 3}
	fsw.mu.Unlock()

	// Drive a gap event at the Session view (v1 -> v3) to trigger resyncPresence.
	s.dispatchPresence(packet.PresenceEvent{Domain: "target", Version: 3, Online: false, IP: 7})

	st := waitSessionView(t, s, "target", 3)
	assert.False(t, st.Online, "snapshot from resync should replace the stale view")
	assert.Equal(t, int32(0), atomic.LoadInt32(&events), "gap event must not be delivered to the handler")
}

func TestSessionResyncPresenceFailure(t *testing.T) {
	h := &captureHandler{}
	s := NewSession(nil, SessionConfig{})
	s.SetLogger(slog.New(h))

	n := New(nil) // no link: the resync Watch fails
	t.Cleanup(func() { _ = n.Close() })
	s.mu.Lock()
	s.node = n
	s.mu.Unlock()

	s.resyncPresence("target")

	assert.True(t, h.contains("presence resync failed"))
	assert.Empty(t, s.ListPresence())
}

func TestSessionResubscribePresenceFailure(t *testing.T) {
	h := &captureHandler{}
	s := NewSession(nil, SessionConfig{})
	s.SetLogger(slog.New(h))

	s.presenceMu.Lock()
	s.watched["target"] = struct{}{}
	s.presenceMu.Unlock()

	n := New(nil) // no link: the resubscribe Watch fails
	t.Cleanup(func() { _ = n.Close() })

	s.resubscribePresence(n)

	assert.True(t, h.contains("presence resubscribe failed"))
	assert.Empty(t, s.ListPresence())
}

// The reconnect backoff starts at 1s and doubles (1s, 2s, 4s) between failed
// connection attempts.
func TestSessionServeBackoffDoubles(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var attempts []time.Duration
		start := time.Now()

		s := NewSession(func() (packet.Conn, error) {
			mu.Lock()
			attempts = append(attempts, time.Since(start))
			mu.Unlock()
			return nil, errors.New("boom")
		}, testSessionConfig())
		s.SetLogger(slog.New(slog.DiscardHandler))
		s.ensureServing()

		done := make(chan error, 1)
		go func() { done <- s.Serve() }()

		synctest.Wait()
		advanceFakeClock(8 * time.Second) // fake clock only
		synctest.Wait()

		mu.Lock()
		got := append([]time.Duration(nil), attempts...)
		mu.Unlock()

		require.GreaterOrEqual(t, len(got), 4, "expected attempts at 0s, 1s, 3s, 7s")
		want := []time.Duration{0, time.Second, 3 * time.Second, 7 * time.Second}
		for i, wantAt := range want {
			assert.InDelta(t, float64(wantAt), float64(got[i]), float64(time.Millisecond),
				"attempt %d should happen after exponential backoff", i)
		}
		assert.Equal(t, SessionConnecting, s.GetState())
		assert.Equal(t, "boom", s.GetLastErr())
		assert.Equal(t, int64(0), s.GetReconnectCount(), "failed connects before first Online are not reconnects")

		s.Close()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("Serve did not return after Close")
		}
	})
}

func TestSessionServeNilConnectorResult(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := NewSession(func() (packet.Conn, error) {
			return nil, nil
		}, testSessionConfig())
		s.SetLogger(slog.New(slog.DiscardHandler))
		s.ensureServing()

		done := make(chan error, 1)
		go func() { done <- s.Serve() }()
		synctest.Wait()

		assert.Equal(t, "connector returned nil connection", s.GetLastErr())
		assert.Equal(t, SessionConnecting, s.GetState())

		s.Close()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("Serve did not return after Close")
		}
	})
}

// A handshake failure is retried with the same exponential backoff as a
// connector failure, rather than spinning or waiting for a fixed timeout.
func TestSessionServeHandshakeFailureBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var attempts []time.Duration
		start := time.Now()

		s := NewSession(func() (packet.Conn, error) {
			mu.Lock()
			attempts = append(attempts, time.Since(start))
			mu.Unlock()
			return failingTransportConn{}, nil
		}, testSessionConfig())
		s.SetLogger(slog.New(slog.DiscardHandler))
		s.ensureServing()

		done := make(chan error, 1)
		go func() { done <- s.Serve() }()

		synctest.Wait()
		advanceFakeClock(2 * time.Second) // fake clock only
		synctest.Wait()

		mu.Lock()
		got := append([]time.Duration(nil), attempts...)
		mu.Unlock()

		require.GreaterOrEqual(t, len(got), 2)
		assert.InDelta(t, float64(time.Second), float64(got[1]-got[0]), float64(time.Millisecond),
			"first handshake retry uses the 1s backoff")
		assert.Equal(t, SessionConnecting, s.GetState())
		assert.NotEmpty(t, s.GetLastErr())

		s.Close()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("Serve did not return after Close")
		}
	})
}

// After a successful connection the backoff is reset: the first failure during
// the following reconnect waits 1s instead of continuing to double.
func TestSessionServeBackoffResetsAfterSuccessfulConnect(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var mu sync.Mutex
		var servers []*Node
		var attempts []time.Duration
		start := time.Now()
		acceptor := link.NewAcceptor(link.AcceptConfig{PSK: testPassword})

		connector := func() (packet.Conn, error) {
			mu.Lock()
			attempts = append(attempts, time.Since(start))
			attempt := len(attempts)
			mu.Unlock()
			if attempt != 2 {
				return nil, errors.New("boom")
			}
			c1, c2 := packet.Pipe()
			go func() {
				pending, hs, err := acceptor.Accept(transportFor(c2))
				if err != nil {
					c2.Close()
					return
				}
				lnk, err := pending.Respond(2)
				if err != nil {
					c2.Close()
					return
				}
				server := NewFromLink(lnk)
				server.SetIP(2)
				server.SetDomain(hs.Domain)
				go server.Serve()
				mu.Lock()
				servers = append(servers, server)
				mu.Unlock()
			}()
			return c1, nil
		}

		s := NewSession(connector, testSessionConfig())
		s.SetLogger(slog.New(slog.DiscardHandler))
		s.ensureServing()

		done := make(chan error, 1)
		go func() { done <- s.Serve() }()

		// Attempt 1 fails at t=0, attempt 2 succeeds at t=1s.
		require.NoError(t, s.WaitReady(5*time.Second))
		synctest.Wait()

		mu.Lock()
		require.Len(t, servers, 1)
		oldServer := servers[0]
		mu.Unlock()
		oldServer.Close()
		synctest.Wait()

		mu.Lock()
		require.Len(t, attempts, 3, "disconnect should immediately trigger the next attempt")
		mu.Unlock()

		// The failed attempt after the successful connection must use the reset
		// 1s backoff; a stale 2s backoff would delay attempt 4 to t=3s.
		advanceFakeClock(1200 * time.Millisecond) // fake clock only
		synctest.Wait()

		mu.Lock()
		require.GreaterOrEqual(t, len(attempts), 4)
		gap := attempts[3] - attempts[2]
		mu.Unlock()
		assert.InDelta(t, float64(time.Second), float64(gap), float64(20*time.Millisecond),
			"backoff must restart at 1s after a successful connection")
		assert.Equal(t, SessionConnecting, s.GetState())
		assert.Equal(t, "boom", s.GetLastErr())

		s.Close()
		select {
		case err := <-done:
			assert.NoError(t, err)
		case <-time.After(2 * time.Second):
			t.Fatal("Serve did not return after Close")
		}
	})
}
