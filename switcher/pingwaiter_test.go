package switcher

import (
	"sync"
	"testing"

	"github.com/net-agent/flex/v4/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pingFrame(t *testing.T, payload string) *wire.Frame {
	t.Helper()
	f, err := wire.NewControl(wire.AckPingDomain, wire.Addr{}, wire.Addr{}, []byte(payload))
	require.NoError(t, err)
	return f
}

func TestPingWaiterDeliverClose(t *testing.T) {
	w := newPingWaiter()
	f := pingFrame(t, "ok")
	require.True(t, w.deliver(f))
	require.Len(t, w.ch, 1)

	// A second delivery is rejected while the buffer is full; the caller keeps
	// ownership of the rejected frame.
	extra := pingFrame(t, "extra")
	assert.False(t, w.deliver(extra))
	extra.Release()

	// close drains the queued frame and closes the channel; later deliveries
	// must be rejected rather than panic.
	w.close()
	assert.True(t, w.closed)
	late := pingFrame(t, "late")
	assert.False(t, w.deliver(late))
	late.Release()
	w.close() // idempotent
}

func TestPingWaiterDeliverCloseRace(t *testing.T) {
	for i := 0; i < 1000; i++ {
		w := newPingWaiter()
		f := pingFrame(t, "race")
		var delivered bool
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			delivered = w.deliver(f)
		}()
		go func() {
			defer wg.Done()
			w.close()
		}()
		wg.Wait()
		if !delivered {
			f.Release()
		}
	}
}
