package node

import (
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errDispatcherBoom = errors.New("handler boom")

func TestBufferToFrameBranches(t *testing.T) {
	t.Run("nil buffer", func(t *testing.T) {
		f, err := bufferToFrame(nil)
		require.NoError(t, err)
		assert.Nil(t, f)
	})

	t.Run("unknown command", func(t *testing.T) {
		f, err := bufferToFrame(packet.NewBufferWithCmd(0x7E))
		require.Error(t, err)
		assert.ErrorContains(t, err, "unknown command")
		assert.Nil(t, f)
	})

	t.Run("ack push data carries ack size and no payload", func(t *testing.T) {
		pbuf := packet.NewBufferWithCmd(packet.AckPushStreamData)
		pbuf.SetSrc(7, 8)
		pbuf.SetDist(9, 10)
		pbuf.SetDataACKSize(321)

		f, err := bufferToFrame(pbuf)
		require.NoError(t, err)
		require.NotNil(t, f)
		defer f.Release()

		assert.Equal(t, wire.ClassData, f.Class())
		assert.Equal(t, wire.Cmd(wire.AckPushStreamData), f.Cmd())
		assert.Equal(t, uint16(321), f.AckSize())
		assert.Empty(t, f.Payload())
		assert.Equal(t, wire.Addr{IP: 7, Port: 8}, f.Src())
		assert.Equal(t, wire.Addr{IP: 9, Port: 10}, f.Dst())
	})

	t.Run("control buffer keeps payload and addresses", func(t *testing.T) {
		pbuf := packet.NewBufferWithCmd(packet.CmdPingDomain)
		pbuf.SetSrc(3, 4)
		pbuf.SetDist(packet.SwitcherIP, 0)
		require.NoError(t, pbuf.SetPayload([]byte("probe")))

		f, err := bufferToFrame(pbuf)
		require.NoError(t, err)
		require.NotNil(t, f)
		defer f.Release()

		assert.Equal(t, wire.ClassControl, f.Class())
		assert.Equal(t, wire.Cmd(wire.CmdPingDomain), f.Cmd())
		assert.Equal(t, []byte("probe"), f.Payload())
	})
}

func TestFrameToBufferBranches(t *testing.T) {
	t.Run("valid frame round-trips", func(t *testing.T) {
		f, err := wire.NewControl(wire.CmdPingDomain,
			wire.Addr{IP: 7, Port: 8}, wire.Addr{IP: 9, Port: 10}, []byte("ping"))
		require.NoError(t, err)
		defer f.Release()

		b := frameToBuffer(f)
		require.NotNil(t, b)
		defer packet.PutBuffer(b)

		assert.Equal(t, packet.CmdPingDomain, b.Cmd(), "class bit must be stripped for legacy handlers")
		assert.Equal(t, uint16(7), b.SrcIP())
		assert.Equal(t, uint16(8), b.SrcPort())
		assert.Equal(t, uint16(9), b.DistIP())
		assert.Equal(t, uint16(10), b.DistPort())
		assert.Equal(t, []byte("ping"), b.Payload)
	})

	t.Run("invalid frame header fails encoding", func(t *testing.T) {
		assert.Nil(t, frameToBuffer(&wire.Frame{}), "zero-value frame has an unregistered class")
	})
}

func TestDispatcherStartRejectsRepeatRun(t *testing.T) {
	n := New(nil)
	d := &n.dispatcher
	require.NoError(t, d.start())
	t.Cleanup(d.stop)

	assert.True(t, d.isRunning())
	assert.ErrorIs(t, d.start(), ErrRepeatRun)
}

func TestDispatcherDispatchRejectsUnknownBuffer(t *testing.T) {
	n := New(nil)
	require.NoError(t, n.dispatcher.start())
	t.Cleanup(n.dispatcher.stop)

	err := n.dispatcher.dispatch(packet.NewBufferWithCmd(0x7E))
	require.Error(t, err)
	assert.ErrorContains(t, err, "unknown command")
}

// newDispatcherHarness starts a dispatcher with both lanes running and a
// capturing logger.
func newDispatcherHarness(t *testing.T) (*Node, *Dispatcher, *captureHandler) {
	t.Helper()
	n := New(nil)
	h := &captureHandler{}
	n.SetDomain("dispatcher-node")
	n.SetLogger(slog.New(h))

	d := &n.dispatcher
	require.NoError(t, d.start())
	go d.processCmdChan()
	go d.processDataChan()
	t.Cleanup(d.stop)
	return n, d, h
}

func TestDispatcherCustomLegacyHandlers(t *testing.T) {
	t.Run("cmd lane", func(t *testing.T) {
		_, d, _ := newDispatcherHarness(t)

		type received struct {
			cmd     byte
			payload string
		}
		got := make(chan received, 1)
		d.RegisterCmdHandler(packet.CmdPingDomain, func(b *packet.Buffer) {
			got <- received{cmd: b.Cmd(), payload: string(b.Payload)}
		})

		f, err := wire.NewControl(wire.CmdPingDomain,
			wire.Addr{IP: 2, Port: 3}, wire.Addr{IP: 1, Port: 4}, []byte("legacy-cmd"))
		require.NoError(t, err)
		require.NoError(t, d.dispatchFrame(f))

		select {
		case r := <-got:
			assert.Equal(t, packet.CmdPingDomain, r.cmd)
			assert.Equal(t, "legacy-cmd", r.payload)
		case <-time.After(2 * time.Second):
			t.Fatal("legacy cmd handler was not called")
		}
	})

	t.Run("data lane", func(t *testing.T) {
		_, d, _ := newDispatcherHarness(t)

		got := make(chan uint16, 1)
		d.RegisterDataHandler(packet.AckPushStreamData, func(b *packet.Buffer) {
			got <- b.DataACKSize()
		})

		f := wire.NewAckData(1234, wire.Addr{IP: 2, Port: 3}, wire.Addr{IP: 1, Port: 4})
		require.NoError(t, d.dispatchFrame(f))

		select {
		case size := <-got:
			assert.Equal(t, uint16(1234), size)
		case <-time.After(2 * time.Second):
			t.Fatal("legacy data handler was not called")
		}
	})
}

func TestDispatcherUnknownCommandWarnings(t *testing.T) {
	t.Run("cmd lane", func(t *testing.T) {
		_, d, h := newDispatcherHarness(t)
		d.cmdChan <- &wire.Frame{} // invalid class + unregistered cmd 0
		require.Eventually(t, func() bool { return h.contains("unknown cmd") },
			2*time.Second, time.Millisecond)
	})

	t.Run("data lane", func(t *testing.T) {
		_, d, h := newDispatcherHarness(t)
		d.dataChan <- &wire.Frame{}
		require.Eventually(t, func() bool { return h.contains("unknown cmd") },
			2*time.Second, time.Millisecond)
	})
}

func TestDispatcherLegacyConversionFailure(t *testing.T) {
	t.Run("cmd lane", func(t *testing.T) {
		_, d, h := newDispatcherHarness(t)
		called := make(chan struct{}, 1)
		d.RegisterCmdHandler(0, func(*packet.Buffer) { called <- struct{}{} })

		d.cmdChan <- &wire.Frame{} // frameToBuffer fails on the invalid class
		require.Eventually(t, func() bool { return h.contains("unexpected cmd") },
			2*time.Second, time.Millisecond)
		select {
		case <-called:
			t.Fatal("handler must not be called when conversion fails")
		default:
		}
	})

	t.Run("data lane", func(t *testing.T) {
		_, d, h := newDispatcherHarness(t)
		called := make(chan struct{}, 1)
		d.RegisterDataHandler(0, func(*packet.Buffer) { called <- struct{}{} })

		d.dataChan <- &wire.Frame{}
		require.Eventually(t, func() bool { return h.contains("unexpected data cmd") },
			2*time.Second, time.Millisecond)
		select {
		case <-called:
			t.Fatal("handler must not be called when conversion fails")
		default:
		}
	})
}

func TestDispatcherFrameHandlerErrorsAreLogged(t *testing.T) {
	t.Run("cmd lane", func(t *testing.T) {
		_, d, h := newDispatcherHarness(t)
		d.frameCmdHandlers[wire.Cmd(wire.CmdPingDomain)] = func(*wire.Frame) error {
			return errDispatcherBoom
		}

		f, err := wire.NewControl(wire.CmdPingDomain,
			wire.Addr{IP: 2, Port: 3}, wire.Addr{IP: 1, Port: 4}, nil)
		require.NoError(t, err)
		require.NoError(t, d.dispatchFrame(f))

		require.Eventually(t, func() bool { return h.contains("frame cmd handler failed") },
			2*time.Second, time.Millisecond)
	})

	t.Run("data lane", func(t *testing.T) {
		_, d, h := newDispatcherHarness(t)
		d.frameDataHandlers[wire.Cmd(wire.CmdPushStreamData)] = func(*wire.Frame) error {
			return errDispatcherBoom
		}

		f, err := wire.NewData(wire.Addr{IP: 2, Port: 3}, wire.Addr{IP: 1, Port: 4}, []byte("x"))
		require.NoError(t, err)
		require.NoError(t, d.dispatchFrame(f))

		require.Eventually(t, func() bool { return h.contains("frame data handler failed") },
			2*time.Second, time.Millisecond)
	})
}
