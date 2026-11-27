package node

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/net-agent/flex/v4/internal/idpool"
	"github.com/net-agent/flex/v4/internal/pending"
	"github.com/net-agent/flex/v4/packet"
	"github.com/net-agent/flex/v4/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func manyDomains(count int) []string {
	domains := make([]string, count)
	for i := range domains {
		domains[i] = "domain-" + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	return domains
}

func TestWatcherSubscribeValidation(t *testing.T) {
	n := New(nil)
	defer n.Close()

	_, err := n.Watch(time.Second)
	assert.ErrorIs(t, err, ErrEmptyWatchDomains)

	_, err = n.Watch(time.Second, manyDomains(maxWatchDomains+1)...)
	assert.ErrorIs(t, err, ErrTooManyDomains)
}

func TestWatcherWatchSendFailure(t *testing.T) {
	n := New(nil) // no link: the subscribe frame cannot be written
	defer n.Close()

	_, err := n.Watch(time.Second, "target")
	assert.ErrorIs(t, err, ErrWriterIsNil)
	assert.Equal(t, int64(1), n.Inspect().Failures.WatchFailed)
	assert.Equal(t, 0, n.watcher.portm.InUse(), "port must be released on write failure")
}

func TestWatcherPortPoolExhausted(t *testing.T) {
	n := New(nil)
	defer n.Close()

	var traced atomic.Value
	n.SetTrace(&Trace{PortExhausted: func(pool string, err error) {
		traced.Store(pool + ":" + err.Error())
	}})

	for {
		if _, err := n.watcher.portm.Allocate(); err != nil {
			break
		}
	}

	_, err := n.Watch(time.Second, "target")
	assert.ErrorIs(t, err, idpool.ErrPoolExhausted)
	assert.Equal(t, int64(1), n.Inspect().Failures.PortExhausted)
	require.NotNil(t, traced.Load())
	assert.Equal(t, "watcher:"+idpool.ErrPoolExhausted.Error(), traced.Load().(string))
}

func TestWatcherDuplicatePendingRegistration(t *testing.T) {
	n := New(nil)
	defer n.Close()

	// The first subscribe allocates port 1; pre-registering it makes Register fail.
	_, err := n.watcher.pending.Register(1)
	require.NoError(t, err)

	_, err = n.Watch(time.Second, "target")
	assert.ErrorIs(t, err, pending.ErrAlreadyRegistered)
	assert.Equal(t, 0, n.watcher.portm.InUse())
}

func TestWatcherOversizedSubscribePayload(t *testing.T) {
	n := New(nil)
	defer n.Close()

	domains := make([]string, maxWatchDomains)
	for i := range domains {
		domains[i] = strings.Repeat("d", 400)
	}

	_, err := n.Watch(time.Second, domains...)
	assert.ErrorIs(t, err, wire.ErrPayloadOverflow)
	assert.Equal(t, 0, n.watcher.portm.InUse())
	assert.Equal(t, 0, n.watcher.pending.Len())
}

func TestWatcherPendingRemovedWhileWaiting(t *testing.T) {
	n, _ := newTappedNode(t)

	type result struct {
		states []packet.PresenceState
		err    error
	}
	resCh := make(chan result, 1)
	go func() {
		states, err := n.Watch(5*time.Second, "target")
		resCh <- result{states: states, err: err}
	}()

	require.Eventually(t, func() bool { return n.watcher.pending.Len() == 1 },
		2*time.Second, time.Millisecond)
	n.watcher.pending.Remove(1)

	select {
	case res := <-resCh:
		assert.ErrorIs(t, res.err, ErrWatchTimeout)
		assert.Empty(t, res.states)
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not return after its pending request was removed")
	}
	assert.Equal(t, int64(1), n.Inspect().Failures.WatchFailed)
}

func TestWatcherLegacyAdapters(t *testing.T) {
	n := New(nil)
	defer n.Close()
	w := &n.watcher

	t.Run("ack success completes pending with snapshot", func(t *testing.T) {
		ch, err := w.pending.Register(7)
		require.NoError(t, err)

		pbuf := packet.NewBufferWithCmd(packet.AckSubscribePresence)
		pbuf.SetSrc(packet.SwitcherIP, 0)
		pbuf.SetDist(1, 7)
		require.NoError(t, pbuf.SetPayload((&packet.SubscribeACK{
			OK:     true,
			States: []packet.PresenceState{{Domain: "a", Online: true, IP: 9}},
		}).Encode()))
		w.handleAckSubscribePresence(pbuf)
		packet.PutBuffer(pbuf)

		res := <-ch
		require.NoError(t, res.Err)
		require.Len(t, res.Val, 1)
		assert.Equal(t, "a", res.Val[0].Domain)
		assert.True(t, res.Val[0].Online)
	})

	t.Run("rejected ack completes pending with error", func(t *testing.T) {
		ch, err := w.pending.Register(8)
		require.NoError(t, err)

		pbuf := packet.NewBufferWithCmd(packet.AckSubscribePresence)
		pbuf.SetSrc(packet.SwitcherIP, 0)
		pbuf.SetDist(1, 8)
		require.NoError(t, pbuf.SetPayload((&packet.SubscribeACK{Error: "denied"}).Encode()))
		w.handleAckSubscribePresence(pbuf)
		packet.PutBuffer(pbuf)

		res := <-ch
		assert.EqualError(t, res.Err, "denied")
		assert.Nil(t, res.Val)
	})

	t.Run("notify is applied and dispatched", func(t *testing.T) {
		events := make(chan packet.PresenceEvent, 1)
		w.SetHandler(func(ev packet.PresenceEvent) { events <- ev })

		ev := packet.PresenceEvent{Seq: 1, Version: 1, Domain: "notify-target", Online: true, IP: 11}
		pbuf := packet.NewBufferWithCmd(packet.CmdNotifyPresence)
		pbuf.SetSrc(packet.SwitcherIP, 0)
		pbuf.SetDist(1, 0)
		require.NoError(t, pbuf.SetPayload(ev.Encode()))
		w.handleCmdNotifyPresence(pbuf)
		packet.PutBuffer(pbuf)

		select {
		case got := <-events:
			assert.Equal(t, "notify-target", got.Domain)
			assert.True(t, got.Online)
			assert.Equal(t, uint16(11), got.IP)
		case <-time.After(2 * time.Second):
			t.Fatal("notify event was not dispatched")
		}
		st, ok := w.view.get("notify-target")
		assert.True(t, ok)
		assert.Equal(t, uint64(1), st.Version)
	})

	t.Run("unknown command is ignored by both adapters", func(t *testing.T) {
		bad := packet.NewBufferWithCmd(0x7E)
		w.handleAckSubscribePresence(bad)
		w.handleCmdNotifyPresence(bad)
		packet.PutBuffer(bad)

		assert.Equal(t, 0, w.pending.Len())
	})
}

func TestWatcherResyncDedupAndSnapshot(t *testing.T) {
	n, _ := newTappedNode(t)
	w := &n.watcher

	w.view.applySnapshot([]packet.PresenceState{{Domain: "d", Online: true, IP: 7, Version: 1}})

	var events int32
	w.SetHandler(func(packet.PresenceEvent) { atomic.AddInt32(&events, 1) })

	w.resync("d")
	require.Eventually(t, func() bool { return w.pending.Len() == 1 },
		2*time.Second, time.Millisecond)

	// A second gap notification for the same domain must not start a second
	// in-flight resync.
	w.resync("d")
	assert.Equal(t, 1, w.pending.Len(), "duplicate resync must be deduplicated")

	require.NoError(t, w.pending.Complete(1, []packet.PresenceState{
		{Domain: "d", Online: false, IP: 7, Version: 3},
	}, nil))

	require.Eventually(t, func() bool {
		st, ok := w.view.get("d")
		return ok && st.Version == 3 && !st.Online
	}, 2*time.Second, time.Millisecond)
	require.Eventually(t, func() bool {
		_, loaded := w.resyncing.Load("d")
		return !loaded
	}, 2*time.Second, time.Millisecond)

	assert.Equal(t, int32(0), atomic.LoadInt32(&events), "resync snapshots must not be delivered as notify events")
}

func TestWatcherResyncFailureIsCounted(t *testing.T) {
	n := New(nil) // no link: the resync subscribe write fails
	defer n.Close()

	n.watcher.resync("d")

	require.Eventually(t, func() bool {
		return atomic.LoadInt64(&n.failures.WatchFailed) == 1
	}, 2*time.Second, time.Millisecond)
	assert.Empty(t, n.ListPresence())
}
