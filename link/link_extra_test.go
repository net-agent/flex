package link

import (
	"bytes"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/internal/sched"
	"github.com/net-agent/flex/v3/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newScriptLink(t *testing.T, tr Transport, cfg ...Config) *Link {
	t.Helper()
	var lnk *Link
	if len(cfg) > 0 {
		lnk = NewDirectWithConfig(tr, cfg[0])
	} else {
		lnk = NewDirect(tr)
	}
	t.Cleanup(func() { _ = lnk.Close() })
	return lnk
}

func TestLinkFrameTap(t *testing.T) {
	var (
		mu    sync.Mutex
		reads [][]byte
		src   = wire.Addr{IP: 1, Port: 2}
		dst   = wire.Addr{IP: 3, Port: 4}
	)

	tr := newScriptTransport(FrameBytes{
		Header:  encodeHeader(t, wire.Header{Class: wire.ClassData, Cmd: wire.Cmd(wire.CmdPushStreamData), Src: src, Dst: dst, Size: 3}),
		Payload: []byte("abc"),
	})
	lnk := newScriptLink(t, tr)

	lnk.SetFrameTap(func(write bool, f *wire.Frame) {
		require.NotNil(t, f)
		mu.Lock()
		defer mu.Unlock()
		if !write {
			reads = append(reads, append([]byte(nil), f.Payload()...))
		}
	})

	f, err := lnk.ReadFrame()
	require.NoError(t, err)
	f.Release()
	assert.Equal(t, [][]byte{[]byte("abc")}, reads)

	// Outbound frames fire the tap with write=true.
	f, err = wire.NewData(src, dst, []byte("out"))
	require.NoError(t, err)
	require.NoError(t, lnk.SendFrameSync(f))
	assert.Len(t, tr.written(), 1)

	// A nil tap clears the observer.
	lnk.SetFrameTap(nil)
	lnk.fireTap(false, nil)
	assert.Len(t, reads, 1)

	// Panics in a tap are not swallowed: a broken observer fails loudly.
	lnk.SetFrameTap(func(bool, *wire.Frame) { panic("tap boom") })
	assert.PanicsWithValue(t, "tap boom", func() { lnk.fireTap(true, nil) })
}

func TestNewDirectSchedulerOption(t *testing.T) {
	tr := &scriptTransport{}
	cfg := sched.SchedulerConfig{Policy: sched.PolicyPacketQuantum, QuantumPackets: 2}
	lnk := NewDirect(tr, cfg)
	t.Cleanup(func() { _ = lnk.Close() })
	assert.Equal(t, cfg.Normalized(), lnk.SchedulerConfig())
}

func TestLinkConfigAndAccessors(t *testing.T) {
	tr := &scriptTransport{
		localAddr:  &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 9000},
		remoteAddr: &net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 9001},
	}
	cfg := sched.SchedulerConfig{Policy: sched.PolicyPacketQuantum, QuantumPackets: 4}
	lnk := newScriptLink(t, tr, Config{Scheduler: cfg})

	assert.Same(t, tr, lnk.Transport())
	assert.Equal(t, cfg.Normalized(), lnk.SchedulerConfig())
	assert.Equal(t, tr.localAddr.String(), lnk.LocalAddr().String())
	assert.Equal(t, tr.remoteAddr.String(), lnk.RemoteAddr().String())
}

func TestLinkTimeouts(t *testing.T) {
	tr := &scriptTransport{}
	lnk := newScriptLink(t, tr)

	require.NoError(t, lnk.SetReadTimeout(0))
	assert.True(t, tr.readDeadline.IsZero())
	require.NoError(t, lnk.SetReadTimeout(-time.Second))
	assert.True(t, tr.readDeadline.IsZero())
	require.NoError(t, lnk.SetReadTimeout(time.Hour))
	assert.False(t, tr.readDeadline.IsZero())

	require.NoError(t, lnk.SetWriteTimeout(0))
	assert.True(t, tr.writeDeadline.IsZero())
	require.NoError(t, lnk.SetWriteTimeout(-time.Second))
	assert.True(t, tr.writeDeadline.IsZero())
	require.NoError(t, lnk.SetWriteTimeout(time.Hour))
	assert.False(t, tr.writeDeadline.IsZero())
}

func TestLinkNilFrameSends(t *testing.T) {
	tr := &scriptTransport{}
	lnk := newScriptLink(t, tr)

	assert.NoError(t, lnk.SendFrame(nil))
	assert.NoError(t, lnk.SendFrameSync(nil))
	require.NoError(t, lnk.Close())
	assert.NoError(t, lnk.SendFrameSync(nil))
	assert.Empty(t, tr.written())
}

func TestLinkSendFrameSync(t *testing.T) {
	src := wire.Addr{IP: 1, Port: 2}
	dst := wire.Addr{IP: 3, Port: 4}

	t.Run("success", func(t *testing.T) {
		tr := &scriptTransport{}
		lnk := newScriptLink(t, tr)
		f, err := wire.NewControl(wire.CmdOpenStream, src, dst, []byte("open"))
		require.NoError(t, err)
		require.NoError(t, lnk.SendFrameSync(f))
		frames := tr.written()
		require.Len(t, frames, 1)
		assert.True(t, bytes.Equal(frames[0].Payload, []byte("open")))
	})

	t.Run("write error closes transport", func(t *testing.T) {
		want := errBoom()
		tr := &scriptTransport{writeErr: want, closeCh: make(chan struct{})}
		lnk := newScriptLink(t, tr)
		f, err := wire.NewData(src, dst, []byte("x"))
		require.NoError(t, err)
		assert.ErrorIs(t, lnk.SendFrameSync(f), want)
		select {
		case <-tr.closeCh:
		case <-time.After(time.Second):
			t.Fatal("transport not closed after write error")
		}
	})

	t.Run("closed", func(t *testing.T) {
		lnk := newScriptLink(t, &scriptTransport{})
		require.NoError(t, lnk.Close())
		f, err := wire.NewData(src, dst, []byte("x"))
		require.NoError(t, err)
		assert.ErrorIs(t, lnk.SendFrameSync(f), ErrWriterClosed)
	})
}

func TestLinkSendValidationErrors(t *testing.T) {
	lnk := newScriptLink(t, &scriptTransport{})
	src := wire.Addr{IP: 1}
	dst := wire.Addr{IP: 2}

	assert.ErrorIs(t, lnk.SendControl(wire.ControlCmd(wire.CmdPushStreamData), src, dst, nil), wire.ErrClassMismatch)
	assert.ErrorIs(t, lnk.SendData(src, dst, make([]byte, wire.MaxPayloadSize+1)), wire.ErrPayloadOverflow)
	assert.ErrorIs(t, lnk.SendEmpty(wire.EmptyCmd(wire.AckPushStreamData), src, dst), wire.ErrPayloadNotAllowed)
	assert.ErrorIs(t, lnk.SendEmpty(wire.EmptyCmd(wire.CmdAdmit), src, dst), wire.ErrClassMismatch)
}

func TestLinkReadFrameErrors(t *testing.T) {
	t.Run("header read error", func(t *testing.T) {
		want := errBoom()
		lnk := newScriptLink(t, &scriptTransport{readErr: want})
		_, err := lnk.ReadFrame()
		assert.ErrorIs(t, err, want)
	})

	t.Run("decode error", func(t *testing.T) {
		var bad [wire.HeaderSize]byte
		bad[0] = byte(wire.CmdOpenStream) // control command without the class bit
		lnk := newScriptLink(t, newScriptTransport(FrameBytes{Header: bad}))
		_, err := lnk.ReadFrame()
		assert.ErrorIs(t, err, wire.ErrClassMismatch)
	})

	t.Run("legacy decode error", func(t *testing.T) {
		var bad [wire.HeaderSize]byte
		bad[0] = 0x01 // unknown command
		lnk := newScriptLink(t, newScriptTransport(FrameBytes{Header: bad}), Config{LegacyWire: true})
		_, err := lnk.ReadFrame()
		assert.ErrorIs(t, err, wire.ErrUnknownCommand)
	})

	t.Run("read payload error fallback", func(t *testing.T) {
		hdr := encodeHeader(t, wire.Header{Class: wire.ClassControl, Cmd: wire.Cmd(wire.CmdPingDomain), Size: 4})
		lnk := newScriptLink(t, newScriptTransport(FrameBytes{Header: hdr}))
		_, err := lnk.ReadFrame()
		assert.ErrorIs(t, err, ErrFrameSizeMismatch)
	})

	t.Run("read payload into error", func(t *testing.T) {
		want := errors.New("into boom")
		inner := &scriptTransport{}
		tr := &intoTransport{scriptTransport: inner, intoErr: want}
		hdr := encodeHeader(t, wire.Header{Class: wire.ClassControl, Cmd: wire.Cmd(wire.CmdPingDomain), Size: 4})
		inner.frames = append(inner.frames, scriptFrame{header: hdr, payload: []byte("ping")})
		lnk := newScriptLink(t, tr)
		_, err := lnk.ReadFrame()
		assert.ErrorIs(t, err, want)
	})
}

func TestLinkReadFramePayloadPaths(t *testing.T) {
	src := wire.Addr{IP: 1, Port: 1000}
	dst := wire.Addr{IP: 2, Port: 80}
	dataHdr := encodeHeader(t, wire.Header{
		Class: wire.ClassData, Cmd: wire.Cmd(wire.CmdPushStreamData), Src: src, Dst: dst, Size: 3,
	})

	t.Run("ReadPayload fallback copies", func(t *testing.T) {
		tr := newScriptTransport(
			FrameBytes{Header: dataHdr, Payload: []byte("abc")},
			FrameBytes{Header: dataHdr, Payload: []byte("def")},
		)
		lnk := newScriptLink(t, tr)
		for _, want := range []string{"abc", "def"} {
			f, err := lnk.ReadFrame()
			require.NoError(t, err)
			assert.Equal(t, want, string(f.Payload()))
			// Mutating the released payload must not corrupt later reads since
			// each frame gets its own pooled slice.
			f.Release()
		}
	})

	t.Run("ReadPayloadInto", func(t *testing.T) {
		inner := newScriptTransport(FrameBytes{Header: dataHdr, Payload: []byte("xyz")})
		lnk := newScriptLink(t, &intoTransport{scriptTransport: inner})
		f, err := lnk.ReadFrame()
		require.NoError(t, err)
		assert.Equal(t, "xyz", string(f.Payload()))
		f.Release()
	})
}

func TestLinkLegacyWrite(t *testing.T) {
	tr := &scriptTransport{}
	lnk := newScriptLink(t, tr, Config{LegacyWire: true})
	f, err := wire.NewControl(wire.CmdOpenStream, wire.Addr{IP: 1}, wire.Addr{IP: 2}, []byte("x"))
	require.NoError(t, err)
	require.NoError(t, lnk.SendFrameSync(f))

	frames := tr.written()
	require.Len(t, frames, 1)
	// Legacy headers omit the encrypted-class bit.
	assert.Zero(t, frames[0].Header[0]&0x80)
	assert.Equal(t, byte(wire.CmdOpenStream), frames[0].Header[0])
}

func TestLinkWriteErrorClosesTransport(t *testing.T) {
	want := errBoom()
	tr := &scriptTransport{writeErr: want, closeCh: make(chan struct{})}
	lnk := newScriptLink(t, tr)
	f, err := wire.NewData(wire.Addr{IP: 1}, wire.Addr{IP: 2}, []byte("x"))
	require.NoError(t, err)
	assert.ErrorIs(t, lnk.SendFrameSync(f), want)
	select {
	case <-tr.closeCh:
	case <-time.After(time.Second):
		t.Fatal("transport not closed after write error")
	}
	// The writer is now stopped.
	f, err = wire.NewData(wire.Addr{IP: 1}, wire.Addr{IP: 2}, []byte("y"))
	require.NoError(t, err)
	assert.ErrorIs(t, lnk.SendFrameSync(f), ErrWriterClosed)
}

func TestLinkCloseReportsTransportError(t *testing.T) {
	want := errors.New("close boom")
	lnk := newScriptLink(t, &scriptTransport{closeErr: want})
	assert.ErrorIs(t, lnk.Close(), want)
	// Close is idempotent and returns the cached error.
	assert.ErrorIs(t, lnk.Close(), want)
}

func TestLinkSendAfterWriteFailureIsClosed(t *testing.T) {
	tr := &scriptTransport{writeErr: errBoom(), closeCh: make(chan struct{})}
	lnk := newScriptLink(t, tr)
	f, err := wire.NewControl(wire.CmdPingDomain, wire.Addr{IP: 1}, wire.Addr{IP: 2}, nil)
	require.NoError(t, err)
	require.Error(t, lnk.SendFrameSync(f))
	select {
	case <-tr.closeCh:
	case <-time.After(time.Second):
		t.Fatal("transport not closed")
	}
	assert.ErrorIs(t, lnk.SendControl(wire.CmdPingDomain, wire.Addr{IP: 1}, wire.Addr{IP: 2}, nil), ErrWriterClosed)
}
