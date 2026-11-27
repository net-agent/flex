package switcher

import (
	"log/slog"
	"testing"
	"time"

	"github.com/net-agent/flex/v4/packet"
	"github.com/stretchr/testify/require"
)

func newBarePresenceCenter() *presenceCenter {
	return &presenceCenter{
		logger:   slog.Default(),
		state:    make(map[string]presenceEntry),
		subs:     make(map[uint16]*presenceSub),
		watchers: make(map[string]map[uint16]struct{}),
	}
}

func TestNewPresenceCenterNilLogger(t *testing.T) {
	pc := newPresenceCenter(nil)
	t.Cleanup(pc.stop)

	require.NotNil(t, pc.logger)
	require.NotNil(t, pc.state)
	require.NotNil(t, pc.subs)
	require.NotNil(t, pc.watchers)
	require.NotNil(t, pc.events)
	require.NotNil(t, pc.done)
}

// TestPresenceCenterRunSweepsOnTicker covers the ticker arm of run(). The test
// waits for several sweep periods, then stops run and inspects state only after
// the goroutine has exited (avoids a data race with run's map access).
func TestPresenceCenterRunSweepsOnTicker(t *testing.T) {
	pc := &presenceCenter{
		events:     make(chan presenceMsg),
		done:       make(chan struct{}),
		logger:     slog.Default(),
		offlineTTL: time.Millisecond,
		sweepEvery: time.Millisecond,
		state: map[string]presenceEntry{
			"gone": {online: false, offlineAt: time.Now().Add(-time.Hour)},
		},
		subs:     make(map[uint16]*presenceSub),
		watchers: make(map[string]map[uint16]struct{}),
	}

	exited := make(chan struct{})
	go func() {
		defer close(exited)
		pc.run()
	}()
	t.Cleanup(func() {
		pc.stop()
		<-exited
	})

	start := time.Now()
	require.Eventually(t, func() bool {
		return time.Since(start) > 50*time.Millisecond
	}, time.Second, time.Millisecond, "ticker should have fired")

	pc.stop()
	<-exited
	_, ok := pc.state["gone"]
	require.False(t, ok, "expired offline entry must be swept by the ticker")
}

func TestPresenceCenterFanoutSkipsStaleWatcher(t *testing.T) {
	pc := newBarePresenceCenter()
	pc.watchers["ghost"] = map[uint16]struct{}{12: {}}

	pc.fanout(presenceMsg{kind: presenceEventOnline, seq: 3, domain: "ghost", ip: 1}, 1, true)

	require.Empty(t, pc.subs, "missing subscriber record must not be recreated")
	require.Contains(t, pc.watchers["ghost"], uint16(12),
		"stale reverse-index entries are left for purge")
}

// TestPresenceCenterFanoutDropsDeadSubscriber verifies that a notify write
// failure removes the subscriber from both indexes instead of retrying forever.
func TestPresenceCenterFanoutDropsDeadSubscriber(t *testing.T) {
	pc := newBarePresenceCenter()
	ctx := newBrokenContext(t, 4, "dead-sub", 40)
	pc.subs[ctx.IP] = &presenceSub{ctx: ctx, domains: map[string]struct{}{"dead": {}}}
	pc.watchers["dead"] = map[uint16]struct{}{ctx.IP: {}}

	pc.fanout(presenceMsg{kind: presenceEventOnline, seq: 1, domain: "dead", ip: 1}, 1, true)

	require.NotZero(t, ctx.Stats.BytesSent, "notify must be attempted")
	require.NotContains(t, pc.subs, ctx.IP, "dead subscriber must be purged")
	_, ok := pc.watchers["dead"]
	require.False(t, ok, "empty reverse index must be removed")
}

func TestPresenceHandleSubscribeMsgQuotaForNewSubscriber(t *testing.T) {
	pc := newBarePresenceCenter()
	pc.maxSubscriptions.Store(1)
	ctx := newBrokenContext(t, 1, "sub", 30)

	pc.handleSubscribeMsg(presenceMsg{
		kind:    presenceEventSubscribe,
		subCtx:  ctx,
		op:      packet.SubscribeAdd,
		domains: []string{"a", "b"},
		ackPort: 9,
	})

	require.NotContains(t, pc.subs, ctx.IP, "rejected new subscriber must be rolled back")
	require.Empty(t, pc.watchers)
	require.NotZero(t, ctx.Stats.BytesSent, "rejection must send an error ACK")
}

func TestPresenceHandleSubscribeMsgUnknownOp(t *testing.T) {
	pc := newBarePresenceCenter()
	ctx := newBrokenContext(t, 2, "sub", 31)

	pc.handleSubscribeMsg(presenceMsg{
		kind:    presenceEventSubscribe,
		subCtx:  ctx,
		op:      byte(42),
		domains: []string{"a"},
		ackPort: 9,
	})

	require.Empty(t, pc.watchers, "unknown op must not create subscriptions")
	require.NotZero(t, ctx.Stats.BytesSent, "unknown op must send a failure ACK")
}

func TestPresenceReplySubscribeACKSendFailure(t *testing.T) {
	pc := newBarePresenceCenter()
	ctx := newBrokenContext(t, 3, "sub", 32)

	pc.replySubscribeACK(ctx, 99, packet.SubscribeACK{OK: true})

	require.NotZero(t, ctx.Stats.BytesSent, "reply must be attempted")
}
