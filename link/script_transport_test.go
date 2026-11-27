package link

import (
	"crypto/ecdh"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/chacha20poly1305"

	"github.com/net-agent/flex/v4/wire"
	"github.com/stretchr/testify/require"
)

// scriptFrame is one pre-encoded frame fed to a scriptTransport.
type scriptFrame struct {
	header  [wire.HeaderSize]byte
	payload []byte
}

// scriptTransport is a deterministic in-memory Transport. Reads come from a
// script, writes are recorded, and errors can be injected per phase.
type scriptTransport struct {
	mu sync.Mutex

	frames  []scriptFrame
	pending []byte
	hasBody bool

	readErr    error
	payloadErr error

	writes   []FrameBytes
	writeErr error

	readDeadline  time.Time
	writeDeadline time.Time
	localAddr     net.Addr
	remoteAddr    net.Addr
	closeErr      error
	closeCount    int
	closeCh       chan struct{}
	closeOnce     sync.Once
}

func newScriptTransport(frames ...FrameBytes) *scriptTransport {
	t := &scriptTransport{}
	for _, f := range frames {
		t.frames = append(t.frames, scriptFrame{header: f.Header, payload: f.Payload})
	}
	return t
}

func (t *scriptTransport) ReadHeader(dst []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.hasBody {
		return ErrFrameNotConsumed
	}
	if len(t.frames) == 0 {
		if t.readErr != nil {
			return t.readErr
		}
		return io.EOF
	}
	f := t.frames[0]
	t.frames = t.frames[1:]
	copy(dst[:wire.HeaderSize], f.header[:])
	t.pending = f.payload
	t.hasBody = true
	return nil
}

func (t *scriptTransport) ReadPayload(n int) ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.hasBody {
		return nil, ErrShortFrame
	}
	p := t.pending
	t.pending = nil
	t.hasBody = false
	if t.payloadErr != nil {
		return nil, t.payloadErr
	}
	if len(p) != n {
		return nil, ErrFrameSizeMismatch
	}
	return p, nil
}

func (t *scriptTransport) WriteFrames(frames []FrameBytes) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, f := range frames {
		t.writes = append(t.writes, FrameBytes{
			Header:  f.Header,
			Payload: append([]byte(nil), f.Payload...),
		})
	}
	return t.writeErr
}

func (t *scriptTransport) SetReadDeadline(deadline time.Time) error {
	t.mu.Lock()
	t.readDeadline = deadline
	t.mu.Unlock()
	return nil
}

func (t *scriptTransport) SetWriteDeadline(deadline time.Time) error {
	t.mu.Lock()
	t.writeDeadline = deadline
	t.mu.Unlock()
	return nil
}

func (t *scriptTransport) LocalAddr() net.Addr {
	if t.localAddr != nil {
		return t.localAddr
	}
	return nil
}

func (t *scriptTransport) RemoteAddr() net.Addr {
	if t.remoteAddr != nil {
		return t.remoteAddr
	}
	return nil
}

func (t *scriptTransport) Close() error {
	t.mu.Lock()
	t.closeCount++
	t.mu.Unlock()
	if t.closeCh != nil {
		t.closeOnce.Do(func() { close(t.closeCh) })
	}
	return t.closeErr
}

func (t *scriptTransport) written() []FrameBytes {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]FrameBytes(nil), t.writes...)
}

// intoTransport adds the optional ReadPayloadInto extension on top of a
// scriptTransport. Link prefers this path when it is available.
type intoTransport struct {
	*scriptTransport
	intoErr error
}

func (t *intoTransport) ReadPayloadInto(dst []byte) error {
	t.mu.Lock()
	if !t.hasBody {
		t.mu.Unlock()
		return ErrShortFrame
	}
	p := t.pending
	t.pending = nil
	t.hasBody = false
	err := t.intoErr
	t.mu.Unlock()
	if err != nil {
		return err
	}
	if len(p) != len(dst) {
		return ErrFrameSizeMismatch
	}
	copy(dst, p)
	return nil
}

// --- handshake frame helpers ---------------------------------------------

const testRequestNonce = "requestnonce"

func sealBootRequest(t *testing.T, boot []byte, h wire.Header, plain []byte) FrameBytes {
	t.Helper()
	var aad [wire.HeaderSize]byte
	require.NoError(t, (wire.Codec{}).EncodeHeader(h, aad[:]))

	aead, err := chacha20poly1305.New(boot)
	require.NoError(t, err)
	var nonce [controlNonceSz]byte
	copy(nonce[:], testRequestNonce)
	ct := aead.Seal(nil, nonce[:], plain, aad[:])
	payload := append(append([]byte(nil), nonce[:]...), ct...)

	cipher, err := bootHeaderCipher(boot, true)
	require.NoError(t, err)
	var wireHdr [wire.HeaderSize]byte
	copy(wireHdr[:], aad[:])
	cipher.XORKeyStream(wireHdr[:], wireHdr[:])
	return FrameBytes{Header: wireHdr, Payload: payload}
}

func handshakeRequestHeader() wire.Header {
	return wire.Header{Class: wire.ClassHandshake, Cmd: wire.Cmd(wire.CmdAdmit), Size: handshakeSize}
}

func validHandshakeRequest(t *testing.T) handshakeRequest {
	t.Helper()
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	require.NoError(t, err)
	var nonce [16]byte
	copy(nonce[:], "0123456789abcdef")
	return handshakeRequest{
		Version:   handshakeVersion,
		Caps:      1,
		Domain:    "client-a",
		Mac:       "mac-a",
		Timestamp: time.Now().UnixNano(),
		Nonce:     nonce,
		Pub:       priv.PublicKey().Bytes(),
	}
}

func buildRequestFrame(t *testing.T, psk string, kdf KDF, req handshakeRequest) FrameBytes {
	t.Helper()
	boot := deriveBootKey(psk, kdf)
	data, err := req.marshal()
	require.NoError(t, err)
	plain, err := padData(data, requestPlainSize)
	require.NoError(t, err)
	return sealBootRequest(t, boot, handshakeRequestHeader(), plain)
}

// --- dial-side server helper ---------------------------------------------

type dialResponse struct {
	req         handshakeRequest
	aad         [wire.HeaderSize]byte
	serverPub   []byte
	serverNonce [16]byte
	kResp       []byte

	header  wire.Header
	plain   []byte
	payload []byte
}

// seal encrypts plain as the handshake response payload with the current
// server key material. Mutators may use it to build custom/corrupt payloads.
func (d *dialResponse) seal(plain []byte) []byte {
	var nonce [controlNonceSz]byte
	copy(nonce[:], "responsenonc")
	aead, _ := chacha20poly1305.New(d.kResp)
	aad := append(append([]byte(nil), d.aad[:]...), d.serverPub...)
	ct := aead.Seal(nil, nonce[:], plain, aad)
	out := append([]byte(nil), d.serverPub...)
	out = append(out, nonce[:]...)
	out = append(out, ct...)
	return out
}

// serveDialResponse runs on the server end of a net.Pipe, decodes a Dial
// request and writes a response shaped by mutate. It returns the first error.
func serveDialResponse(conn net.Conn, boot []byte, mutate func(*dialResponse)) error {
	st := NewTCP(conn)

	var wireHdr [wire.HeaderSize]byte
	if err := st.ReadHeader(wireHdr[:]); err != nil {
		return err
	}
	hdrCipher, err := bootHeaderCipher(boot, true)
	if err != nil {
		return err
	}
	var hdr [wire.HeaderSize]byte
	hdrCipher.XORKeyStream(hdr[:], wireHdr[:])
	h, err := (wire.Codec{}).DecodeHeader(hdr[:])
	if err != nil {
		return err
	}
	payload, err := st.ReadPayload(h.PayloadLen())
	if err != nil {
		return err
	}

	var aad [wire.HeaderSize]byte
	if err := (wire.Codec{}).EncodeHeader(h, aad[:]); err != nil {
		return err
	}
	aead, err := chacha20poly1305.New(boot)
	if err != nil {
		return err
	}
	plain, err := aead.Open(nil, payload[:controlNonceSz], payload[controlNonceSz:], aad[:])
	if err != nil {
		return err
	}
	req, err := parseHandshakeRequest(plain)
	if err != nil {
		return err
	}

	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	serverPub := priv.PublicKey().Bytes()
	peer := peerPublicKey(req.Pub)
	if peer == nil {
		return errHandshakeRejected
	}
	shared, err := priv.ECDH(peer)
	if err != nil {
		return err
	}
	d := &dialResponse{
		req:       *req,
		aad:       aad,
		serverPub: serverPub,
		kResp:     hkdfKey(shared, "flex-resp", handshakeTranscript(req.Pub, serverPub, req.Nonce[:], nil)),
	}
	copy(d.serverNonce[:], "servernonce12345")

	data := append([]byte{handshakeVersion, 0, 7, 0, 0}, d.serverNonce[:]...)
	d.plain, err = padData(data, responsePlainSize)
	if err != nil {
		return err
	}
	d.header = handshakeRequestHeader()
	if mutate != nil {
		mutate(d)
	}
	if d.payload == nil {
		d.payload = d.seal(d.plain)
	}

	sendCipher, err := bootHeaderCipher(boot, false)
	if err != nil {
		return err
	}
	var out [wire.HeaderSize]byte
	if err := (wire.Codec{}).EncodeHeader(d.header, out[:]); err != nil {
		return err
	}
	sendCipher.XORKeyStream(out[:], out[:])
	return st.WriteFrames([]FrameBytes{{Header: out, Payload: d.payload}})
}

// runDialServer starts serveDialResponse in the background and returns the
// server end error channel. The caller closes the server connection after Dial
// returns, which unblocks any pending write.
func runDialServer(conn net.Conn, boot []byte, mutate func(*dialResponse)) <-chan error {
	ch := make(chan error, 1)
	go func() {
		ch <- serveDialResponse(conn, boot, mutate)
	}()
	return ch
}

func errBoom() error { return errors.New("boom") }

// errRandSource is a stable sentinel so errors.Is works across the custom
// crypto/rand.Reader used by the RNG-failure tests.
var errRandSource = errors.New("rand source failed")

// failRandReader yields a fixed number of zero bytes and then fails. Tests
// temporarily install it as crypto/rand.Reader to exercise the otherwise
// unreachable RNG-failure branches without a production seam.
type failRandReader struct {
	mu   sync.Mutex
	data []byte
	off  int
	err  error
}

func (r *failRandReader) Read(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.off >= len(r.data) {
		return 0, r.err
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}

// failRandAfter installs a failing crypto/rand.Reader that supplies n zero
// bytes before returning an error. It restores the previous reader when the
// test (or subtest) finishes.
func failRandAfter(t *testing.T, n int) {
	t.Helper()
	old := rand.Reader
	rand.Reader = &failRandReader{data: make([]byte, n), err: errRandSource}
	t.Cleanup(func() { rand.Reader = old })
}
