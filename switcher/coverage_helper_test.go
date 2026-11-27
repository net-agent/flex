package switcher

import (
	"io"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/net-agent/flex/v4/link"
	"github.com/net-agent/flex/v4/packet"
	"github.com/stretchr/testify/require"
)

// testTextAddr is a net.Addr whose String is not a host:port pair. It lets
// tests drive remoteIP's SplitHostPort error branch deterministically.
type testTextAddr string

func (a testTextAddr) Network() string { return "test" }
func (a testTextAddr) String() string  { return string(a) }

// testRawConn is a net.Conn with injectable addresses. Only the methods used by
// the code under test do real work.
type testRawConn struct {
	local  net.Addr
	remote net.Addr
}

func (c *testRawConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *testRawConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *testRawConn) Close() error                     { return nil }
func (c *testRawConn) LocalAddr() net.Addr              { return c.local }
func (c *testRawConn) RemoteAddr() net.Addr             { return c.remote }
func (c *testRawConn) SetDeadline(time.Time) error      { return nil }
func (c *testRawConn) SetReadDeadline(time.Time) error  { return nil }
func (c *testRawConn) SetWriteDeadline(time.Time) error { return nil }

// testPacketWrite is a copy of what a link wrote through packet.Conn.
type testPacketWrite struct {
	head    [packet.HeaderSz]byte
	payload []byte
}

// testPacketConn is a packet.Conn that deliberately does NOT implement
// link.Transport, so it exercises the legacy adapter paths.
type testPacketConn struct {
	raw        net.Conn
	closeCount atomic.Int32
	readCount  atomic.Int32
	writes     chan testPacketWrite
}

func newTestPacketConn(raw net.Conn) *testPacketConn {
	return &testPacketConn{
		raw:    raw,
		writes: make(chan testPacketWrite, 8),
	}
}

func (c *testPacketConn) GetRawConn() net.Conn { return c.raw }

func (c *testPacketConn) ReadBuffer() (*packet.Buffer, error) {
	c.readCount.Add(1)
	return nil, io.EOF
}

func (c *testPacketConn) SetReadTimeout(time.Duration) error { return nil }

func (c *testPacketConn) WriteBuffer(buf *packet.Buffer) error {
	if buf == nil {
		return nil
	}
	select {
	case c.writes <- testPacketWrite{head: buf.Head, payload: append([]byte(nil), buf.Payload...)}:
	default:
	}
	return nil
}

func (c *testPacketConn) SetWriteTimeout(time.Duration) {}

func (c *testPacketConn) Close() error {
	c.closeCount.Add(1)
	return nil
}

// panicGetRawConn panics on GetRawConn to exercise ServeConn's recover guard.
type panicGetRawConn struct {
	*testPacketConn
}

func (c *panicGetRawConn) GetRawConn() net.Conn {
	panic("forced panic in GetRawConn")
}

// testAcceptErrListener returns a fixed error from Accept without ever being
// closed, covering Serve's "accept failed while not shutting down" branch.
type testAcceptErrListener struct {
	err        error
	closeCount atomic.Int32
}

func (l *testAcceptErrListener) Accept() (net.Conn, error) { return nil, l.err }
func (l *testAcceptErrListener) Close() error {
	l.closeCount.Add(1)
	return nil
}
func (l *testAcceptErrListener) Addr() net.Addr {
	return &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}
}

// newBrokenContext returns a Context whose link has already been closed. The
// handshake window is open, so SendFrame attempts a write and fails with
// link.ErrWriterClosed; Stats.BytesSent proves a reply was constructed.
func newBrokenContext(t *testing.T, id int, domain string, ip uint16) *Context {
	t.Helper()
	serverConn, peerConn := packet.Pipe()
	lnk := link.NewDirectWithConfig(linkTransport(serverConn), link.Config{LegacyWire: true})
	ctx := newContext(id, lnk, domain, "", nil)
	ctx.IP = ip
	ctx.markReady()
	require.NoError(t, lnk.Close())
	t.Cleanup(func() {
		_ = peerConn.Close()
		ctx.release()
	})
	return ctx
}

// newContextWithPeerLink builds a plaintext (legacy wire) link pair and returns
// the ctx owning one end plus the peer link. Both ends are closed by cleanup.
func newContextWithPeerLink(t *testing.T, id int, domain string, ip uint16) (*Context, *link.Link) {
	t.Helper()
	serverConn, peerConn := packet.Pipe()
	lnk := link.NewDirectWithConfig(linkTransport(serverConn), link.Config{LegacyWire: true})
	ctx := newContext(id, lnk, domain, "", nil)
	ctx.IP = ip
	ctx.markReady()
	peer := link.NewDirectWithConfig(linkTransport(peerConn), link.Config{LegacyWire: true})
	t.Cleanup(func() {
		_ = peer.Close()
		ctx.release()
	})
	return ctx, peer
}
