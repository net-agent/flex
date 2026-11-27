package node

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/link"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/wire"
)

// tappedFrame is a plain-text copy of a frame observed at the link boundary.
// Copies are used because the link owns (and recycles) the frame after the tap
// returns.
type tappedFrame struct {
	class   wire.Class
	cmd     wire.Cmd
	src     wire.Addr
	dst     wire.Addr
	payload []byte
	ackSize uint16
}

// captureTransport is a link.Transport test double: outbound frames are
// accepted and dropped, inbound reads block until Close. It lets tests drive a
// Node without a real peer while still exercising the link write path.
type captureTransport struct {
	mu     sync.Mutex
	closed chan struct{}
	once   sync.Once
}

func newCaptureTransport() *captureTransport {
	return &captureTransport{closed: make(chan struct{})}
}

func (t *captureTransport) ReadHeader(dst []byte) error {
	<-t.closed
	return io.EOF
}

func (t *captureTransport) ReadPayload(n int) ([]byte, error) { return nil, io.EOF }

func (t *captureTransport) WriteFrames(frames []link.FrameBytes) error { return nil }

func (t *captureTransport) SetReadDeadline(deadline time.Time) error { return nil }

func (t *captureTransport) SetWriteDeadline(deadline time.Time) error { return nil }

func (t *captureTransport) LocalAddr() net.Addr  { return nil }
func (t *captureTransport) RemoteAddr() net.Addr { return nil }

func (t *captureTransport) Close() error {
	t.once.Do(func() { close(t.closed) })
	return nil
}

// newTappedNode builds a node whose outbound frames are captured by a
// FrameTap. No Serve loop runs, so callers drive handlers directly.
func newTappedNode(t *testing.T) (*Node, <-chan tappedFrame) {
	t.Helper()
	tr := newCaptureTransport()
	n := NewFromLink(link.NewDirect(tr))
	n.SetIP(1)
	n.SetDomain("tap-node")
	frames := make(chan tappedFrame, 64)
	n.Link().SetFrameTap(func(write bool, f *wire.Frame) {
		if !write {
			return
		}
		frames <- tappedFrame{
			class:   f.Class(),
			cmd:     f.Cmd(),
			src:     f.Src(),
			dst:     f.Dst(),
			payload: append([]byte(nil), f.Payload()...),
			ackSize: f.AckSize(),
		}
	})
	t.Cleanup(func() { _ = n.Close() })
	return n, frames
}

// requireFrame waits for the next outbound frame.
func requireFrame(t *testing.T, ch <-chan tappedFrame) tappedFrame {
	t.Helper()
	select {
	case f := <-ch:
		return f
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for outbound frame")
		return tappedFrame{}
	}
}

// requireNoFrame asserts no frame has been emitted yet.
func requireNoFrame(t *testing.T, ch <-chan tappedFrame) {
	t.Helper()
	select {
	case f := <-ch:
		t.Fatalf("unexpected outbound frame: %+v", f)
	default:
	}
}

// panicTransport panics on the first read. Serve must recover it.
type panicTransport struct{}

func (panicTransport) ReadHeader(dst []byte) error       { panic("read boom") }
func (panicTransport) ReadPayload(n int) ([]byte, error) { panic("read boom") }
func (panicTransport) WriteFrames(frames []link.FrameBytes) error {
	return nil
}
func (panicTransport) SetReadDeadline(deadline time.Time) error  { return nil }
func (panicTransport) SetWriteDeadline(deadline time.Time) error { return nil }
func (panicTransport) LocalAddr() net.Addr                       { return nil }
func (panicTransport) RemoteAddr() net.Addr                      { return nil }
func (panicTransport) Close() error                              { return nil }

// failingTransportConn implements both packet.Conn and link.Transport but
// fails every read immediately. It reaches the Serve handshake-failure branch
// without real network I/O, so it is safe inside a synctest bubble.
type failingTransportConn struct {
	packet.Conn
}

func (failingTransportConn) ReadHeader(dst []byte) error { return io.EOF }
func (failingTransportConn) ReadPayload(n int) ([]byte, error) {
	return nil, io.EOF
}
func (failingTransportConn) WriteFrames(frames []link.FrameBytes) error { return nil }
func (failingTransportConn) SetReadDeadline(deadline time.Time) error   { return nil }
func (failingTransportConn) SetWriteDeadline(deadline time.Time) error  { return nil }
func (failingTransportConn) LocalAddr() net.Addr                        { return nil }
func (failingTransportConn) RemoteAddr() net.Addr                       { return nil }
func (failingTransportConn) Close() error                               { return nil }
