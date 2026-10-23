package wire

import (
	"encoding/binary"
	"fmt"
)

// Header is the decoded 11-byte wire header. Size is the wire payload length
// for ordinary frames and the acknowledged byte count for AckPushStreamData.
type Header struct {
	Class Class
	Cmd   Cmd
	Src   Addr
	Dst   Addr
	Size  uint16
}

// PayloadLen returns the number of bytes that follow the header on the wire.
func (h Header) PayloadLen() int {
	if h.Cmd == Cmd(AckPushStreamData) {
		return 0
	}
	return int(h.Size)
}

// AckSize returns the acknowledged byte count for AckPushStreamData.
func (h Header) AckSize() uint16 {
	if h.Cmd != Cmd(AckPushStreamData) {
		return 0
	}
	return h.Size
}

// String formats a header for logs.
func (h Header) String() string {
	return fmt.Sprintf("[%s][%s][%s->%s][size=%d]", h.Class, h.Cmd, h.Src, h.Dst, h.Size)
}

// Codec encodes and decodes the fixed 11-byte flex header.
//
// Layout:
//
//	+-------+------+--------+-------+--------+---------------------+
//	| Cmd   | DstIP| DstPort| SrcIP | SrcPort| Size                |
//	| 1B    | 2B   | 2B     | 2B    | 2B     | 2B                  |
//	+-------+------+--------+-------+--------+---------------------+
//
// Cmd bit 0 is ACK, bits 1..6 are the command type, bit 7 is the class bit
// (1 = link-encrypted payload, 0 = opaque data).
type Codec struct{}

// EncodeHeader writes h into dst, which must be at least HeaderSize bytes.
func (Codec) EncodeHeader(h Header, dst []byte) error {
	if len(dst) < HeaderSize {
		return ErrShortHeader
	}
	if !h.Class.valid() {
		return fmt.Errorf("%w: %d", ErrInvalidClass, uint8(h.Class))
	}
	if h.Cmd&Cmd(classFlag) != 0 {
		return fmt.Errorf("%w: class bit set in cmd 0x%02x", ErrUnknownCommand, byte(h.Cmd))
	}
	want, ok := ClassOf(h.Cmd)
	if !ok {
		return fmt.Errorf("%w: 0x%02x", ErrUnknownCommand, byte(h.Cmd))
	}
	if want != h.Class {
		return fmt.Errorf("%w: cmd=%s class=%s want=%s", ErrClassMismatch, h.Cmd, h.Class, want)
	}
	if noPayload(h.Cmd) && h.Cmd != Cmd(AckPushStreamData) && h.Size != 0 {
		return fmt.Errorf("%w: %s size=%d", ErrPayloadNotAllowed, h.Cmd, h.Size)
	}

	b := byte(h.Cmd)
	if h.Class.encrypted() {
		b |= classFlag
	}
	dst[0] = b
	binary.BigEndian.PutUint16(dst[1:3], h.Dst.IP)
	binary.BigEndian.PutUint16(dst[3:5], h.Dst.Port)
	binary.BigEndian.PutUint16(dst[5:7], h.Src.IP)
	binary.BigEndian.PutUint16(dst[7:9], h.Src.Port)
	binary.BigEndian.PutUint16(dst[9:11], h.Size)
	return nil
}

// DecodeHeader parses src into a Header. It validates the class bit against the
// command table, so a control command smuggled in a data-class frame (or the
// reverse) is rejected here, before any payload is read.
func (Codec) DecodeHeader(src []byte) (Header, error) {
	if len(src) < HeaderSize {
		return Header{}, ErrShortHeader
	}
	b := src[0]
	classBit := b&classFlag != 0
	cmd := Cmd(b &^ classFlag)

	class, ok := ClassOf(cmd)
	if !ok {
		return Header{}, fmt.Errorf("%w: 0x%02x", ErrUnknownCommand, byte(cmd))
	}
	if class.encrypted() != classBit {
		return Header{}, fmt.Errorf("%w: cmd=%s wire-bit=%v want=%v", ErrClassMismatch, cmd, classBit, class.encrypted())
	}

	h := Header{
		Class: class,
		Cmd:   cmd,
		Dst:   Addr{IP: binary.BigEndian.Uint16(src[1:3]), Port: binary.BigEndian.Uint16(src[3:5])},
		Src:   Addr{IP: binary.BigEndian.Uint16(src[5:7]), Port: binary.BigEndian.Uint16(src[7:9])},
		Size:  binary.BigEndian.Uint16(src[9:11]),
	}
	if noPayload(cmd) && cmd != Cmd(AckPushStreamData) && h.Size != 0 {
		return Header{}, fmt.Errorf("%w: %s size=%d", ErrPayloadNotAllowed, cmd, h.Size)
	}
	return h, nil
}

// EncodeFrame writes a plaintext frame's header into dst. It is a convenience
// for the plaintext transport; the secure link builds the header itself because
// the payload size changes when a control payload is encrypted.
func (Codec) EncodeFrame(f *Frame, dst []byte) error {
	return Codec{}.EncodeHeader(f.Header(), dst)
}
