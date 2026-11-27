package link

import (
	"bytes"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakePacketConn is a minimal packet.Conn used to exercise the legacy
// packetTransport adapter without a real network connection.
type fakePacketConn struct {
	readBuf     *packet.Buffer
	readErr     error
	readTimeout time.Duration

	writeTimeout time.Duration
	writes       []packet.Buffer
	writeErr     error

	raw      net.Conn
	closed   bool
	closeErr error
}

func (c *fakePacketConn) ReadBuffer() (*packet.Buffer, error) {
	if c.readErr != nil {
		return nil, c.readErr
	}
	b := c.readBuf
	c.readBuf = nil
	return b, nil
}

func (c *fakePacketConn) SetReadTimeout(d time.Duration) error {
	c.readTimeout = d
	return nil
}

func (c *fakePacketConn) WriteBuffer(b *packet.Buffer) error {
	if c.writeErr != nil {
		return c.writeErr
	}
	c.writes = append(c.writes, packet.Buffer{
		Head:    b.Head,
		Payload: append([]byte(nil), b.Payload...),
	})
	return nil
}

func (c *fakePacketConn) SetWriteTimeout(d time.Duration) { c.writeTimeout = d }
func (c *fakePacketConn) GetRawConn() net.Conn            { return c.raw }
func (c *fakePacketConn) Close() error {
	c.closed = true
	return c.closeErr
}

type stubNetConn struct{ local, remote net.Addr }

func (c *stubNetConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *stubNetConn) Write(b []byte) (int, error)      { return len(b), nil }
func (c *stubNetConn) Close() error                     { return nil }
func (c *stubNetConn) LocalAddr() net.Addr              { return c.local }
func (c *stubNetConn) RemoteAddr() net.Addr             { return c.remote }
func (c *stubNetConn) SetDeadline(time.Time) error      { return nil }
func (c *stubNetConn) SetReadDeadline(time.Time) error  { return nil }
func (c *stubNetConn) SetWriteDeadline(time.Time) error { return nil }

func newPacketBuffer(t *testing.T, cmd byte, src, dst wire.Addr, payload []byte) *packet.Buffer {
	t.Helper()
	b := packet.NewBuffer()
	b.SetHeader(cmd, dst.IP, dst.Port, src.IP, src.Port)
	require.NoError(t, b.SetPayload(payload))
	return b
}

func TestPacketTransportReadHeaderAndPayload(t *testing.T) {
	src := wire.Addr{IP: 1, Port: 1000}
	dst := wire.Addr{IP: 2, Port: 80}
	pc := &fakePacketConn{readBuf: newPacketBuffer(t, packet.CmdOpenStream, src, dst, []byte("abc"))}
	tr := NewPacketTransport(pc)

	var hdr [wire.HeaderSize]byte
	require.NoError(t, tr.ReadHeader(hdr[:]))
	assert.Equal(t, []byte{byte(wire.CmdOpenStream), 0, 2, 0, 80, 0, 1, 3, 232, 0, 3}, hdr[:])

	// A second header read before consuming the body must fail loudly instead
	// of silently dropping the pending payload.
	assert.ErrorIs(t, tr.ReadHeader(hdr[:]), ErrFrameNotConsumed)

	payload, err := tr.ReadPayload(3)
	require.NoError(t, err)
	assert.Equal(t, []byte("abc"), payload)
	// A payload read without a preceding header is a framing error.
	_, err = tr.ReadPayload(1)
	assert.ErrorIs(t, err, ErrShortFrame)
}

func TestPacketTransportReadPayload(t *testing.T) {
	src := wire.Addr{IP: 1, Port: 1000}
	dst := wire.Addr{IP: 2, Port: 80}

	t.Run("size mismatch", func(t *testing.T) {
		pc := &fakePacketConn{readBuf: newPacketBuffer(t, packet.CmdOpenStream, src, dst, []byte("abcd"))}
		tr := NewPacketTransport(pc)
		var hdr [wire.HeaderSize]byte
		require.NoError(t, tr.ReadHeader(hdr[:]))
		_, err := tr.ReadPayload(3)
		assert.ErrorIs(t, err, ErrFrameSizeMismatch)
	})

	t.Run("empty payload", func(t *testing.T) {
		pc := &fakePacketConn{readBuf: newPacketBuffer(t, packet.CmdCloseStream, src, dst, nil)}
		tr := NewPacketTransport(pc)
		var hdr [wire.HeaderSize]byte
		require.NoError(t, tr.ReadHeader(hdr[:]))
		_, err := tr.ReadPayload(0)
		assert.NoError(t, err)
	})

	t.Run("read buffer error", func(t *testing.T) {
		errBoom := errors.New("boom")
		tr := NewPacketTransport(&fakePacketConn{readErr: errBoom})
		var hdr [wire.HeaderSize]byte
		assert.ErrorIs(t, tr.ReadHeader(hdr[:]), errBoom)
	})
}

func TestPacketTransportReadPayloadInto(t *testing.T) {
	src := wire.Addr{IP: 1, Port: 1000}
	dst := wire.Addr{IP: 2, Port: 80}

	t.Run("success", func(t *testing.T) {
		pc := &fakePacketConn{readBuf: newPacketBuffer(t, packet.CmdOpenStream, src, dst, []byte("abc"))}
		tr := NewPacketTransport(pc).(*packetTransport)
		var hdr [wire.HeaderSize]byte
		require.NoError(t, tr.ReadHeader(hdr[:]))
		dstBuf := make([]byte, 3)
		require.NoError(t, tr.ReadPayloadInto(dstBuf))
		assert.Equal(t, []byte("abc"), dstBuf)
	})

	t.Run("without header", func(t *testing.T) {
		tr := NewPacketTransport(&fakePacketConn{}).(*packetTransport)
		assert.ErrorIs(t, tr.ReadPayloadInto(make([]byte, 1)), ErrShortFrame)
	})

	t.Run("size mismatch", func(t *testing.T) {
		pc := &fakePacketConn{readBuf: newPacketBuffer(t, packet.CmdOpenStream, src, dst, []byte("abcd"))}
		tr := NewPacketTransport(pc).(*packetTransport)
		var hdr [wire.HeaderSize]byte
		require.NoError(t, tr.ReadHeader(hdr[:]))
		assert.ErrorIs(t, tr.ReadPayloadInto(make([]byte, 2)), ErrFrameSizeMismatch)
	})
}

func TestPacketTransportWriteFrames(t *testing.T) {
	src := wire.Addr{IP: 1, Port: 1000}
	dst := wire.Addr{IP: 2, Port: 80}
	pc := &fakePacketConn{}
	tr := NewPacketTransport(pc)

	hdr := encodeHeader(t, wire.Header{
		Class: wire.ClassControl, Cmd: wire.Cmd(wire.CmdOpenStream), Src: src, Dst: dst, Size: 3,
	})
	require.NoError(t, tr.WriteFrames([]FrameBytes{
		{Header: hdr, Payload: []byte("one")},
		{Header: hdr, Payload: []byte("two")},
	}))
	require.Len(t, pc.writes, 2)
	assert.True(t, bytes.Equal(pc.writes[0].Payload, []byte("one")))
	assert.True(t, bytes.Equal(pc.writes[1].Payload, []byte("two")))

	// Write error is returned as-is.
	errBoom := errors.New("write boom")
	pc.writeErr = errBoom
	assert.ErrorIs(t, tr.WriteFrames([]FrameBytes{{Header: hdr, Payload: []byte("x")}}), errBoom)
}

func TestPacketTransportDeadlines(t *testing.T) {
	pc := &fakePacketConn{}
	tr := NewPacketTransport(pc)

	require.NoError(t, tr.SetReadDeadline(time.Time{}))
	assert.Zero(t, pc.readTimeout)
	require.NoError(t, tr.SetWriteDeadline(time.Time{}))
	assert.Zero(t, pc.writeTimeout)

	future := time.Now().Add(time.Hour)
	require.NoError(t, tr.SetReadDeadline(future))
	assert.Positive(t, pc.readTimeout)
	assert.LessOrEqual(t, pc.readTimeout, time.Hour)
	require.NoError(t, tr.SetWriteDeadline(future))
	assert.Positive(t, pc.writeTimeout)

	past := time.Now().Add(-time.Hour)
	require.NoError(t, tr.SetReadDeadline(past))
	assert.Negative(t, pc.readTimeout)

	assert.Zero(t, until(time.Time{}))
	assert.Negative(t, until(past))
	assert.Positive(t, until(future))
}

func TestPacketTransportAddrs(t *testing.T) {
	tr := NewPacketTransport(&fakePacketConn{})
	assert.Nil(t, tr.LocalAddr())
	assert.Nil(t, tr.RemoteAddr())

	local := &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1111}
	remote := &net.TCPAddr{IP: net.IPv4(10, 0, 0, 1), Port: 2222}
	tr = NewPacketTransport(&fakePacketConn{raw: &stubNetConn{local: local, remote: remote}})
	assert.Equal(t, local.String(), tr.LocalAddr().String())
	assert.Equal(t, remote.String(), tr.RemoteAddr().String())
}

func TestPacketTransportClose(t *testing.T) {
	src := wire.Addr{IP: 1, Port: 1000}
	dst := wire.Addr{IP: 2, Port: 80}

	t.Run("releases pending", func(t *testing.T) {
		pc := &fakePacketConn{readBuf: newPacketBuffer(t, packet.CmdOpenStream, src, dst, []byte("abc"))}
		tr := NewPacketTransport(pc).(*packetTransport)
		var hdr [wire.HeaderSize]byte
		require.NoError(t, tr.ReadHeader(hdr[:]))
		require.NotNil(t, tr.pending)
		require.NoError(t, tr.Close())
		assert.Nil(t, tr.pending)
		assert.True(t, pc.closed)
	})

	t.Run("returns close error", func(t *testing.T) {
		errBoom := errors.New("close boom")
		pc := &fakePacketConn{closeErr: errBoom}
		tr := NewPacketTransport(pc)
		assert.ErrorIs(t, tr.Close(), errBoom)
	})
}
