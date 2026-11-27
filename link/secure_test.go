package link

import (
	"bytes"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v4/wire"
)

type tapTransport struct {
	Transport
	mu      sync.Mutex
	written []FrameBytes
}

func (t *tapTransport) WriteFrames(frames []FrameBytes) error {
	t.mu.Lock()
	for _, f := range frames {
		t.written = append(t.written, FrameBytes{
			Header:  f.Header,
			Payload: append([]byte(nil), f.Payload...),
		})
	}
	t.mu.Unlock()
	return t.Transport.WriteFrames(frames)
}

func (t *tapTransport) snapshot() []FrameBytes {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]FrameBytes, len(t.written))
	copy(out, t.written)
	return out
}

func (t *tapTransport) clear() {
	t.mu.Lock()
	t.written = nil
	t.mu.Unlock()
}

type securePairResult struct {
	lnk *Link
	hs  *Handshake
	err error
}

// newSecurePair performs a v4 handshake over net.Pipe and returns both links.
// The client transport is wrapped in a tap so tests can inspect wire bytes.
func newSecurePair(t *testing.T, psk string, clientTap **tapTransport) (*Link, *Link) {
	t.Helper()
	c1, c2 := net.Pipe()
	clientTr := NewTCP(c1)
	var tap *tapTransport
	if clientTap != nil {
		tap = &tapTransport{Transport: clientTr}
		*clientTap = tap
		clientTr = tap
	}
	serverTr := NewTCP(c2)

	dialer := NewDialer(DialConfig{
		PSK:     psk,
		Domain:  "client-a",
		KDF:     KDFHKDF,
		Timeout: 5 * time.Second,
	})
	acceptor := NewAcceptor(AcceptConfig{
		PSK:     psk,
		KDF:     KDFHKDF,
		Timeout: 5 * time.Second,
	})

	serverCh := make(chan securePairResult, 1)
	go func() {
		pending, hs, err := acceptor.Accept(serverTr)
		if err != nil {
			serverTr.Close()
			serverCh <- securePairResult{err: err}
			return
		}
		lnk, err := pending.Respond(7)
		if err != nil {
			serverTr.Close()
			serverCh <- securePairResult{err: err}
			return
		}
		serverCh <- securePairResult{lnk: lnk, hs: hs}
	}()

	client, ip, err := dialer.Dial(clientTr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if ip != 7 {
		t.Fatalf("assigned ip=%d", ip)
	}
	res := <-serverCh
	if res.err != nil {
		t.Fatalf("accept: %v", res.err)
	}
	if res.hs.Domain != "client-a" {
		t.Fatalf("handshake domain=%q", res.hs.Domain)
	}
	t.Cleanup(func() {
		client.Close()
		res.lnk.Close()
	})
	return client, res.lnk
}

func TestSecureLinkRoundTrip(t *testing.T) {
	var tap *tapTransport
	client, server := newSecurePair(t, "test-password", &tap)

	// Handshake shape: 11-byte header + fixed 512-byte payload.
	frames := tap.snapshot()
	if len(frames) == 0 || len(frames[0].Payload) != handshakeSize {
		t.Fatalf("handshake frames=%d", len(frames))
	}
	if frames[0].Header[0]&0x80 == 0 {
		t.Fatal("handshake class bit not set")
	}
	tap.clear()

	src := wire.Addr{IP: 1, Port: 1000}
	dst := wire.Addr{IP: 7, Port: 80}

	// Data payload is opaque and must appear verbatim on the wire.
	if err := client.SendData(src, dst, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	got, err := server.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if got.Class() != wire.ClassData || !bytes.Equal(got.Payload(), []byte("hello")) {
		t.Fatalf("data frame=%s payload=%q", got, got.Payload())
	}
	got.Release()

	frames = tap.snapshot()
	last := frames[len(frames)-1]
	if !bytes.Equal(last.Payload, []byte("hello")) {
		t.Fatalf("data payload changed on wire: %q", last.Payload)
	}

	// Control payload must be sealed and padded, and must not contain the
	// plaintext domain string.
	if err := client.SendControl(wire.CmdOpenStream, src, dst, []byte("secret.example")); err != nil {
		t.Fatal(err)
	}
	got, err = server.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if got.Class() != wire.ClassControl || string(got.Payload()) != "secret.example" {
		t.Fatalf("control frame=%s payload=%q", got, got.Payload())
	}
	got.Release()

	frames = tap.snapshot()
	last = frames[len(frames)-1]
	if bytes.Contains(last.Payload, []byte("secret.example")) {
		t.Fatal("control plaintext appeared on the wire")
	}
	if len(last.Payload) != controlNonceSz+controlPadTo+controlTagSz {
		t.Fatalf("padded control payload len=%d", len(last.Payload))
	}

	// ACK count lives in the encrypted header.
	if err := server.SendAck(4096, dst, src); err != nil {
		t.Fatal(err)
	}
	got, err = client.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if got.Cmd() != wire.Cmd(wire.AckPushStreamData) || got.AckSize() != 4096 {
		t.Fatalf("ack frame=%s ack=%d", got, got.AckSize())
	}
	got.Release()

	// Header-only close.
	if err := client.SendEmpty(wire.CmdCloseStream, src, dst); err != nil {
		t.Fatal(err)
	}
	got, err = server.ReadFrame()
	if err != nil {
		t.Fatal(err)
	}
	if got.Cmd() != wire.Cmd(wire.CmdCloseStream) || got.PayloadSize() != 0 {
		t.Fatalf("close frame=%s", got)
	}
	got.Release()
}

func TestSecureHandshakeWrongPSK(t *testing.T) {
	c1, c2 := net.Pipe()
	clientTr := NewTCP(c1)
	serverTr := NewTCP(c2)

	acceptor := NewAcceptor(AcceptConfig{PSK: "right", KDF: KDFHKDF, Timeout: 2 * time.Second})
	go func() {
		_, _, err := acceptor.Accept(serverTr)
		if err == nil {
			t.Error("expected accept failure")
		}
		serverTr.Close()
	}()

	dialer := NewDialer(DialConfig{PSK: "wrong", Domain: "client", KDF: KDFHKDF, Timeout: 2 * time.Second})
	if _, _, err := dialer.Dial(clientTr); err == nil {
		t.Fatal("expected dial failure")
	}
	clientTr.Close()
}

func TestSecureHandshakeReplayRejected(t *testing.T) {
	acceptor := NewAcceptor(AcceptConfig{
		PSK:       "test-password",
		KDF:       KDFHKDF,
		Timeout:   2 * time.Second,
		ReplayTTL: time.Minute,
	})

	// First connection: normal handshake, capture the request bytes.
	c1, c2 := net.Pipe()
	clientTr := NewTCP(c1)
	tap := &tapTransport{Transport: clientTr}
	serverTr := NewTCP(c2)
	serverCh := make(chan error, 1)
	go func() {
		pending, _, err := acceptor.Accept(serverTr)
		if err != nil {
			serverTr.Close()
			serverCh <- err
			return
		}
		_, err = pending.Respond(9)
		serverCh <- err
	}()
	dialer := NewDialer(DialConfig{PSK: "test-password", Domain: "client", KDF: KDFHKDF, Timeout: 2 * time.Second})
	client, _, err := dialer.Dial(tap)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if err := <-serverCh; err != nil {
		t.Fatalf("accept: %v", err)
	}
	client.Close()
	serverTr.Close()

	frames := tap.snapshot()
	if len(frames) == 0 {
		t.Fatal("no handshake request captured")
	}
	request := frames[0]

	// Second connection: replay the exact request to the same acceptor.
	c3, c4 := net.Pipe()
	replayClient := NewTCP(c3)
	replayServer := NewTCP(c4)
	acceptCh := make(chan error, 1)
	go func() {
		_, _, err := acceptor.Accept(replayServer)
		acceptCh <- err
		replayServer.Close()
	}()
	go func() {
		_ = replayClient.WriteFrames([]FrameBytes{request})
	}()
	select {
	case err := <-acceptCh:
		if !errors.Is(err, errHandshakeReplay) {
			t.Fatalf("replay err=%v, want errHandshakeReplay", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("replay accept timed out")
	}
	replayClient.Close()
}
