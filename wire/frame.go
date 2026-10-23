package wire

import (
	"errors"
	"fmt"
	"sync"
)

var (
	// ErrUnknownCommand is returned when a command byte is not registered.
	ErrUnknownCommand = errors.New("wire: unknown command")
	// ErrClassMismatch is returned when a command is used with the wrong class.
	ErrClassMismatch = errors.New("wire: command class mismatch")
	// ErrInvalidClass is returned for a class value outside the known set.
	ErrInvalidClass = errors.New("wire: invalid class")
	// ErrPayloadOverflow is returned when a payload exceeds MaxPayloadSize.
	ErrPayloadOverflow = errors.New("wire: payload exceeds MaxPayloadSize")
	// ErrPayloadNotAllowed is returned when a no-payload command carries data.
	ErrPayloadNotAllowed = errors.New("wire: payload not allowed for command")
	// ErrShortHeader is returned when a header buffer is shorter than HeaderSize.
	ErrShortHeader = errors.New("wire: short header")
)

// Frame is an immutable flex packet: header fields are set once at construction
// and can only be read afterwards. Payload ownership transfers to the frame;
// call Release when done.
//
// The immutability is deliberate. Handlers build a reply Frame instead of
// mutating the request, so a command can never drift into a different class or
// lose the ACK bit halfway through a forwarding path.
type Frame struct {
	class   Class
	cmd     Cmd
	src     Addr
	dst     Addr
	payload []byte
	ackSize uint16 // only for AckPushStreamData
	pooled  bool
}

var framePool = sync.Pool{
	New: func() any { return &Frame{} },
}

func getFrame() *Frame {
	f := framePool.Get().(*Frame)
	f.pooled = true
	return f
}

// Release returns the frame (and its payload capacity) to the pool. It is
// idempotent and nil-safe. The caller must not use the frame or its payload
// after Release.
func (f *Frame) Release() {
	if f == nil || !f.pooled {
		return
	}
	f.pooled = false
	f.class = 0
	f.cmd = 0
	f.src = Addr{}
	f.dst = Addr{}
	f.ackSize = 0
	f.payload = f.payload[:0]
	framePool.Put(f)
}

// NewFrame is the trusted constructor used by the link/codec after validating
// a wire header. It enforces the command/class table and the no-payload rules.
// It takes ownership of payload.
func NewFrame(class Class, cmd Cmd, src, dst Addr, payload []byte, ackSize uint16) (*Frame, error) {
	if !class.valid() {
		return nil, fmt.Errorf("%w: %d", ErrInvalidClass, uint8(class))
	}
	if cmd&Cmd(classFlag) != 0 {
		return nil, fmt.Errorf("%w: class bit set in cmd 0x%02x", ErrUnknownCommand, byte(cmd))
	}
	want, ok := ClassOf(cmd)
	if !ok {
		return nil, fmt.Errorf("%w: 0x%02x", ErrUnknownCommand, byte(cmd))
	}
	if want != class {
		return nil, fmt.Errorf("%w: cmd=%s class=%s want=%s", ErrClassMismatch, cmd, class, want)
	}
	if len(payload) > MaxPayloadSize {
		return nil, fmt.Errorf("%w: %d", ErrPayloadOverflow, len(payload))
	}
	if cmd == Cmd(AckPushStreamData) {
		if len(payload) != 0 {
			return nil, fmt.Errorf("%w: %s", ErrPayloadNotAllowed, cmd)
		}
	} else if len(payload) == 0 && ackSize != 0 {
		return nil, fmt.Errorf("%w: ack size on non-ack command %s", ErrPayloadNotAllowed, cmd)
	} else if len(payload) != 0 && ackSize != 0 {
		return nil, fmt.Errorf("%w: ack size on payload command %s", ErrPayloadNotAllowed, cmd)
	}
	if noPayload(cmd) && len(payload) != 0 {
		return nil, fmt.Errorf("%w: %s", ErrPayloadNotAllowed, cmd)
	}

	f := getFrame()
	f.class = class
	f.cmd = cmd
	f.src = src
	f.dst = dst
	f.payload = payload
	f.ackSize = ackSize
	return f, nil
}

// NewControl builds a control frame whose payload the link will encrypt.
func NewControl(cmd ControlCmd, src, dst Addr, payload []byte) (*Frame, error) {
	return NewFrame(ClassControl, Cmd(cmd), src, dst, payload, 0)
}

// NewData builds an opaque data frame. The link never inspects its payload.
func NewData(src, dst Addr, payload []byte) (*Frame, error) {
	return NewFrame(ClassData, Cmd(CmdPushStreamData), src, dst, payload, 0)
}

// NewEmpty builds a command with no payload.
func NewEmpty(cmd EmptyCmd, src, dst Addr) (*Frame, error) {
	c := Cmd(cmd)
	if c == Cmd(AckPushStreamData) {
		return nil, fmt.Errorf("%w: use NewAckData for %s", ErrPayloadNotAllowed, c)
	}
	class, ok := ClassOf(c)
	if !ok {
		return nil, fmt.Errorf("%w: 0x%02x", ErrUnknownCommand, byte(c))
	}
	if class == ClassHandshake {
		return nil, fmt.Errorf("%w: handshake command %s is not routable", ErrClassMismatch, c)
	}
	return NewFrame(class, c, src, dst, nil, 0)
}

// NewAckData builds an AckPushStreamData frame. Its header payload-size field
// carries the acknowledged byte count; there is no payload.
func NewAckData(size uint16, src, dst Addr) *Frame {
	f, err := NewFrame(ClassData, Cmd(AckPushStreamData), src, dst, nil, size)
	if err != nil {
		// Static command/class pair; unreachable unless the table is edited
		// inconsistently. Panicking here turns a protocol-table bug into a
		// startup/test failure rather than a silent wrong packet.
		panic(err)
	}
	return f
}

// NewHandshake builds the bootstrap frame. Only the handshake codec may use it.
func NewHandshake(payload []byte) (*Frame, error) {
	return NewFrame(ClassHandshake, Cmd(CmdAdmit), Addr{}, Addr{}, payload, 0)
}

// Class returns the frame's confidentiality class.
func (f *Frame) Class() Class { return f.class }

// Cmd returns the command byte (including the ACK flag).
func (f *Frame) Cmd() Cmd { return f.cmd }

// Src returns the source virtual address.
func (f *Frame) Src() Addr { return f.src }

// Dst returns the destination virtual address.
func (f *Frame) Dst() Addr { return f.dst }

// SID returns the stream identity (source and destination address).
func (f *Frame) SID() SID { return SID{Src: f.src, Dst: f.dst} }

// Payload returns the frame payload. The slice is read-only and only valid
// until Release.
func (f *Frame) Payload() []byte { return f.payload }

// PayloadSize returns len(Payload) as a uint16. AckPushStreamData has no
// payload and returns 0; use AckSize for its acknowledged byte count.
func (f *Frame) PayloadSize() uint16 { return uint16(len(f.payload)) }

// AckSize returns the acknowledged byte count for AckPushStreamData.
func (f *Frame) AckSize() uint16 {
	if f.cmd != Cmd(AckPushStreamData) {
		return 0
	}
	return f.ackSize
}

// IsAck reports whether the ACK bit is set.
func (f *Frame) IsAck() bool { return f.cmd.IsAck() }

// Header returns the header fields for a plaintext write. The secure link
// overrides Size with the ciphertext length when it encrypts a control payload.
func (f *Frame) Header() Header {
	size := f.PayloadSize()
	if f.cmd == Cmd(AckPushStreamData) {
		size = f.ackSize
	}
	return Header{Class: f.class, Cmd: f.cmd, Src: f.src, Dst: f.dst, Size: size}
}

// String formats a frame for logs.
func (f *Frame) String() string {
	if f.cmd == Cmd(AckPushStreamData) {
		return fmt.Sprintf("[%s][%s][%s->%s][ack=%d]", f.class, f.cmd, f.src, f.dst, f.ackSize)
	}
	return fmt.Sprintf("[%s][%s][%s->%s][size=%d]", f.class, f.cmd, f.src, f.dst, len(f.payload))
}

func noPayload(cmd Cmd) bool {
	switch cmd {
	case Cmd(CmdCloseStream), Cmd(AckCloseStream),
		Cmd(AckPushStreamData), Cmd(AckPushMessage):
		return true
	default:
		return false
	}
}
