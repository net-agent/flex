package stream

import (
	"errors"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type captureEndpoint struct {
	frames []*wire.Frame
}

func (c *captureEndpoint) SendFrame(f *wire.Frame) error {
	// Keep the frames alive; release in test cleanup.
	c.frames = append(c.frames, f)
	return nil
}

func TestEndpointConstructors(t *testing.T) {
	ep := &captureEndpoint{}
	s := NewWithEndpoint(ep, 0)
	require.NotNil(t, s)
	assert.Equal(t, DefaultWindowSize, s.GetWindowAvailable())

	s = NewDialStreamWithEndpoint(ep, "a", 1, 1000, "b", 2, 80, 4096)
	assert.Equal(t, DirectionOutbound, s.GetState().Direction)
	assert.Equal(t, "b", s.GetState().RemoteDomain)
	assert.Equal(t, uint16(1), s.GetState().LocalAddr.IP)
	assert.Equal(t, uint16(80), s.GetState().RemoteAddr.Port)
	assert.Equal(t, int32(4096), s.GetWindowAvailable())

	s = NewAcceptStreamWithEndpoint(ep, "b", 2, 80, "a", 1, 1000, 2048)
	assert.Equal(t, DirectionInbound, s.GetState().Direction)
	assert.Equal(t, "a", s.GetState().RemoteDomain)
}

func TestSetLoggerAndWindowAvailable(t *testing.T) {
	s := New(&mockWriter{}, 128)
	s.SetLogger(nil) // must not panic or clear the logger
	require.NotNil(t, s.logger)
	s.SetLogger(slog.New(slog.DiscardHandler))
	require.NotNil(t, s.acks.logger)
	assert.Equal(t, int32(128), s.GetWindowAvailable())
}

func TestLegacyEndpointNilWriter(t *testing.T) {
	released := 0
	f, err := wire.NewFrameWithRelease(wire.ClassControl, wire.Cmd(wire.CmdPingDomain),
		wire.Addr{IP: 1}, wire.Addr{IP: 2}, nil, 0, func([]byte) { released++ })
	require.NoError(t, err)

	err = legacyEndpoint{}.SendFrame(f)
	require.ErrorIs(t, err, ErrNilWriter)
	assert.Equal(t, 1, released, "SendFrame must release the frame even on error")
}

func TestLegacyEndpointStripsClassBit(t *testing.T) {
	w := &recordingWriter{}
	s := New(w, 1024)
	require.NoError(t, s.SetWriteDeadline(deadlineSoon()))
	_, err := s.Write([]byte("payload"))
	require.NoError(t, err)

	require.Eventually(t, func() bool { return w.count() > 0 }, 2*time.Second, 10*time.Millisecond)
	w.mu.Lock()
	head := w.bufs[0]
	w.mu.Unlock()
	assert.Equal(t, byte(packet.CmdPushStreamData), head[0]&^0x80)
	assert.Zero(t, head[0]&0x80, "legacy endpoint must strip the class bit")
}

type fakeSenderEndpoint struct {
	frames []*wire.Frame
}

func (f *fakeSenderEndpoint) SendFrame(frame *wire.Frame) error {
	f.frames = append(f.frames, frame)
	return nil
}

func TestSenderEndpointOperations(t *testing.T) {
	ep := &fakeSenderEndpoint{}
	var counter int32
	sn := newSenderWithEndpoint(ep, &counter)
	sn.SetSrc(1, 1000)
	sn.SetDst(2, 80)

	require.NoError(t, sn.SendData(nil))
	assert.Empty(t, ep.frames, "empty payload must not allocate a frame")

	require.ErrorIs(t, sn.SendData(make([]byte, packet.MaxPayloadSize+1)), ErrSendDataOversize)
	assert.Empty(t, ep.frames)

	require.NoError(t, sn.SendData([]byte("hello")))
	require.Len(t, ep.frames, 1)
	assert.Equal(t, wire.Cmd(wire.CmdPushStreamData), wire.Cmd(ep.frames[0].Cmd()))
	assert.Equal(t, wire.Addr{IP: 1, Port: 1000}, ep.frames[0].Src())
	assert.Equal(t, wire.Addr{IP: 2, Port: 80}, ep.frames[0].Dst())

	require.NoError(t, sn.SendDataAck(4096))
	require.Len(t, ep.frames, 2)
	assert.Equal(t, wire.Cmd(wire.AckPushStreamData), ep.frames[1].Cmd())
	assert.Equal(t, uint16(4096), ep.frames[1].AckSize())

	require.NoError(t, sn.SendClose())
	require.NoError(t, sn.SendCloseAck())
	require.Len(t, ep.frames, 4)
	assert.Equal(t, wire.Cmd(wire.CmdCloseStream), ep.frames[2].Cmd())
	assert.Equal(t, wire.Cmd(wire.AckCloseStream), ep.frames[3].Cmd())
	assert.Equal(t, int32(4), counter)

	sn.InterruptWrite() // intentionally a no-op
	assert.False(t, sn.CanInterruptWrite())
	for _, f := range ep.frames {
		f.Release()
	}
}

func TestTimeoutErrorContract(t *testing.T) {
	require.ErrorIs(t, ErrTimeout, ErrTimeout)
	assert.Equal(t, "i/o timeout", ErrTimeout.Error())
	ne, ok := ErrTimeout.(interface {
		Timeout() bool
		Temporary() bool
	})
	require.True(t, ok)
	assert.True(t, ne.Timeout())
	assert.True(t, ne.Temporary())
}

func TestSetOnDetachOnce(t *testing.T) {
	s := New(&mockWriter{}, 0)
	calls := 0
	s.SetOnDetach(func() { calls++ })
	s.SetOnDetach(nil) // nil must be ignored
	require.NoError(t, s.CloseRead())
	assert.Zero(t, calls, "one direction closed: no detach yet")
	require.NoError(t, s.CloseWrite())
	assert.Equal(t, 1, calls)

	// Detach is one-shot: later registrations must not fire again.
	s.SetOnDetach(func() { calls++ })
	assert.Equal(t, 1, calls)
	s.SetOnDetach(func() { calls++ })
	assert.Equal(t, 1, calls)
}

func TestLegacyHandlerAdapters(t *testing.T) {
	// nil buffers are ignored by every legacy adapter; use a fresh stream for
	// the close adapters because HandleClose closes the receive side.
	nilStream := New(&mockWriter{}, 0)
	nilStream.HandleCmdPushStreamData(nil)
	nilStream.HandleAckPushStreamData(nil)
	nilStream.HandleCmdCloseStream(nil)
	nilStream.HandleAckCloseStream(nil)

	s := New(&mockWriter{}, 0)
	buf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
	require.NoError(t, buf.SetPayload([]byte("abc")))
	s.HandleCmdPushStreamData(buf)
	assert.Equal(t, 3, s.recv.pending())
	assert.Equal(t, int32(1), s.GetState().RecvBufferCount)

	ack := packet.NewBufferWithCmd(packet.AckPushStreamData)
	ack.SetHeader(packet.AckPushStreamData, 0, 0, 0, 0)
	ack.SetDataACKSize(16)
	s.HandleAckPushStreamData(ack)

	ackStream := New(&mockWriter{}, 0)
	ackClose := packet.NewBufferWithCmd(packet.AckCloseStream)
	ackStream.HandleAckCloseStream(ackClose)
	select {
	case <-ackStream.closeAckCh:
	default:
		t.Fatal("AckCloseStream adapter must signal closeAckCh")
	}
}

func TestRecvBufferCoalesceBoundary(t *testing.T) {
	for _, n := range []int{1023, 1024, 2048} {
		b := newRecvBuffer(0)
		require.NoError(t, b.append(frameWithPayload(make([]byte, n))))
		got := make([]byte, n)
		read, err := b.read(got)
		require.NoError(t, err)
		assert.Equal(t, n, read)
		b.discard()
	}
}

func frameWithPayload(p []byte) *wire.Frame {
	f, err := wire.NewData(wire.Addr{IP: 1}, wire.Addr{IP: 2}, p)
	if err != nil {
		panic(err)
	}
	return f
}

func deadlineSoon() time.Time { return time.Now().Add(time.Second) }

type failingEndpoint struct{ err error }

func (f *failingEndpoint) SendFrame(frame *wire.Frame) error {
	frame.Release()
	return f.err
}

func TestSenderErrorPropagation(t *testing.T) {
	ep := &failingEndpoint{err: errors.New("endpoint down")}
	sn := newSenderWithEndpoint(ep, nil)
	require.ErrorIs(t, sn.SendData([]byte("x")), ep.err)
	require.ErrorIs(t, sn.SendDataAck(1), ep.err)
	require.ErrorIs(t, sn.SendClose(), ep.err)
	require.ErrorIs(t, sn.SendCloseAck(), ep.err)
}

func TestStreamWriteAndCloseError(t *testing.T) {
	ep := &failingEndpoint{err: errors.New("endpoint down")}
	s := NewWithEndpoint(ep, 1024)
	_, err := s.Write([]byte("data"))
	require.ErrorIs(t, err, ep.err)

	s = NewWithEndpoint(ep, 1024)
	require.ErrorIs(t, s.Close(), ep.err)
	// Close is not fully established because SendClose failed; CloseWrite is
	// already set, so a second Closes must be idempotent per net.Conn.
	require.ErrorIs(t, s.Close(), net.ErrClosed)
}

func TestStreamCloseReadTwice(t *testing.T) {
	s := New(&mockWriter{}, 0)
	require.NoError(t, s.CloseRead())
	require.ErrorIs(t, s.CloseRead(), ErrReaderIsClosed)
}

func TestHandlerOversizePayloadDropped(t *testing.T) {
	s := New(&mockWriter{}, 0)
	buf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
	buf.Payload = make([]byte, packet.MaxPayloadSize+1)
	s.HandleCmdPushStreamData(buf)
	assert.Zero(t, s.recv.pending())
}

func TestHandlerCloseAdaptersNonNil(t *testing.T) {
	s := New(&mockWriter{}, 0)
	s.HandleCmdCloseStream(packet.NewBufferWithCmd(packet.CmdCloseStream))
	assert.True(t, s.GetState().IsClosed)
}

func TestRecvBufferClosedState(t *testing.T) {
	b := newRecvBuffer(0)
	f := frameWithPayload([]byte("x"))
	require.NoError(t, b.append(f))
	require.True(t, b.close())
	require.False(t, b.close())

	late := frameWithPayload([]byte("y"))
	require.ErrorIs(t, b.append(late), errRecvClosed)
	late.Release()
}

func TestAckTrackerNilLoggerAndStopIdempotent(t *testing.T) {
	a := newAckTracker(func(uint16) error { return nil }, nil, nil)
	a.add(0)
	a.add(-1)
	a.add(1)
	a.stop()
	a.stop() // idempotent
	assert.Zero(t, a.pendingBytes())
}

func TestAckTrackerFlushSendError(t *testing.T) {
	sendErr := errors.New("ack send failed")
	a := newAckTracker(func(uint16) error { return sendErr }, nil, slog.New(slog.DiscardHandler))
	a.add(1)
	a.flush() // must return without panicking; logger is discarded
	assert.Zero(t, a.pendingBytes())
}

func TestDeadlineHelpers(t *testing.T) {
	s := New(&mockWriter{}, 0)
	assert.False(t, s.isReadDeadlineExceeded())
	assert.False(t, s.isWriteDeadlineExceeded())

	s.readDeadline.Set(time.Now().Add(-time.Second))
	s.writeDeadline.Set(time.Now().Add(-time.Second))
	assert.True(t, s.isReadDeadlineExceeded())
	assert.True(t, s.isWriteDeadlineExceeded())

	s.readDeadline.Set(time.Time{})
	s.writeDeadline.Set(time.Time{})
	assert.False(t, s.isReadDeadlineExceeded())
	assert.False(t, s.isWriteDeadlineExceeded())
}

type timeoutSenderError struct{}

func (timeoutSenderError) Error() string   { return "timeout" }
func (timeoutSenderError) Timeout() bool   { return true }
func (timeoutSenderError) Temporary() bool { return true }

func TestWriteTransportTimeoutMapsToErrTimeout(t *testing.T) {
	ep := &failingEndpoint{err: timeoutSenderError{}}
	s := NewWithEndpoint(ep, 1024)
	_, err := s.Write([]byte("x"))
	require.ErrorIs(t, err, ErrTimeout)
}

func TestWritePastDeadlineReturnsErrTimeout(t *testing.T) {
	s := New(&mockWriter{}, 1024)
	require.NoError(t, s.SetWriteDeadline(time.Now().Add(-time.Second)))
	_, err := s.Write([]byte("x"))
	require.ErrorIs(t, err, ErrTimeout)
}

func TestAckFlushSplitsOversizePending(t *testing.T) {
	var sizes []int
	a := newAckTracker(func(n uint16) error {
		sizes = append(sizes, int(n))
		return nil
	}, nil, slog.New(slog.DiscardHandler))
	a.add(70000)
	a.flush()
	require.Equal(t, []int{65535, 4465}, sizes)
	assert.Zero(t, a.pendingBytes())
}

func TestRecvBufferEmptyPayload(t *testing.T) {
	b := newRecvBuffer(0)
	f := frameWithPayload(nil)
	require.ErrorIs(t, b.append(f), errEmptyPayload)
	f.Release()
}
