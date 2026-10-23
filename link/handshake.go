package link

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"sync"
	"time"

	"golang.org/x/crypto/chacha20"
	"golang.org/x/crypto/chacha20poly1305"

	"github.com/net-agent/flex/v3/internal/admit"
	"github.com/net-agent/flex/v3/internal/sched"
	"github.com/net-agent/flex/v3/wire"
)

const (
	defaultHandshakeTimeout = 10 * time.Second
	maxTimestampSkew        = 5 * time.Minute
	requestPlainSize        = handshakeSize - controlNonceSz - controlTagSz      // 484
	responsePlainSize       = handshakeSize - 32 - controlNonceSz - controlTagSz // 452
)

// DialConfig configures a client-side link handshake.
type DialConfig struct {
	PSK       string
	Domain    string
	Mac       string
	KDF       KDF
	Scheduler sched.SchedulerConfig
	Timeout   time.Duration
}

// AcceptConfig configures a server-side link handshake.
type AcceptConfig struct {
	PSK       string
	KDF       KDF
	Scheduler sched.SchedulerConfig
	Timeout   time.Duration
	// ReplayTTL bounds the nonce cache; zero means maxTimestampSkew.
	ReplayTTL time.Duration
	// ReplayMax caps the nonce cache; zero means 1<<20. Full cache fails closed.
	ReplayMax int
}

// Dialer performs the v4 handshake as the initiator. The bootstrap key is
// derived once and reused across reconnects.
type Dialer struct {
	cfg  DialConfig
	once sync.Once
	boot []byte
}

func (d *Dialer) bootKey() []byte {
	d.once.Do(func() { d.boot = deriveBootKey(d.cfg.PSK, d.cfg.KDF) })
	return d.boot
}

// NewDialer creates a client handshake helper.
func NewDialer(cfg DialConfig) *Dialer {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultHandshakeTimeout
	}
	cfg.Scheduler = cfg.Scheduler.Normalized()
	return &Dialer{cfg: cfg}
}

// SetSchedulerConfig updates the scheduler used by links created afterwards.
func (d *Dialer) SetSchedulerConfig(cfg sched.SchedulerConfig) {
	d.cfg.Scheduler = cfg.Normalized()
}

// Dial performs the handshake and returns a secure link plus the assigned IP.
func (d *Dialer) Dial(t Transport) (*Link, uint16, error) {
	deadline := time.Now().Add(d.cfg.Timeout)
	_ = t.SetReadDeadline(deadline)
	_ = t.SetWriteDeadline(deadline)

	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, 0, err
	}
	clientPub := priv.PublicKey().Bytes()

	var clientNonce [16]byte
	if _, err := rand.Read(clientNonce[:]); err != nil {
		return nil, 0, err
	}

	boot := d.bootKey()
	req := handshakeRequest{
		Version:   handshakeVersion,
		Domain:    d.cfg.Domain,
		Mac:       d.cfg.Mac,
		Timestamp: time.Now().UnixNano(),
		Nonce:     clientNonce,
		Pub:       clientPub,
	}
	data, err := req.marshal()
	if err != nil {
		return nil, 0, err
	}
	plain, err := padData(data, requestPlainSize)
	if err != nil {
		return nil, 0, err
	}

	hdr := wire.Header{
		Class: wire.ClassHandshake,
		Cmd:   wire.Cmd(wire.CmdAdmit),
		Size:  handshakeSize,
	}
	var aad [wire.HeaderSize]byte
	if err := (wire.Codec{}).EncodeHeader(hdr, aad[:]); err != nil {
		return nil, 0, err
	}

	aead, err := chacha20poly1305.New(boot)
	if err != nil {
		return nil, 0, err
	}
	var nonce [controlNonceSz]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, 0, err
	}
	ct := aead.Seal(nil, nonce[:], plain, aad[:])
	payload := make([]byte, 0, handshakeSize)
	payload = append(payload, nonce[:]...)
	payload = append(payload, ct...)
	if len(payload) != handshakeSize {
		return nil, 0, fmt.Errorf("link: bad request size %d", len(payload))
	}

	sendHdr, err := bootHeaderCipher(boot, true)
	if err != nil {
		return nil, 0, err
	}
	if err := writeBootFrame(t, sendHdr, hdr, payload); err != nil {
		return nil, 0, err
	}

	recvHdr, err := bootHeaderCipher(boot, false)
	if err != nil {
		return nil, 0, err
	}
	resp, respPayload, err := readBootFrame(t, recvHdr)
	if err != nil {
		return nil, 0, err
	}
	if resp.Class != wire.ClassHandshake || resp.Cmd != wire.Cmd(wire.CmdAdmit) || len(respPayload) != handshakeSize {
		return nil, 0, fmt.Errorf("%w: bad response header", errHandshakeRejected)
	}

	serverPub := respPayload[:32]
	respNonce := respPayload[32 : 32+controlNonceSz]
	respCT := respPayload[32+controlNonceSz:]

	peer := peerPublicKey(serverPub)
	if peer == nil {
		return nil, 0, fmt.Errorf("%w: bad server key", errHandshakeRejected)
	}
	shared, err := priv.ECDH(peer)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: ecdh", errHandshakeRejected)
	}
	transcript0 := handshakeTranscript(clientPub, serverPub, clientNonce[:], nil)
	kResp := hkdfKey(shared, "flex-resp", transcript0)
	respAEAD, err := chacha20poly1305.New(kResp)
	if err != nil {
		return nil, 0, err
	}
	respAAD := append(append([]byte(nil), aad[:]...), serverPub...)
	respPlain, err := respAEAD.Open(nil, respNonce, respCT, respAAD)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: response decrypt", errHandshakeRejected)
	}
	ip, serverNonce, err := parseHandshakeResponse(respPlain)
	if err != nil {
		return nil, 0, err
	}

	transcript1 := handshakeTranscript(clientPub, serverPub, clientNonce[:], serverNonce)
	linkKey := hkdfKey(shared, "flex-link", transcript1)
	lnk, err := NewSecure(t, linkKey, true, Config{Scheduler: d.cfg.Scheduler})
	if err != nil {
		return nil, 0, err
	}
	_ = t.SetReadDeadline(time.Time{})
	_ = t.SetWriteDeadline(time.Time{})
	return lnk, ip, nil
}

// Handshake is the authenticated metadata returned by Acceptor.Accept.
type Handshake struct {
	Version byte
	Caps    byte
	Domain  string
	Mac     string
}

// Pending is a decoded handshake waiting for the caller to assign an IP and
// send the response. The response must be written before any data frame.
type Pending struct {
	acceptor    *Acceptor
	t           Transport
	req         *handshakeRequest
	serverPub   []byte
	kResp       []byte
	kLink       []byte
	serverNonce [16]byte
	used        bool
}

// Acceptor performs the v4 handshake as the responder.
type Acceptor struct {
	cfg    AcceptConfig
	once   sync.Once
	boot   []byte
	replay *replayCache
}

func (a *Acceptor) bootKey() []byte {
	a.once.Do(func() { a.boot = deriveBootKey(a.cfg.PSK, a.cfg.KDF) })
	return a.boot
}

// NewAcceptor creates a server handshake helper. The bootstrap key is derived
// once; the replay cache is shared across connections.
func NewAcceptor(cfg AcceptConfig) *Acceptor {
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultHandshakeTimeout
	}
	if cfg.ReplayTTL <= 0 {
		cfg.ReplayTTL = maxTimestampSkew
	}
	if cfg.ReplayMax <= 0 {
		cfg.ReplayMax = 1 << 20
	}
	cfg.Scheduler = cfg.Scheduler.Normalized()
	return &Acceptor{
		cfg:    cfg,
		replay: newReplayCache(cfg.ReplayTTL, cfg.ReplayMax),
	}
}

// SetSchedulerConfig updates the scheduler used by links created afterwards.
func (a *Acceptor) SetSchedulerConfig(cfg sched.SchedulerConfig) {
	a.cfg.Scheduler = cfg.Normalized()
}

// Accept decodes the client's request and returns a Pending that will send the
// response once the caller has assigned an IP.
func (a *Acceptor) Accept(t Transport) (*Pending, *Handshake, error) {
	deadline := time.Now().Add(a.cfg.Timeout)
	_ = t.SetReadDeadline(deadline)
	_ = t.SetWriteDeadline(deadline)

	boot := a.bootKey()
	recvHdr, err := bootHeaderCipher(boot, true)
	if err != nil {
		return nil, nil, err
	}
	hdr, payload, err := readBootFrame(t, recvHdr)
	if err != nil {
		return nil, nil, err
	}
	if hdr.Class != wire.ClassHandshake || hdr.Cmd != wire.Cmd(wire.CmdAdmit) || len(payload) != handshakeSize {
		return nil, nil, fmt.Errorf("%w: bad request header", errHandshakeRejected)
	}

	var aad [wire.HeaderSize]byte
	if err := (wire.Codec{}).EncodeHeader(hdr, aad[:]); err != nil {
		return nil, nil, err
	}
	if len(payload) < controlNonceSz+controlTagSz {
		return nil, nil, fmt.Errorf("%w: short request", errHandshakeRejected)
	}
	reqNonce := payload[:controlNonceSz]
	reqCT := payload[controlNonceSz:]

	aead, err := chacha20poly1305.New(boot)
	if err != nil {
		return nil, nil, err
	}
	plain, err := aead.Open(nil, reqNonce, reqCT, aad[:])
	if err != nil {
		return nil, nil, fmt.Errorf("%w: request decrypt", errHandshakeRejected)
	}
	req, err := parseHandshakeRequest(plain)
	if err != nil {
		return nil, nil, err
	}
	if req.Version != handshakeVersion {
		return nil, nil, errHandshakeVersion
	}
	drift := time.Since(time.Unix(0, req.Timestamp))
	if drift < 0 {
		drift = -drift
	}
	if drift > maxTimestampSkew {
		return nil, nil, errHandshakeRejected
	}
	if !a.replay.add(req.Nonce, time.Now()) {
		return nil, nil, errHandshakeReplay
	}
	domain, err := admit.NormalizeDomain(req.Domain)
	if err != nil {
		return nil, nil, err
	}
	req.Domain = domain

	serverPriv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serverPub := serverPriv.PublicKey().Bytes()
	peer := peerPublicKey(req.Pub)
	if peer == nil {
		return nil, nil, fmt.Errorf("%w: bad client key", errHandshakeRejected)
	}
	shared, err := serverPriv.ECDH(peer)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: ecdh", errHandshakeRejected)
	}

	var serverNonce [16]byte
	if _, err := rand.Read(serverNonce[:]); err != nil {
		return nil, nil, err
	}
	transcript0 := handshakeTranscript(req.Pub, serverPub, req.Nonce[:], nil)
	kResp := hkdfKey(shared, "flex-resp", transcript0)
	transcript1 := handshakeTranscript(req.Pub, serverPub, req.Nonce[:], serverNonce[:])
	kLink := hkdfKey(shared, "flex-link", transcript1)

	p := &Pending{
		acceptor:    a,
		t:           t,
		req:         req,
		serverPub:   serverPub,
		kResp:       kResp,
		kLink:       kLink,
		serverNonce: serverNonce,
	}
	hs := &Handshake{Version: req.Version, Caps: req.Caps, Domain: req.Domain, Mac: req.Mac}
	return p, hs, nil
}

// Respond sends the handshake response with the assigned virtual IP and
// returns the secure link. It may be called once.
func (p *Pending) Respond(ip uint16) (*Link, error) {
	if p.used {
		return nil, fmt.Errorf("%w: response already sent", errHandshakeRejected)
	}
	p.used = true

	data := make([]byte, 0, 32)
	data = append(data, handshakeVersion)
	var tmp2 [2]byte
	binary.BigEndian.PutUint16(tmp2[:], ip)
	data = append(data, tmp2[:]...)
	binary.BigEndian.PutUint16(tmp2[:], uint16(p.req.Caps))
	data = append(data, tmp2[:]...)
	data = append(data, p.serverNonce[:]...)

	plain, err := padData(data, responsePlainSize)
	if err != nil {
		return nil, err
	}
	hdr := wire.Header{
		Class: wire.ClassHandshake,
		Cmd:   wire.Cmd(wire.CmdAdmit),
		Size:  handshakeSize,
	}
	var aad [wire.HeaderSize]byte
	if err := (wire.Codec{}).EncodeHeader(hdr, aad[:]); err != nil {
		return nil, err
	}

	respAEAD, err := chacha20poly1305.New(p.kResp)
	if err != nil {
		return nil, err
	}
	var nonce [controlNonceSz]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, err
	}
	respAAD := append(append([]byte(nil), aad[:]...), p.serverPub...)
	ct := respAEAD.Seal(nil, nonce[:], plain, respAAD)

	payload := make([]byte, 0, handshakeSize)
	payload = append(payload, p.serverPub...)
	payload = append(payload, nonce[:]...)
	payload = append(payload, ct...)
	if len(payload) != handshakeSize {
		return nil, fmt.Errorf("link: bad response size %d", len(payload))
	}

	_ = p.t.SetWriteDeadline(time.Now().Add(p.acceptor.cfg.Timeout))
	sendHdr, err := bootHeaderCipher(p.acceptor.bootKey(), false)
	if err != nil {
		return nil, err
	}
	if err := writeBootFrame(p.t, sendHdr, hdr, payload); err != nil {
		return nil, err
	}

	lnk, err := NewSecure(p.t, p.kLink, false, Config{Scheduler: p.acceptor.cfg.Scheduler})
	if err != nil {
		return nil, err
	}
	_ = p.t.SetReadDeadline(time.Time{})
	_ = p.t.SetWriteDeadline(time.Time{})
	return lnk, nil
}

// --- wire helpers ---------------------------------------------------------

func bootHeaderCipher(boot []byte, clientToServer bool) (*chacha20.Cipher, error) {
	label := "flex-boot-hdr-s2c"
	if clientToServer {
		label = "flex-boot-hdr-c2s"
	}
	key := hkdfKey(boot, label, nil)
	var zero [chacha20.NonceSize]byte
	return chacha20.NewUnauthenticatedCipher(key, zero[:])
}

func writeBootFrame(t Transport, hdrCipher *chacha20.Cipher, h wire.Header, payload []byte) error {
	var hdr [wire.HeaderSize]byte
	if err := (wire.Codec{}).EncodeHeader(h, hdr[:]); err != nil {
		return err
	}
	hdrCipher.XORKeyStream(hdr[:], hdr[:])
	return t.WriteFrames([]FrameBytes{{Header: hdr, Payload: payload}})
}

func readBootFrame(t Transport, hdrCipher *chacha20.Cipher) (wire.Header, []byte, error) {
	var wireHdr [wire.HeaderSize]byte
	if err := t.ReadHeader(wireHdr[:]); err != nil {
		return wire.Header{}, nil, err
	}
	var hdr [wire.HeaderSize]byte
	hdrCipher.XORKeyStream(hdr[:], wireHdr[:])
	h, err := (wire.Codec{}).DecodeHeader(hdr[:])
	if err != nil {
		return wire.Header{}, nil, err
	}
	payload, err := t.ReadPayload(h.PayloadLen())
	if err != nil {
		return wire.Header{}, nil, err
	}
	return h, payload, nil
}

func peerPublicKey(b []byte) *ecdh.PublicKey {
	pub, err := ecdh.X25519().NewPublicKey(b)
	if err != nil {
		return nil
	}
	return pub
}

func handshakeTranscript(clientPub, serverPub, clientNonce, serverNonce []byte) []byte {
	h := sha256.New()
	h.Write(clientPub)
	h.Write(serverPub)
	h.Write(clientNonce)
	if serverNonce != nil {
		h.Write(serverNonce)
	}
	return h.Sum(nil)
}

func padData(data []byte, size int) ([]byte, error) {
	if len(data)+2 > size {
		return nil, fmt.Errorf("link: handshake data too large: %d", len(data))
	}
	out := make([]byte, size)
	binary.BigEndian.PutUint16(out[:2], uint16(len(data)))
	copy(out[2:], data)
	return out, nil
}

func unpadData(plain []byte) ([]byte, error) {
	if len(plain) < 2 {
		return nil, fmt.Errorf("%w: short handshake plaintext", errHandshakeRejected)
	}
	n := int(binary.BigEndian.Uint16(plain[:2]))
	if n > len(plain)-2 {
		return nil, fmt.Errorf("%w: bad handshake length", errHandshakeRejected)
	}
	return plain[2 : 2+n], nil
}

type handshakeRequest struct {
	Version   byte
	Caps      byte
	Domain    string
	Mac       string
	Timestamp int64
	Nonce     [16]byte
	Pub       []byte
}

func (r *handshakeRequest) marshal() ([]byte, error) {
	if len(r.Domain) > 0xFFFF || len(r.Mac) > 0xFF || len(r.Pub) != 32 {
		return nil, fmt.Errorf("%w: bad request fields", errHandshakeRejected)
	}
	out := make([]byte, 0, 2+len(r.Domain)+1+len(r.Mac)+8+16+32)
	out = append(out, r.Version, r.Caps)
	var tmp [2]byte
	binary.BigEndian.PutUint16(tmp[:], uint16(len(r.Domain)))
	out = append(out, tmp[:]...)
	out = append(out, r.Domain...)
	out = append(out, byte(len(r.Mac)))
	out = append(out, r.Mac...)
	var ts [8]byte
	binary.BigEndian.PutUint64(ts[:], uint64(r.Timestamp))
	out = append(out, ts[:]...)
	out = append(out, r.Nonce[:]...)
	out = append(out, r.Pub...)
	return out, nil
}

func parseHandshakeRequest(plain []byte) (*handshakeRequest, error) {
	data, err := unpadData(plain)
	if err != nil {
		return nil, err
	}
	if len(data) < 2 {
		return nil, errHandshakeRejected
	}
	r := &handshakeRequest{Version: data[0], Caps: data[1]}
	data = data[2:]
	if len(data) < 2 {
		return nil, errHandshakeRejected
	}
	dlen := int(binary.BigEndian.Uint16(data[:2]))
	data = data[2:]
	if len(data) < dlen {
		return nil, errHandshakeRejected
	}
	r.Domain = string(data[:dlen])
	data = data[dlen:]
	if len(data) < 1 {
		return nil, errHandshakeRejected
	}
	mlen := int(data[0])
	data = data[1:]
	if len(data) < mlen {
		return nil, errHandshakeRejected
	}
	r.Mac = string(data[:mlen])
	data = data[mlen:]
	if len(data) < 8+16+32 {
		return nil, errHandshakeRejected
	}
	r.Timestamp = int64(binary.BigEndian.Uint64(data[:8]))
	data = data[8:]
	copy(r.Nonce[:], data[:16])
	data = data[16:]
	r.Pub = append([]byte(nil), data[:32]...)
	return r, nil
}

func parseHandshakeResponse(plain []byte) (uint16, []byte, error) {
	data, err := unpadData(plain)
	if err != nil {
		return 0, nil, err
	}
	if len(data) < 1+2+2+16 {
		return 0, nil, errHandshakeRejected
	}
	if data[0] != handshakeVersion {
		return 0, nil, errHandshakeVersion
	}
	ip := binary.BigEndian.Uint16(data[1:3])
	// caps := binary.BigEndian.Uint16(data[3:5]) // reserved for future negotiation
	nonce := append([]byte(nil), data[5:21]...)
	return ip, nonce, nil
}

// --- replay cache ---------------------------------------------------------

type replayCache struct {
	mu      sync.Mutex
	entries map[[16]byte]time.Time
	ttl     time.Duration
	max     int
}

func newReplayCache(ttl time.Duration, max int) *replayCache {
	return &replayCache{entries: make(map[[16]byte]time.Time), ttl: ttl, max: max}
}

// add returns false when the nonce is a replay or the cache cannot accept it.
func (c *replayCache) add(nonce [16]byte, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for n, at := range c.entries {
		if now.Sub(at) > c.ttl {
			delete(c.entries, n)
		}
	}
	if _, exists := c.entries[nonce]; exists {
		return false
	}
	if len(c.entries) >= c.max {
		return false
	}
	c.entries[nonce] = now
	return true
}
