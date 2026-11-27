package link

import (
	"bytes"
	"crypto/rand"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testLinkKey(t *testing.T) []byte {
	t.Helper()
	key := make([]byte, linkKeyLen)
	_, err := rand.Read(key)
	require.NoError(t, err)
	return key
}

func secureStatePair(t *testing.T) (*secureState, *secureState) {
	t.Helper()
	key := testLinkKey(t)
	client, err := newSecureState(key, true)
	require.NoError(t, err)
	server, err := newSecureState(key, false)
	require.NoError(t, err)
	return client, server
}

func TestSecureStateSealControl(t *testing.T) {
	client, server := secureStatePair(t)
	h := wire.Header{Class: wire.ClassControl, Cmd: wire.Cmd(wire.CmdOpenStream)}

	sealed, payload, err := client.sealControl(h, []byte("payload"))
	require.NoError(t, err)
	assert.Equal(t, uint16(controlNonceSz+controlPadTo+controlTagSz), sealed.Size)
	require.Len(t, payload, controlNonceSz+controlPadTo+controlTagSz)

	plain, err := server.openControl(sealed, payload)
	require.NoError(t, err)
	assert.Equal(t, []byte("payload"), plain)

	// The scratch buffer grows and shrinks without leaking state between
	// calls; every result must still decrypt.
	for _, size := range []int{10, 200, 10} {
		sealed, payload, err = client.sealControl(h, bytes.Repeat([]byte("x"), size))
		require.NoError(t, err)
		plain, err = server.openControl(sealed, payload)
		require.NoError(t, err)
		assert.Len(t, plain, size)
	}

	// A control payload whose padded size exceeds MaxPayloadSize is rejected.
	_, _, err = client.sealControl(h, make([]byte, wire.MaxPayloadSize))
	assert.ErrorIs(t, err, errControlPayload)

	// Invalid headers fail during AAD encoding.
	_, _, err = client.sealControl(wire.Header{Class: wire.ClassData, Cmd: wire.Cmd(wire.CmdOpenStream)}, []byte("x"))
	assert.ErrorIs(t, err, wire.ErrClassMismatch)
	_, _, err = client.sealControl(wire.Header{Class: 0, Cmd: wire.Cmd(wire.CmdOpenStream)}, []byte("x"))
	assert.ErrorIs(t, err, wire.ErrInvalidClass)
}

func TestSecureStateOpenControlErrors(t *testing.T) {
	client, server := secureStatePair(t)
	h := wire.Header{Class: wire.ClassControl, Cmd: wire.Cmd(wire.CmdOpenStream)}

	t.Run("short payload", func(t *testing.T) {
		_, err := server.openControl(h, make([]byte, controlNonceSz+controlTagSz-1))
		assert.ErrorIs(t, err, errControlPayload)
	})

	t.Run("invalid header", func(t *testing.T) {
		_, _, err := client.sealControl(h, []byte("x"))
		require.NoError(t, err)
		bad := wire.Header{Class: wire.ClassData, Cmd: wire.Cmd(wire.CmdOpenStream)}
		_, err = server.openControl(bad, make([]byte, controlNonceSz+controlPadTo+controlTagSz))
		assert.ErrorIs(t, err, wire.ErrClassMismatch)
		_, err = server.openControl(wire.Header{Class: 0, Cmd: wire.Cmd(wire.CmdOpenStream)}, make([]byte, controlNonceSz+controlPadTo+controlTagSz))
		assert.ErrorIs(t, err, wire.ErrInvalidClass)
	})

	t.Run("tampered ciphertext", func(t *testing.T) {
		sealed, payload, err := client.sealControl(h, []byte("x"))
		require.NoError(t, err)
		payload[len(payload)-1] ^= 0xff
		_, err = server.openControl(sealed, payload)
		assert.ErrorIs(t, err, errControlPayload)
	})

	// sealRaw seals an arbitrary plaintext with the client's control key, which
	// is the server's receive key.
	sealRaw := func(plain []byte) (wire.Header, []byte) {
		var aad [wire.HeaderSize]byte
		require.NoError(t, (wire.Codec{}).EncodeHeader(h, aad[:]))
		var nonce [controlNonceSz]byte
		copy(nonce[:], "manualnonce!")
		ct := client.sendCtl.Seal(nil, nonce[:], plain, aad[:])
		return h, append(nonce[:], ct...)
	}

	t.Run("short plaintext", func(t *testing.T) {
		hdr, payload := sealRaw([]byte{0})
		_, err := server.openControl(hdr, payload)
		assert.ErrorIs(t, err, errControlPayload)
	})

	t.Run("bad plaintext length", func(t *testing.T) {
		hdr, payload := sealRaw([]byte{0, 5, 1, 2})
		_, err := server.openControl(hdr, payload)
		assert.ErrorIs(t, err, errControlPayload)
	})
}

// encryptSecureHeader turns a plaintext header into wire bytes for the peer's
// receive cipher. peer is a fresh non-initiator state whose sendHdr matches the
// initiator link's recvHdr.
func encryptSecureHeader(t *testing.T, peer *secureState, h wire.Header) [wire.HeaderSize]byte {
	t.Helper()
	var plain [wire.HeaderSize]byte
	require.NoError(t, (wire.Codec{}).EncodeHeader(h, plain[:]))
	peer.sendHdr.XORKeyStream(plain[:], plain[:])
	return plain
}

func TestReadSecureFrameErrors(t *testing.T) {
	key := testLinkKey(t)

	newLink := func(tr Transport) (*Link, *secureState) {
		lnk, err := NewSecure(tr, key, true, Config{})
		require.NoError(t, err)
		peer, err := newSecureState(key, false)
		require.NoError(t, err)
		t.Cleanup(func() { _ = lnk.Close() })
		return lnk, peer
	}

	t.Run("header read error", func(t *testing.T) {
		want := errBoom()
		lnk, _ := newLink(&scriptTransport{readErr: want})
		_, err := lnk.ReadFrame()
		assert.ErrorIs(t, err, want)
	})

	t.Run("decode error", func(t *testing.T) {
		tr := &scriptTransport{}
		lnk, peer := newLink(tr)
		// A control command without the class bit is rejected by the decoder.
		var plain [wire.HeaderSize]byte
		plain[0] = byte(wire.CmdOpenStream)
		peer.sendHdr.XORKeyStream(plain[:], plain[:])
		tr.frames = append(tr.frames, scriptFrame{header: plain})
		_, err := lnk.ReadFrame()
		assert.ErrorIs(t, err, wire.ErrClassMismatch)
	})

	t.Run("handshake frame rejected", func(t *testing.T) {
		tr := &scriptTransport{}
		lnk, peer := newLink(tr)
		hdr := encryptSecureHeader(t, peer, wire.Header{Class: wire.ClassHandshake, Cmd: wire.Cmd(wire.CmdAdmit)})
		tr.frames = append(tr.frames, scriptFrame{header: hdr})
		_, err := lnk.ReadFrame()
		assert.ErrorIs(t, err, errHandshakeRejected)
	})

	t.Run("payload read error", func(t *testing.T) {
		tr := &scriptTransport{}
		lnk, peer := newLink(tr)
		hdr := encryptSecureHeader(t, peer, wire.Header{
			Class: wire.ClassData, Cmd: wire.Cmd(wire.CmdPushStreamData), Size: 3,
		})
		tr.frames = append(tr.frames, scriptFrame{header: hdr})
		_, err := lnk.ReadFrame()
		assert.ErrorIs(t, err, ErrFrameSizeMismatch)
	})

	t.Run("control decrypt error", func(t *testing.T) {
		tr := &scriptTransport{}
		lnk, peer := newLink(tr)
		hdr := encryptSecureHeader(t, peer, wire.Header{
			Class: wire.ClassControl, Cmd: wire.Cmd(wire.CmdOpenStream), Size: controlNonceSz + controlTagSz,
		})
		tr.frames = append(tr.frames, scriptFrame{header: hdr, payload: make([]byte, controlNonceSz+controlTagSz)})
		_, err := lnk.ReadFrame()
		assert.ErrorIs(t, err, errControlPayload)
	})

	t.Run("data payload passes through", func(t *testing.T) {
		tr := &scriptTransport{}
		lnk, peer := newLink(tr)
		hdr := encryptSecureHeader(t, peer, wire.Header{
			Class: wire.ClassData, Cmd: wire.Cmd(wire.CmdPushStreamData), Size: 4,
		})
		tr.frames = append(tr.frames, scriptFrame{header: hdr, payload: []byte("data")})
		f, err := lnk.ReadFrame()
		require.NoError(t, err)
		assert.Equal(t, []byte("data"), f.Payload())
		f.Release()
	})
}

func TestWriteSecureFramesSealError(t *testing.T) {
	tr := &scriptTransport{closeCh: make(chan struct{})}
	lnk, err := NewSecure(tr, testLinkKey(t), true, Config{})
	require.NoError(t, err)
	defer lnk.Close()

	// MaxPayloadSize is accepted by the frame constructor, but the padding and
	// AEAD overhead push the sealed control body past the wire limit.
	f, err := wire.NewControl(wire.CmdOpenStream, wire.Addr{IP: 1}, wire.Addr{IP: 2}, make([]byte, wire.MaxPayloadSize))
	require.NoError(t, err)
	err = lnk.SendFrameSync(f)
	assert.ErrorIs(t, err, errControlPayload)
	// A write failure is fatal: the transport is closed so readers unblock.
	select {
	case <-tr.closeCh:
	case <-time.After(time.Second):
		t.Fatal("transport was not closed after the write failure")
	}
}
