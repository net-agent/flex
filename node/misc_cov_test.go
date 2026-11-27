package node

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/net-agent/flex/v4/internal/sched"
	"github.com/net-agent/flex/v4/packet"
	"github.com/net-agent/flex/v4/stream"
	"github.com/net-agent/flex/v4/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectRejectsNilConn(t *testing.T) {
	n, err := Connect(nil, "domain", "", "password")
	assert.ErrorIs(t, err, ErrWriterIsNil)
	assert.Nil(t, n)
}

func TestDialDomainOversizedOpenRequest(t *testing.T) {
	n := New(nil)
	defer n.Close()

	_, err := n.DialDomain(strings.Repeat("d", wire.MaxPayloadSize+1), 80)
	require.Error(t, err)
	assert.ErrorIs(t, err, wire.ErrPayloadOverflow)
	assert.Equal(t, 0, n.dialer.portm.InUse(), "local port must be released when frame construction fails")
	assert.Equal(t, 0, n.dialer.pending.Len())
}

func TestDialerPendingRemovedWhileWaiting(t *testing.T) {
	n, _ := newTappedNode(t)

	errCh := make(chan error, 1)
	go func() {
		_, err := n.DialIP(2, 80)
		errCh <- err
	}()

	require.Eventually(t, func() bool { return n.dialer.pending.Len() == 1 },
		2*time.Second, time.Millisecond)
	n.dialer.pending.Remove(1000) // first port from the shared pool

	select {
	case err := <-errCh:
		assert.ErrorIs(t, err, ErrWaitResponseTimeout)
	case <-time.After(2 * time.Second):
		t.Fatal("DialIP did not return after its pending request was removed")
	}
	assert.Equal(t, int64(1), n.Inspect().Failures.DialTimeout)
}

func TestDialerLegacyAckAdapterRejectsUnknownCommand(t *testing.T) {
	d := &Dialer{}
	assert.NotPanics(t, func() {
		d.handleAckOpenStream(packet.NewBufferWithCmd(0x7E))
	})
}

func TestHeartbeatRunStopsOnClosedTicker(t *testing.T) {
	n := New(nil)
	defer n.Close()

	ticks := make(chan time.Time)
	close(ticks)
	ticker := &time.Ticker{C: ticks}

	finished := make(chan struct{})
	go func() {
		n.heartbeat.run(ticker, n.done, func() {})
		close(finished)
	}()

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeat.run did not return on a closed ticker channel")
	}
}

func TestHeartbeatRunWithoutCheckerLogsAndStops(t *testing.T) {
	h := &captureHandler{}
	n := New(nil)
	n.SetLogger(slog.New(h))
	defer n.Close()

	n.heartbeat.interval = time.Minute
	n.heartbeat.lastWriteTime.Store(0) // far older than the interval
	n.heartbeat.SetChecker(nil)

	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	finished := make(chan struct{})
	go func() {
		n.heartbeat.run(ticker, n.done, func() {})
		close(finished)
	}()

	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("heartbeat.run did not stop when no checker was configured")
	}
	assert.True(t, h.contains("aliveChecker is nil"))
}

func TestRatesSinceSkipsNilStreams(t *testing.T) {
	base := time.Now()
	prev := Snapshot{
		At:      base,
		Streams: []*stream.State{nil, {Index: 1, BytesRead: 10, BytesWritten: 20}},
	}
	cur := Snapshot{
		At:      base.Add(time.Second),
		Streams: []*stream.State{nil, {Index: 2, BytesRead: 1, BytesWritten: 1}},
	}

	r := cur.RatesSince(prev)
	require.NotNil(t, r)
	assert.Empty(t, r.Streams, "nil and unmatched streams must be skipped")
}

func TestTracePortExhaustedNilNodeIsSafe(t *testing.T) {
	var n *Node
	assert.NotPanics(t, func() {
		n.tracePortExhausted("shared", assert.AnError)
	})
}

func TestListenHubOpenFrameWindowClamp(t *testing.T) {
	n, frames := newTappedNode(t)
	n.SetDetachRetention(0) // clean detached streams synchronously

	l, err := n.Listen(80)
	require.NoError(t, err)

	// An oversized peer window is clamped to the local effective window.
	req := packet.OpenStreamRequest{Domain: "target", WindowSize: uint32(stream.MaxWindowSize) + 1}
	f, err := wire.NewControl(wire.CmdOpenStream,
		wire.Addr{IP: 2, Port: 100}, wire.Addr{IP: 1, Port: 80}, req.Encode())
	require.NoError(t, err)
	require.NoError(t, n.listenHub.handleOpenFrame(f))

	ackFrame := requireFrame(t, frames)
	assert.Equal(t, wire.Cmd(wire.AckOpenStream), ackFrame.cmd)
	ack := packet.DecodeOpenStreamACK(ackFrame.payload)
	assert.True(t, ack.OK)
	assert.Equal(t, uint32(n.effectiveWindowSize()), ack.WindowSize, "remote window must be clamped to local")

	conn, err := l.Accept()
	require.NoError(t, err)
	st := conn.(*stream.Stream)
	require.NoError(t, st.CloseRead())
	require.NoError(t, st.CloseWrite())

}

func TestListenHubLegacyOpenAdapterRejectsUnknownCommand(t *testing.T) {
	n, frames := newTappedNode(t)

	n.listenHub.handleCmdOpenStream(packet.NewBufferWithCmd(0x7E))
	requireNoFrame(t, frames)
}

func TestDefaultSchedulerConfig(t *testing.T) {
	cfg := DefaultSchedulerConfig()
	assert.Equal(t, SchedulerPolicyCostBudget, cfg.Policy)
	assert.Equal(t, sched.DefaultQuantumBytes, cfg.QuantumBytes)
	assert.Equal(t, sched.DefaultMinChargeBytes, cfg.MinChargeBytes)
}
