package link

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	domainpkg "github.com/net-agent/flex/v4/internal/domain"
	"github.com/net-agent/flex/v4/internal/sched"
	"github.com/net-agent/flex/v4/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestKeysDerivation(t *testing.T) {
	// One Argon2id invocation costs ~tens of ms; the long-lived Dialer/Acceptor
	// caches it, so a single call is enough to cover the default branch.
	key := deriveBootKey("default-kdf", KDFArgon2id)
	assert.Len(t, key, bootKeyLen)
	assert.Len(t, deriveBootKey("x", KDFHKDF), bootKeyLen)
	assert.Len(t, hkdfKey([]byte("secret"), "info", []byte("salt")), linkKeyLen)

}

func TestNewDialerAcceptorDefaults(t *testing.T) {
	d := NewDialer(DialConfig{PSK: "x", KDF: KDFHKDF})
	assert.Equal(t, defaultHandshakeTimeout, d.cfg.Timeout)
	d.SetSchedulerConfig(sched.SchedulerConfig{Policy: sched.PolicyPacketQuantum, QuantumPackets: 3})
	assert.Equal(t, sched.PolicyPacketQuantum, d.cfg.Scheduler.Policy)
	assert.Equal(t, 3, d.cfg.Scheduler.QuantumPackets)

	a := NewAcceptor(AcceptConfig{PSK: "x", KDF: KDFHKDF})
	assert.Equal(t, defaultHandshakeTimeout, a.cfg.Timeout)
	assert.Equal(t, maxTimestampSkew, a.cfg.ReplayTTL)
	assert.Equal(t, 1<<20, a.cfg.ReplayMax)
	a.SetSchedulerConfig(sched.SchedulerConfig{QuantumBytes: 4096, MinChargeBytes: 512})
	assert.Equal(t, 4096, a.cfg.Scheduler.QuantumBytes)

	a2 := NewAcceptor(AcceptConfig{PSK: "x", KDF: KDFHKDF, ReplayTTL: time.Minute, ReplayMax: 2, Timeout: time.Second})
	assert.Equal(t, 2, a2.cfg.ReplayMax)
	assert.Equal(t, time.Minute, a2.cfg.ReplayTTL)
	assert.Len(t, d.bootKey(), bootKeyLen)
	assert.Len(t, a.bootKey(), bootKeyLen)
}

func TestAcceptRejectsBadRequests(t *testing.T) {
	const psk = "accept-psk"
	boot := deriveBootKey(psk, KDFHKDF)

	// requestFrame builds a normal request and lets the case mutate it before
	// sealing. This keeps every case independent of the internal key schedule.
	requestFrame := func(mut func(*handshakeRequest)) FrameBytes {
		req := validHandshakeRequest(t)
		if mut != nil {
			mut(&req)
		}
		data, err := req.marshal()
		require.NoError(t, err)
		plain, err := padData(data, requestPlainSize)
		require.NoError(t, err)
		return sealBootRequest(t, boot, handshakeRequestHeader(), plain)
	}

	cases := []struct {
		name    string
		frame   func() FrameBytes
		wantErr error
	}{
		{
			name:    "version mismatch",
			frame:   func() FrameBytes { return requestFrame(func(r *handshakeRequest) { r.Version = handshakeVersion - 1 }) },
			wantErr: errHandshakeVersion,
		},
		{
			name: "timestamp too old",
			frame: func() FrameBytes {
				return requestFrame(func(r *handshakeRequest) { r.Timestamp = time.Now().Add(-maxTimestampSkew - time.Second).UnixNano() })
			},
			wantErr: errHandshakeRejected,
		},
		{
			name: "timestamp in future",
			frame: func() FrameBytes {
				return requestFrame(func(r *handshakeRequest) { r.Timestamp = time.Now().Add(maxTimestampSkew + time.Second).UnixNano() })
			},
			wantErr: errHandshakeRejected,
		},
		{
			name:    "invalid domain",
			frame:   func() FrameBytes { return requestFrame(func(r *handshakeRequest) { r.Domain = "bad domain!" }) },
			wantErr: domainpkg.ErrInvalidDomain,
		},
		{
			name:    "empty domain",
			frame:   func() FrameBytes { return requestFrame(func(r *handshakeRequest) { r.Domain = "" }) },
			wantErr: domainpkg.ErrInvalidDomain,
		},
		{
			name:    "low order public key",
			frame:   func() FrameBytes { return requestFrame(func(r *handshakeRequest) { r.Pub = make([]byte, 32) }) },
			wantErr: errHandshakeRejected,
		},
		{
			name: "corrupt ciphertext",
			frame: func() FrameBytes {
				f := requestFrame(nil)
				f.Payload[len(f.Payload)-1] ^= 0xff
				return f
			},
			wantErr: errHandshakeRejected,
		},
		{
			name: "bad padding length",
			frame: func() FrameBytes {
				plain := make([]byte, requestPlainSize)
				binary.BigEndian.PutUint16(plain[:2], 0xffff)
				return sealBootRequest(t, boot, handshakeRequestHeader(), plain)
			},
			wantErr: errHandshakeRejected,
		},
		{
			name: "bad request header class",
			frame: func() FrameBytes {
				return sealBootHeader(t, boot, wire.Header{Class: wire.ClassData, Cmd: wire.Cmd(wire.CmdPushStreamData), Size: handshakeSize}, make([]byte, handshakeSize))
			},
			wantErr: errHandshakeRejected,
		},
		{
			name: "short request payload",
			frame: func() FrameBytes {
				return sealBootHeader(t, boot, wire.Header{Class: wire.ClassHandshake, Cmd: wire.Cmd(wire.CmdAdmit), Size: 100}, make([]byte, 100))
			},
			wantErr: errHandshakeRejected,
		},
		{
			name: "undecodable header",
			frame: func() FrameBytes {
				// A data command without the class bit is a class mismatch; the
				// bootstrap header cipher makes this a valid ciphertext.
				plain := [wire.HeaderSize]byte{byte(wire.CmdOpenStream)}
				return FrameBytes{Header: encryptBootHeader(t, boot, true, plain)}
			},
			wantErr: wire.ErrClassMismatch,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			acceptor := NewAcceptor(AcceptConfig{PSK: psk, KDF: KDFHKDF, Timeout: time.Second})
			tr := newScriptTransport(tc.frame())
			_, _, err := acceptor.Accept(tr)
			require.Error(t, err)
			assert.ErrorIs(t, err, tc.wantErr)
		})
	}

	t.Run("domain is normalized", func(t *testing.T) {
		acceptor := NewAcceptor(AcceptConfig{PSK: psk, KDF: KDFHKDF, Timeout: time.Second})
		tr := newScriptTransport(requestFrame(func(r *handshakeRequest) { r.Domain = "  Client-A  " }))
		_, hs, err := acceptor.Accept(tr)
		require.NoError(t, err)
		assert.Equal(t, "client-a", hs.Domain)
		assert.Equal(t, byte(handshakeVersion), hs.Version)
		assert.Equal(t, "mac-a", hs.Mac)
	})

	t.Run("read header error", func(t *testing.T) {
		want := errors.New("read boom")
		acceptor := NewAcceptor(AcceptConfig{PSK: psk, KDF: KDFHKDF, Timeout: time.Second})
		tr := &scriptTransport{readErr: want}
		_, _, err := acceptor.Accept(tr)
		assert.ErrorIs(t, err, want)
	})

	t.Run("read payload error", func(t *testing.T) {
		want := errors.New("payload boom")
		acceptor := NewAcceptor(AcceptConfig{PSK: psk, KDF: KDFHKDF, Timeout: time.Second})
		tr := newScriptTransport(requestFrame(nil))
		tr.payloadErr = want
		_, _, err := acceptor.Accept(tr)
		assert.ErrorIs(t, err, want)
	})
}

func TestAcceptReplayCache(t *testing.T) {
	const psk = "replay-psk"
	frame := buildRequestFrame(t, psk, KDFHKDF, validHandshakeRequest(t))

	acceptor := NewAcceptor(AcceptConfig{PSK: psk, KDF: KDFHKDF, Timeout: time.Second, ReplayMax: 8})
	tr := newScriptTransport(frame)
	pending, _, err := acceptor.Accept(tr)
	require.NoError(t, err)
	lnk, err := pending.Respond(1)
	require.NoError(t, err)
	require.NoError(t, lnk.Close())

	// The exact same nonce must be rejected on a second connection.
	_, _, err = acceptor.Accept(newScriptTransport(frame))
	assert.ErrorIs(t, err, errHandshakeReplay)
}

func TestPendingRespondOnlyOnce(t *testing.T) {
	frame := buildRequestFrame(t, "respond-psk", KDFHKDF, validHandshakeRequest(t))
	tr := newScriptTransport(frame)
	acceptor := NewAcceptor(AcceptConfig{PSK: "respond-psk", KDF: KDFHKDF, Timeout: time.Second})

	pending, _, err := acceptor.Accept(tr)
	require.NoError(t, err)
	lnk, err := pending.Respond(7)
	require.NoError(t, err)
	require.NotNil(t, lnk)
	t.Cleanup(func() { _ = lnk.Close() })

	assert.Len(t, tr.written(), 1)
	_, err = pending.Respond(8)
	require.Error(t, err)
	assert.ErrorIs(t, err, errHandshakeRejected)

	// The response was written with the secure handshake shape.
	frames := tr.written()
	assert.Len(t, frames, 1)
	assert.Len(t, frames[0].Payload, handshakeSize)
}

func TestPendingRespondWriteError(t *testing.T) {
	frame := buildRequestFrame(t, "respond-err", KDFHKDF, validHandshakeRequest(t))
	tr := newScriptTransport(frame)
	tr.writeErr = errBoom()
	acceptor := NewAcceptor(AcceptConfig{PSK: "respond-err", KDF: KDFHKDF, Timeout: time.Second})

	pending, _, err := acceptor.Accept(tr)
	require.NoError(t, err)
	lnk, err := pending.Respond(1)
	assert.Nil(t, lnk)
	assert.ErrorIs(t, err, tr.writeErr)
}

func TestDialRejectsBadConfig(t *testing.T) {
	t.Run("marshal failure", func(t *testing.T) {
		d := NewDialer(DialConfig{PSK: "x", KDF: KDFHKDF, Domain: strings.Repeat("a", 0x10000), Timeout: time.Second})
		_, _, err := d.Dial(&scriptTransport{})
		assert.ErrorIs(t, err, errHandshakeRejected)
	})
	t.Run("padding failure", func(t *testing.T) {
		d := NewDialer(DialConfig{PSK: "x", KDF: KDFHKDF, Domain: strings.Repeat("a", 600), Timeout: time.Second})
		_, _, err := d.Dial(&scriptTransport{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "handshake data too large")
	})
}

func TestDialTransportErrors(t *testing.T) {
	psk := "dial-psk"
	boot := deriveBootKey(psk, KDFHKDF)
	newDialer := func() *Dialer {
		return NewDialer(DialConfig{PSK: psk, KDF: KDFHKDF, Domain: "client", Timeout: time.Second})
	}

	t.Run("write error", func(t *testing.T) {
		want := errors.New("write boom")
		tr := &scriptTransport{writeErr: want}
		_, _, err := newDialer().Dial(tr)
		assert.ErrorIs(t, err, want)
	})

	t.Run("read header error", func(t *testing.T) {
		_, _, err := newDialer().Dial(&scriptTransport{readErr: io.ErrUnexpectedEOF})
		assert.ErrorIs(t, err, io.ErrUnexpectedEOF)
	})

	t.Run("read payload error", func(t *testing.T) {
		// A valid bootstrap response header with no body exercises the payload
		// read error path inside readBootFrame.
		hdr := encryptBootHeader(t, boot, false, encodeHeader(t, handshakeRequestHeader()))
		tr := newScriptTransport(FrameBytes{Header: hdr})
		_, _, err := newDialer().Dial(tr)
		assert.ErrorIs(t, err, ErrFrameSizeMismatch)
	})

	t.Run("bad response header class", func(t *testing.T) {
		c1, c2 := net.Pipe()
		defer c1.Close()
		defer c2.Close()
		srv := runDialServer(c2, boot, func(d *dialResponse) {
			d.header = wire.Header{Class: wire.ClassData, Cmd: wire.Cmd(wire.CmdPushStreamData), Size: handshakeSize}
		})
		_, _, err := newDialer().Dial(NewTCP(c1))
		assert.ErrorIs(t, err, errHandshakeRejected)
		assert.NoError(t, <-srv)
	})

	t.Run("short response payload", func(t *testing.T) {
		c1, c2 := net.Pipe()
		defer c1.Close()
		defer c2.Close()
		srv := runDialServer(c2, boot, func(d *dialResponse) {
			d.header = wire.Header{Class: wire.ClassHandshake, Cmd: wire.Cmd(wire.CmdAdmit), Size: 100}
			d.payload = make([]byte, 100)
		})
		_, _, err := newDialer().Dial(NewTCP(c1))
		assert.ErrorIs(t, err, errHandshakeRejected)
		assert.NoError(t, <-srv)
	})

	t.Run("low order server key", func(t *testing.T) {
		c1, c2 := net.Pipe()
		defer c1.Close()
		defer c2.Close()
		srv := runDialServer(c2, boot, func(d *dialResponse) {
			d.serverPub = make([]byte, 32)
		})
		_, _, err := newDialer().Dial(NewTCP(c1))
		assert.ErrorIs(t, err, errHandshakeRejected)
		assert.NoError(t, <-srv)
	})

	t.Run("response decrypt error", func(t *testing.T) {
		c1, c2 := net.Pipe()
		defer c1.Close()
		defer c2.Close()
		srv := runDialServer(c2, boot, func(d *dialResponse) {
			d.payload = d.seal(d.plain)
			d.payload[len(d.payload)-1] ^= 0xff
		})
		_, _, err := newDialer().Dial(NewTCP(c1))
		assert.ErrorIs(t, err, errHandshakeRejected)
		assert.NoError(t, <-srv)
	})

	t.Run("response version mismatch", func(t *testing.T) {
		c1, c2 := net.Pipe()
		defer c1.Close()
		defer c2.Close()
		srv := runDialServer(c2, boot, func(d *dialResponse) {
			data := make([]byte, 21)
			data[0] = handshakeVersion + 1
			d.plain, _ = padData(data, responsePlainSize)
		})
		_, _, err := newDialer().Dial(NewTCP(c1))
		assert.ErrorIs(t, err, errHandshakeVersion)
		assert.NoError(t, <-srv)
	})

	t.Run("response short plaintext", func(t *testing.T) {
		c1, c2 := net.Pipe()
		defer c1.Close()
		defer c2.Close()
		srv := runDialServer(c2, boot, func(d *dialResponse) {
			d.plain, _ = padData([]byte{handshakeVersion, 0, 7}, responsePlainSize)
		})
		_, _, err := newDialer().Dial(NewTCP(c1))
		assert.ErrorIs(t, err, errHandshakeRejected)
		assert.NoError(t, <-srv)
	})

	t.Run("response bad padding", func(t *testing.T) {
		c1, c2 := net.Pipe()
		defer c1.Close()
		defer c2.Close()
		srv := runDialServer(c2, boot, func(d *dialResponse) {
			d.plain = make([]byte, responsePlainSize)
			binary.BigEndian.PutUint16(d.plain[:2], 0xffff)
		})
		_, _, err := newDialer().Dial(NewTCP(c1))
		assert.ErrorIs(t, err, errHandshakeRejected)
		assert.NoError(t, <-srv)
	})
}

func TestBootFrameHelpers(t *testing.T) {
	boot := deriveBootKey("frame-psk", KDFHKDF)

	t.Run("write encode error", func(t *testing.T) {
		hdr, err := bootHeaderCipher(boot, true)
		require.NoError(t, err)
		err = writeBootFrame(&scriptTransport{}, hdr, wire.Header{Class: wire.ClassData, Cmd: wire.Cmd(wire.CmdOpenStream)}, nil)
		assert.ErrorIs(t, err, wire.ErrClassMismatch)
	})

	t.Run("peer public key", func(t *testing.T) {
		assert.Nil(t, peerPublicKey([]byte{1, 2, 3}))
		priv, err := ecdh.X25519().GenerateKey(rand.Reader)
		require.NoError(t, err)
		assert.NotNil(t, peerPublicKey(priv.PublicKey().Bytes()))
	})

	t.Run("pad too large", func(t *testing.T) {
		_, err := padData(make([]byte, responsePlainSize), responsePlainSize)
		assert.Error(t, err)
	})

	t.Run("unpad short", func(t *testing.T) {
		_, err := unpadData([]byte{0})
		assert.ErrorIs(t, err, errHandshakeRejected)
	})

	t.Run("unpad bad length", func(t *testing.T) {
		_, err := unpadData([]byte{0xff, 0xff, 1})
		assert.ErrorIs(t, err, errHandshakeRejected)
	})
}

func TestHandshakeCodecs(t *testing.T) {
	t.Run("marshal rejects fields", func(t *testing.T) {
		cases := []handshakeRequest{
			{Domain: strings.Repeat("d", 0x10000), Pub: make([]byte, 32)},
			{Mac: strings.Repeat("m", 0x100), Pub: make([]byte, 32)},
			{Pub: make([]byte, 31)},
		}
		for _, req := range cases {
			_, err := req.marshal()
			assert.ErrorIs(t, err, errHandshakeRejected)
		}
	})

	t.Run("parse request truncations", func(t *testing.T) {
		req := validHandshakeRequest(t)
		data, err := req.marshal()
		require.NoError(t, err)
		padded, err := padData(data, requestPlainSize)
		require.NoError(t, err)

		shortData := [][]byte{{0}, {0, 1}, {0, 1, 0, 0}, {0, 1, 0, 0, 0}}
		for _, d := range shortData {
			plain, err := padData(d, requestPlainSize)
			require.NoError(t, err)
			_, err = parseHandshakeRequest(plain)
			assert.ErrorIs(t, err, errHandshakeRejected)
		}
		for _, plain := range [][]byte{
			{0},
			{0xff, 0xff, 1},
			padded[:3],
			padded[:5],
			padded[:8],
			padded[:12],
			padded[:14],
			padded[:20],
			padded[:30],
			padded[:75],
			padded,
		} {
			_, err := parseHandshakeRequest(plain)
			if len(plain) == len(padded) {
				assert.NoError(t, err)
				continue
			}
			assert.Error(t, err)
		}

		// A valid record whose declared domain length exceeds the body.
		badDomain := append([]byte(nil), padded...)
		binary.BigEndian.PutUint16(badDomain[4:6], 0xff)
		_, err = parseHandshakeRequest(badDomain)
		assert.Error(t, err)

		// A valid record whose declared MAC length exceeds the body.
		badMac := append([]byte(nil), padded...)
		dlen := int(binary.BigEndian.Uint16(badMac[4:6]))
		badMac[6+dlen] = 0xff
		_, err = parseHandshakeRequest(badMac)
		assert.Error(t, err)
	})

	t.Run("parse response", func(t *testing.T) {
		_, _, err := parseHandshakeResponse([]byte{0})
		assert.ErrorIs(t, err, errHandshakeRejected)

		plain, err := padData(make([]byte, 20), responsePlainSize)
		require.NoError(t, err)
		_, _, err = parseHandshakeResponse(plain)
		assert.ErrorIs(t, err, errHandshakeRejected)

		badVersion := make([]byte, 21)
		badVersion[0] = handshakeVersion + 1
		plain, err = padData(badVersion, responsePlainSize)
		require.NoError(t, err)
		_, _, err = parseHandshakeResponse(plain)
		assert.ErrorIs(t, err, errHandshakeVersion)

		okData := make([]byte, 21)
		okData[0] = handshakeVersion
		binary.BigEndian.PutUint16(okData[1:3], 77)
		plain, err = padData(okData, responsePlainSize)
		require.NoError(t, err)
		ip, nonce, err := parseHandshakeResponse(plain)
		require.NoError(t, err)
		assert.Equal(t, uint16(77), ip)
		assert.Len(t, nonce, 16)
	})
}

func TestReplayCacheEvictionAndCapacity(t *testing.T) {
	t.Run("eviction", func(t *testing.T) {
		c := newReplayCache(time.Minute, 10)
		t0 := time.Unix(1000, 0)
		assert.True(t, c.add([16]byte{1}, t0))
		assert.False(t, c.add([16]byte{1}, t0.Add(time.Second)))
		// Two TTLs later the old entry is past its TTL and is evicted.
		assert.True(t, c.add([16]byte{2}, t0.Add(2*time.Minute)))
		assert.True(t, c.add([16]byte{1}, t0.Add(2*time.Minute)))
	})

	t.Run("full cache fails closed", func(t *testing.T) {
		c := newReplayCache(time.Minute, 1)
		now := time.Unix(2000, 0)
		assert.True(t, c.add([16]byte{1}, now))
		assert.False(t, c.add([16]byte{2}, now))
	})
}

// sealBootHeader encrypts a bootstrap header but leaves the body untouched.
// It is used for malformed requests where the body is deliberately invalid.
func sealBootHeader(t *testing.T, boot []byte, h wire.Header, payload []byte) FrameBytes {
	t.Helper()
	var plain [wire.HeaderSize]byte
	require.NoError(t, (wire.Codec{}).EncodeHeader(h, plain[:]))
	return FrameBytes{Header: encryptBootHeader(t, boot, true, plain), Payload: payload}
}

func encryptBootHeader(t *testing.T, boot []byte, clientToServer bool, plain [wire.HeaderSize]byte) [wire.HeaderSize]byte {
	t.Helper()
	cipher, err := bootHeaderCipher(boot, clientToServer)
	require.NoError(t, err)
	var out [wire.HeaderSize]byte
	copy(out[:], plain[:])
	cipher.XORKeyStream(out[:], out[:])
	return out
}

func TestHandshakeRandFailures(t *testing.T) {
	const psk = "rand-psk"
	acceptFrame := buildRequestFrame(t, psk, KDFHKDF, validHandshakeRequest(t))

	// crypto/rand.Read intentionally crashes the process on a failing source
	// (Go issue 66821), so only the X25519 key-generation paths, which return
	// the reader error, are exercised here. The source is replaced only for
	// the duration of each subtest.
	t.Run("dial keygen", func(t *testing.T) {
		failRandAfter(t, 0)
		d := NewDialer(DialConfig{PSK: psk, KDF: KDFHKDF, Domain: "client", Timeout: time.Second})
		_, _, err := d.Dial(&scriptTransport{})
		assert.ErrorIs(t, err, errRandSource)
	})

	t.Run("accept server keygen", func(t *testing.T) {
		failRandAfter(t, 0)
		acceptor := NewAcceptor(AcceptConfig{PSK: psk, KDF: KDFHKDF, Timeout: time.Second})
		_, _, err := acceptor.Accept(newScriptTransport(acceptFrame))
		assert.ErrorIs(t, err, errRandSource)
	})
}
