package link

import (
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"

	"golang.org/x/crypto/chacha20"
	"golang.org/x/crypto/chacha20poly1305"

	"github.com/net-agent/flex/v3/wire"
)

const (
	controlPadTo   = 64
	controlNonceSz = chacha20poly1305.NonceSize
	controlTagSz   = chacha20poly1305.Overhead
)

var errControlPayload = errors.New("link: invalid control payload")

// secureState holds per-direction header keystreams and control-payload AEADs.
// Header ciphers advance once per frame on a link; read and write each have a
// single owner goroutine, so no locks are needed.
type secureState struct {
	sendHdr *chacha20.Cipher
	recvHdr *chacha20.Cipher
	sendCtl cipher.AEAD
	recvCtl cipher.AEAD
}

func newSecureState(linkKey []byte, initiator bool) (*secureState, error) {
	hdrC2S := hkdfKey(linkKey, "flex-hdr-c2s", nil)
	hdrS2C := hkdfKey(linkKey, "flex-hdr-s2c", nil)
	ctlC2S := hkdfKey(linkKey, "flex-ctl-c2s", nil)
	ctlS2C := hkdfKey(linkKey, "flex-ctl-s2c", nil)

	var zero [chacha20.NonceSize]byte
	sendHdrKey, recvHdrKey := hdrC2S, hdrS2C
	ctlSendKey, ctlRecvKey := ctlC2S, ctlS2C
	if !initiator {
		sendHdrKey, recvHdrKey = hdrS2C, hdrC2S
		ctlSendKey, ctlRecvKey = ctlS2C, ctlC2S
	}

	sendHdr, err := chacha20.NewUnauthenticatedCipher(sendHdrKey, zero[:])
	if err != nil {
		return nil, err
	}
	recvHdr, err := chacha20.NewUnauthenticatedCipher(recvHdrKey, zero[:])
	if err != nil {
		return nil, err
	}
	sendCtl, err := chacha20poly1305.New(ctlSendKey)
	if err != nil {
		return nil, err
	}
	recvCtl, err := chacha20poly1305.New(ctlRecvKey)
	if err != nil {
		return nil, err
	}
	return &secureState{sendHdr: sendHdr, recvHdr: recvHdr, sendCtl: sendCtl, recvCtl: recvCtl}, nil
}

// sealControl encrypts a control payload. The plaintext is framed as
// [2B length][payload][padding] and padded to a 64-byte bucket. The header
// (including the final wire size) is the AEAD associated data.
func (s *secureState) sealControl(h wire.Header, plaintext []byte) (wire.Header, []byte, error) {
	total := 2 + len(plaintext)
	padded := (total + controlPadTo - 1) / controlPadTo * controlPadTo
	wireSize := controlNonceSz + padded + controlTagSz
	if wireSize > wire.MaxPayloadSize {
		return h, nil, fmt.Errorf("%w: control payload too large", errControlPayload)
	}
	h.Size = uint16(wireSize)

	var aad [wire.HeaderSize]byte
	if err := (wire.Codec{}).EncodeHeader(h, aad[:]); err != nil {
		return h, nil, err
	}

	buf := make([]byte, padded)
	binary.BigEndian.PutUint16(buf[:2], uint16(len(plaintext)))
	copy(buf[2:], plaintext)

	nonce := make([]byte, controlNonceSz)
	if _, err := rand.Read(nonce); err != nil {
		return h, nil, err
	}
	ct := s.sendCtl.Seal(nil, nonce, buf, aad[:])
	out := make([]byte, 0, controlNonceSz+len(ct))
	out = append(out, nonce...)
	out = append(out, ct...)
	return h, out, nil
}

// openControl decrypts a control payload and strips padding.
func (s *secureState) openControl(h wire.Header, payload []byte) ([]byte, error) {
	if len(payload) < controlNonceSz+controlTagSz {
		return nil, fmt.Errorf("%w: short control payload", errControlPayload)
	}
	var aad [wire.HeaderSize]byte
	if err := (wire.Codec{}).EncodeHeader(h, aad[:]); err != nil {
		return nil, err
	}
	nonce := payload[:controlNonceSz]
	ct := payload[controlNonceSz:]
	buf, err := s.recvCtl.Open(nil, nonce, ct, aad[:])
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errControlPayload, err)
	}
	if len(buf) < 2 {
		return nil, fmt.Errorf("%w: short plaintext", errControlPayload)
	}
	n := int(binary.BigEndian.Uint16(buf[:2]))
	if n > len(buf)-2 {
		return nil, fmt.Errorf("%w: bad length %d", errControlPayload, n)
	}
	return buf[2 : 2+n], nil
}

// readSecureFrame reads one encrypted frame.
func (l *Link) readSecureFrame() (*wire.Frame, error) {
	var wireHdr [wire.HeaderSize]byte
	if err := l.t.ReadHeader(wireHdr[:]); err != nil {
		return nil, err
	}
	var hdr [wire.HeaderSize]byte
	l.sec.recvHdr.XORKeyStream(hdr[:], wireHdr[:])
	h, err := (wire.Codec{}).DecodeHeader(hdr[:])
	if err != nil {
		return nil, fmt.Errorf("link: decode secure header: %w", err)
	}
	if h.Class == wire.ClassHandshake {
		return nil, fmt.Errorf("%w: unexpected handshake frame", errHandshakeRejected)
	}

	payload, err := l.t.ReadPayload(h.PayloadLen())
	if err != nil {
		return nil, fmt.Errorf("link: read secure payload: %w", err)
	}
	if len(payload) > 0 && h.Class != wire.ClassData {
		payload, err = l.sec.openControl(h, payload)
		if err != nil {
			return nil, err
		}
	}
	f, err := wire.NewFrame(h.Class, h.Cmd, h.Src, h.Dst, payload, h.AckSize())
	if err != nil {
		return nil, err
	}
	l.fireTap(false, f)
	return f, nil
}

// writeSecureFrames encrypts headers, and control payloads, while leaving data
// payloads untouched.
func (l *Link) writeSecureFrames(frames []*wire.Frame) error {
	if cap(l.out) < len(frames) {
		l.out = make([]FrameBytes, len(frames))
	} else {
		l.out = l.out[:len(frames)]
	}
	for i, f := range frames {
		l.fireTap(true, f)
		h := f.Header()
		payload := f.Payload()

		// Control frames with a body are sealed and padded. Header-only
		// commands (Close/AckClose/AckPush) keep the zero-length wire body;
		// their semantic value lives in the encrypted header.
		if len(payload) > 0 && f.Class() != wire.ClassData {
			var err error
			h, payload, err = l.sec.sealControl(h, payload)
			if err != nil {
				return err
			}
		}

		var hdr [wire.HeaderSize]byte
		if err := (wire.Codec{}).EncodeHeader(h, hdr[:]); err != nil {
			return err
		}
		l.sec.sendHdr.XORKeyStream(hdr[:], hdr[:])
		l.out[i].Header = hdr
		l.out[i].Payload = payload
	}
	return l.t.WriteFrames(l.out)
}
