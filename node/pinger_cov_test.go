package node

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPingerLegacyPingAdapter(t *testing.T) {
	tests := []struct {
		name     string
		domain   string
		wantData string
	}{
		{name: "empty domain is a liveness probe", domain: "", wantData: ""},
		{name: "matching domain", domain: "tap-node", wantData: ""},
		{name: "mismatched domain is rejected", domain: "other-node", wantData: "domain not match"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n, frames := newTappedNode(t)

			pbuf := packet.NewBufferWithCmd(packet.CmdPingDomain)
			pbuf.SetSrc(2, 77)
			pbuf.SetDist(packet.SwitcherIP, 0)
			require.NoError(t, pbuf.SetPayload([]byte(tc.domain)))

			n.pinger.handleCmdPingDomain(pbuf)
			packet.PutBuffer(pbuf)

			ack := requireFrame(t, frames)
			assert.Equal(t, wire.ClassControl, ack.class)
			assert.Equal(t, wire.Cmd(wire.AckPingDomain), ack.cmd)
			assert.Equal(t, wire.Addr{IP: 1}, ack.src)
			assert.Equal(t, wire.Addr{IP: 2, Port: 77}, ack.dst)
			assert.Equal(t, tc.wantData, string(ack.payload))
		})
	}
}

func TestPingerLegacyAckAdapter(t *testing.T) {
	n := New(nil)
	defer n.Close()

	t.Run("error payload completes the request with an error", func(t *testing.T) {
		ch, err := n.pinger.pending.Register(5)
		require.NoError(t, err)

		pbuf := packet.NewBufferWithCmd(packet.AckPingDomain)
		pbuf.SetDist(0, 5)
		require.NoError(t, pbuf.SetPayload([]byte("nope")))
		n.pinger.handleAckPingDomain(pbuf)
		packet.PutBuffer(pbuf)

		res := <-ch
		assert.EqualError(t, res.Err, "nope")
	})

	t.Run("empty payload completes successfully", func(t *testing.T) {
		ch, err := n.pinger.pending.Register(6)
		require.NoError(t, err)

		pbuf := packet.NewBufferWithCmd(packet.AckPingDomain)
		pbuf.SetDist(0, 6)
		n.pinger.handleAckPingDomain(pbuf)
		packet.PutBuffer(pbuf)

		res := <-ch
		assert.NoError(t, res.Err)
	})

	t.Run("unknown command is ignored by both adapters", func(t *testing.T) {
		bad := packet.NewBufferWithCmd(0x7E)
		n.pinger.handleCmdPingDomain(bad)
		n.pinger.handleAckPingDomain(bad)
		packet.PutBuffer(bad)

		assert.Equal(t, 0, n.pinger.pending.Len())
	})
}

func TestPingerPingDomainOversizedPayload(t *testing.T) {
	n, frames := newTappedNode(t)

	_, err := n.PingDomain(strings.Repeat("d", wire.MaxPayloadSize+1), time.Second)
	require.Error(t, err)
	assert.ErrorIs(t, err, wire.ErrPayloadOverflow)

	assert.Equal(t, 0, n.pinger.portm.InUse(), "port must be released when frame construction fails")
	assert.Equal(t, 0, n.pinger.pending.Len())
	assert.Equal(t, int64(0), n.Inspect().Failures.PingFailed, "local encode failure is not a wire failure")
	requireNoFrame(t, frames)
}

func TestPingerPendingRemovedWhileWaiting(t *testing.T) {
	n, _ := newTappedNode(t)

	errCh := make(chan error, 1)
	go func() {
		_, err := n.PingDomain("target", 5*time.Second)
		errCh <- err
	}()

	require.Eventually(t, func() bool { return n.pinger.pending.Len() == 1 },
		2*time.Second, time.Millisecond)
	n.pinger.pending.Remove(1) // closes the wait channel: request aborted

	select {
	case err := <-errCh:
		assert.ErrorIs(t, err, ErrPingDomainTimeout)
	case <-time.After(2 * time.Second):
		t.Fatal("PingDomain did not return after its pending request was removed")
	}
	assert.Equal(t, int64(1), n.Inspect().Failures.PingFailed)
}

func TestPingerReplyWriteFailureIsLogged(t *testing.T) {
	h := &captureHandler{}
	n := New(nil) // no link: the reply write fails
	n.SetDomain("pinger-node")
	n.SetLogger(slog.New(h))
	defer n.Close()

	f, err := wire.NewControl(wire.CmdPingDomain,
		wire.Addr{IP: 2, Port: 77}, wire.SwitcherAddr, nil)
	require.NoError(t, err)
	defer f.Release()

	require.NoError(t, n.pinger.handlePingFrame(f))
	assert.True(t, h.contains("write ping response failed"))
}
