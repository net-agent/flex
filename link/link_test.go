package link

import (
	"bytes"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/wire"
)

func newPipeLinks(t *testing.T) (*Link, *Link) {
	t.Helper()
	c1, c2 := net.Pipe()
	a := NewDirect(NewTCP(c1))
	b := NewDirect(NewTCP(c2))
	t.Cleanup(func() {
		_ = a.Close()
		_ = b.Close()
	})
	return a, b
}

func TestLinkControlRoundTrip(t *testing.T) {
	a, b := newPipeLinks(t)
	src := wire.Addr{IP: 1, Port: 1000}
	dst := wire.Addr{IP: 2, Port: 80}

	f, err := wire.NewControl(wire.CmdOpenStream, src, dst, []byte("example"))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.SendFrame(f); err != nil {
		t.Fatal(err)
	}

	got, err := b.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	defer got.Release()

	if got.Class() != wire.ClassControl ||
		got.Cmd() != wire.Cmd(wire.CmdOpenStream) ||
		got.Src() != src || got.Dst() != dst ||
		!bytes.Equal(got.Payload(), []byte("example")) {
		t.Fatalf("frame=%s", got)
	}
}

func TestLinkDataRoundTrip(t *testing.T) {
	a, b := newPipeLinks(t)
	src := wire.Addr{IP: 1, Port: 1000}
	dst := wire.Addr{IP: 2, Port: 80}
	payload := []byte{0x00, 0x01, 0xfe, 0xff, 'h', 'i'}

	if err := a.SendData(src, dst, payload); err != nil {
		t.Fatal(err)
	}
	got, err := b.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	defer got.Release()

	if got.Class() != wire.ClassData || !bytes.Equal(got.Payload(), payload) {
		t.Fatalf("frame=%s payload=%x", got, got.Payload())
	}
}

func TestLinkAckRoundTrip(t *testing.T) {
	a, b := newPipeLinks(t)
	src := wire.Addr{IP: 2, Port: 80}
	dst := wire.Addr{IP: 1, Port: 1000}

	if err := a.SendAck(4096, src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := b.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	defer got.Release()

	if got.Cmd() != wire.Cmd(wire.AckPushStreamData) || got.AckSize() != 4096 || got.PayloadSize() != 0 {
		t.Fatalf("frame=%s ack=%d", got, got.AckSize())
	}
}

func TestLinkCloseUnblocksReader(t *testing.T) {
	a, b := newPipeLinks(t)
	if err := b.SetReadTimeout(500 * time.Millisecond); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ReadFrame(); err == nil {
		t.Fatal("expected read error after peer close")
	}
}

func TestLinkSendAfterClose(t *testing.T) {
	a, _ := newPipeLinks(t)
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.SendData(wire.Addr{IP: 1}, wire.Addr{IP: 2}, []byte("x")); !errors.Is(err, ErrWriterClosed) {
		t.Fatalf("err=%v", err)
	}
}

// captureTransport records encoded batches and copies their payloads so the
// test can inspect them after the link releases the frames.
type captureTransport struct {
	ch     chan []FrameBytes
	closed chan struct{}
	once   sync.Once
}

func newCaptureTransport() *captureTransport {
	return &captureTransport{
		ch:     make(chan []FrameBytes, 8),
		closed: make(chan struct{}),
	}
}

func (c *captureTransport) ReadHeader([]byte) error {
	<-c.closed
	return io.EOF
}

func (c *captureTransport) ReadPayload(int) ([]byte, error) { return nil, io.EOF }

func (c *captureTransport) WriteFrames(frames []FrameBytes) error {
	out := make([]FrameBytes, len(frames))
	for i, f := range frames {
		out[i].Header = f.Header
		out[i].Payload = append([]byte(nil), f.Payload...)
	}
	c.ch <- out
	return nil
}

func (c *captureTransport) SetReadDeadline(time.Time) error  { return nil }
func (c *captureTransport) SetWriteDeadline(time.Time) error { return nil }
func (c *captureTransport) LocalAddr() net.Addr              { return nil }
func (c *captureTransport) RemoteAddr() net.Addr             { return nil }

func (c *captureTransport) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func TestSendDataCopiesPayload(t *testing.T) {
	ct := newCaptureTransport()
	l := NewDirect(ct)
	defer l.Close()

	payload := []byte("hello")
	if err := l.SendData(wire.Addr{IP: 1}, wire.Addr{IP: 2}, payload); err != nil {
		t.Fatal(err)
	}
	payload[0] = 'X' // caller reuse must not affect the queued frame

	select {
	case batch := <-ct.ch:
		if len(batch) != 1 || !bytes.Equal(batch[0].Payload, []byte("hello")) {
			t.Fatalf("captured=%q", batch[0].Payload)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for write")
	}
}

func TestFairWriterPreservesPerStreamOrder(t *testing.T) {
	ct := newCaptureTransport()
	l := NewDirect(ct)
	defer l.Close()

	aSrc, aDst := wire.Addr{IP: 1, Port: 1000}, wire.Addr{IP: 2, Port: 80}
	bSrc, bDst := wire.Addr{IP: 3, Port: 1001}, wire.Addr{IP: 4, Port: 443}

	for _, p := range []string{"a1", "a2", "a3"} {
		if err := l.SendData(aSrc, aDst, []byte(p)); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"b1", "b2", "b3"} {
		if err := l.SendData(bSrc, bDst, []byte(p)); err != nil {
			t.Fatal(err)
		}
	}

	var aSeen, bSeen []string
	deadline := time.After(2 * time.Second)
	for len(aSeen)+len(bSeen) < 6 {
		select {
		case batch := <-ct.ch:
			for _, f := range batch {
				h, err := (wire.Codec{}).DecodeHeader(f.Header[:])
				if err != nil {
					t.Fatal(err)
				}
				if h.Src.IP == 1 {
					aSeen = append(aSeen, string(f.Payload))
				} else {
					bSeen = append(bSeen, string(f.Payload))
				}
			}
		case <-deadline:
			t.Fatalf("timeout: a=%v b=%v", aSeen, bSeen)
		}
	}
	if got := strings.Join(aSeen, ""); got != "a1a2a3" {
		t.Fatalf("stream A order=%v", aSeen)
	}
	if got := strings.Join(bSeen, ""); got != "b1b2b3" {
		t.Fatalf("stream B order=%v", bSeen)
	}
}
