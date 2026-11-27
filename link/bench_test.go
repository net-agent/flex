package link

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/net-agent/flex/v4/wire"
)

// nopTransport discards writes and never returns data. It isolates the link's
// framing/crypto cost from the kernel/pipe.
type nopTransport struct {
	frames int
	bytes  int64
}

func (n *nopTransport) ReadHeader([]byte) error         { return io.EOF }
func (n *nopTransport) ReadPayload(int) ([]byte, error) { return nil, io.EOF }
func (n *nopTransport) WriteFrames(frames []FrameBytes) error {
	n.frames += len(frames)
	for _, f := range frames {
		n.bytes += int64(len(f.Payload))
	}
	return nil
}
func (n *nopTransport) SetReadDeadline(time.Time) error  { return nil }
func (n *nopTransport) SetWriteDeadline(time.Time) error { return nil }
func (n *nopTransport) LocalAddr() net.Addr              { return nil }
func (n *nopTransport) RemoteAddr() net.Addr             { return nil }
func (n *nopTransport) Close() error                     { return nil }

func benchSecureLink(b *testing.B, payload []byte, control bool) {
	b.Helper()
	tr := &nopTransport{}
	l, err := NewSecure(tr, make([]byte, linkKeyLen), true, Config{})
	if err != nil {
		b.Fatal(err)
	}
	defer l.Close()

	src := wire.Addr{IP: 1, Port: 1000}
	dst := wire.Addr{IP: 2, Port: 80}
	var f *wire.Frame
	if control {
		f, err = wire.NewControl(wire.CmdOpenStream, src, dst, payload)
	} else {
		f, err = wire.NewData(src, dst, payload)
	}
	if err != nil {
		b.Fatal(err)
	}
	defer f.Release()

	b.SetBytes(int64(len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := l.writeSecureFrames([]*wire.Frame{f}); err != nil {
			b.Fatal(err)
		}
	}
	if tr.frames != b.N {
		b.Fatalf("frames=%d want=%d", tr.frames, b.N)
	}
}

func BenchmarkSecureDataFrame64B(b *testing.B)    { benchSecureLink(b, make([]byte, 64), false) }
func BenchmarkSecureDataFrame16K(b *testing.B)    { benchSecureLink(b, make([]byte, 16*1024), false) }
func BenchmarkSecureControlFrame32B(b *testing.B) { benchSecureLink(b, make([]byte, 32), true) }
func BenchmarkSecureControlFrame1K(b *testing.B)  { benchSecureLink(b, make([]byte, 1024), true) }

func BenchmarkWireCodecHeader(b *testing.B) {
	var hdr [wire.HeaderSize]byte
	h := wire.Header{
		Class: wire.ClassData,
		Cmd:   wire.Cmd(wire.CmdPushStreamData),
		Src:   wire.Addr{IP: 1, Port: 1000},
		Dst:   wire.Addr{IP: 2, Port: 80},
		Size:  16384,
	}
	codec := wire.Codec{}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := codec.EncodeHeader(h, hdr[:]); err != nil {
			b.Fatal(err)
		}
		if _, err := codec.DecodeHeader(hdr[:]); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkHandshake(b *testing.B) {
	dialer := NewDialer(DialConfig{PSK: "bench", Domain: "client", KDF: KDFHKDF})
	acceptor := NewAcceptor(AcceptConfig{PSK: "bench", KDF: KDFHKDF})
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c1, c2 := net.Pipe()
		tr1, tr2 := NewTCP(c1), NewTCP(c2)
		type result struct {
			lnk *Link
			err error
		}
		done := make(chan result, 1)
		go func() {
			pending, _, err := acceptor.Accept(tr2)
			if err != nil {
				done <- result{err: err}
				return
			}
			lnk, err := pending.Respond(1)
			done <- result{lnk: lnk, err: err}
		}()
		client, _, err := dialer.Dial(tr1)
		if err != nil {
			b.Fatal(err)
		}
		res := <-done
		if res.err != nil {
			b.Fatal(res.err)
		}
		client.Close()
		res.lnk.Close()
	}
}

func BenchmarkArgon2idBootKey(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = deriveBootKey("bench-password", KDFArgon2id)
	}
}
