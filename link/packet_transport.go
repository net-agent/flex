package link

import (
	"net"
	"time"

	"github.com/net-agent/flex/v3/packet"
)

// packetTransport adapts a custom packet.Conn that does not implement the
// shared framing.Transport. Built-in packet.Conn implementations implement it
// directly, so this fallback is only for third-party transports; it copies
// payloads because the old ReadBuffer/WriteBuffer API has no two-phase body
// ownership.
type packetTransport struct {
	pc      packet.Conn
	pending *packet.Buffer
}

// NewPacketTransport adapts a legacy packet.Conn. Prefer passing a conn that
// implements framing.Transport directly to avoid the payload copy.
func NewPacketTransport(pc packet.Conn) Transport {
	return &packetTransport{pc: pc}
}

func (t *packetTransport) ReadHeader(dst []byte) error {
	if t.pending != nil {
		return ErrFrameNotConsumed
	}
	b, err := t.pc.ReadBuffer()
	if err != nil {
		return err
	}
	if len(b.Head) < 11 {
		packet.PutBuffer(b)
		return ErrShortFrame
	}
	copy(dst[:11], b.Head[:])
	t.pending = b
	return nil
}

func (t *packetTransport) ReadPayload(n int) ([]byte, error) {
	b := t.pending
	t.pending = nil
	if b == nil {
		return nil, ErrShortFrame
	}
	if len(b.Payload) != n {
		packet.PutBuffer(b)
		return nil, ErrFrameSizeMismatch
	}
	p := append([]byte(nil), b.Payload...)
	packet.PutBuffer(b)
	return p, nil
}

func (t *packetTransport) ReadPayloadInto(dst []byte) error {
	b := t.pending
	t.pending = nil
	if b == nil {
		return ErrShortFrame
	}
	if len(b.Payload) != len(dst) {
		packet.PutBuffer(b)
		return ErrFrameSizeMismatch
	}
	copy(dst, b.Payload)
	packet.PutBuffer(b)
	return nil
}

func (t *packetTransport) WriteFrames(frames []FrameBytes) error {
	for i := range frames {
		b := &packet.Buffer{Head: frames[i].Header, Payload: frames[i].Payload}
		if err := t.pc.WriteBuffer(b); err != nil {
			return err
		}
	}
	return nil
}

func (t *packetTransport) SetReadDeadline(deadline time.Time) error {
	return t.pc.SetReadTimeout(until(deadline))
}

func (t *packetTransport) SetWriteDeadline(deadline time.Time) error {
	t.pc.SetWriteTimeout(until(deadline))
	return nil
}

func (t *packetTransport) LocalAddr() net.Addr {
	if raw := t.pc.GetRawConn(); raw != nil {
		return raw.LocalAddr()
	}
	return nil
}

func (t *packetTransport) RemoteAddr() net.Addr {
	if raw := t.pc.GetRawConn(); raw != nil {
		return raw.RemoteAddr()
	}
	return nil
}

func (t *packetTransport) Close() error {
	if t.pending != nil {
		packet.PutBuffer(t.pending)
		t.pending = nil
	}
	return t.pc.Close()
}

func until(deadline time.Time) time.Duration {
	if deadline.IsZero() {
		return 0
	}
	return time.Until(deadline)
}
