package link

import (
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v3/internal/sched"
	"github.com/net-agent/flex/v3/wire"
)

// Link is one established flex connection. It owns the transport, the write
// scheduler and (once the handshake lands) the secure record layer. Callers
// only see frames.
type Link struct {
	t      Transport
	codec  wire.Codec
	w      *fairWriter
	sec    *secureState
	legacy bool // omit/accept the class bit for old packet.Buffer peers

	// out is writer-goroutine scratch for header encoding. It is never touched
	// by readers, so no lock is needed.
	out []FrameBytes

	closeOnce sync.Once
	closeErr  error

	// tap, when set, observes plaintext frames at the link boundary. It is
	// used by tests/observability and is nil in production.
	tap atomic.Pointer[FrameTap]

	// payloadPool recycles receive buffers. A frame read by this link carries
	// its buffer back here when the frame is released.
	payloadPool sync.Pool
}

// FrameTap observes a plaintext frame. write is true for outbound frames.
// The callback must not retain or modify the frame.
type FrameTap func(write bool, f *wire.Frame)

// SetFrameTap installs an optional plaintext frame observer.
func (l *Link) SetFrameTap(fn FrameTap) {
	if fn == nil {
		l.tap.Store(nil)
		return
	}
	l.tap.Store(&fn)
}

func (l *Link) fireTap(write bool, f *wire.Frame) {
	if p := l.tap.Load(); p != nil {
		(*p)(write, f)
	}
}

// NewDirect wraps a transport without a handshake or encryption. It is the
// direct node-to-node mode and the plaintext foundation used by tests. The
// handshake/secure constructors will be added on top of the same Link.
func NewDirect(t Transport, cfg ...sched.SchedulerConfig) *Link {
	c := sched.DefaultSchedulerConfig()
	if len(cfg) > 0 {
		c = cfg[0]
	}
	return NewDirectWithConfig(t, Config{Scheduler: c})
}

// Config configures a Link.
type Config struct {
	Scheduler  sched.SchedulerConfig
	LegacyWire bool // migration only: omit/accept the class bit for old peers
}

// NewDirectWithConfig wraps a transport without a handshake or encryption.
func NewDirectWithConfig(t Transport, cfg Config) *Link {
	return newLink(t, cfg, nil)
}

// NewSecure wraps a transport with the v4 link keys. initiator selects the
// client (true) or server (false) direction of the header/control key pairs.
func NewSecure(t Transport, linkKey []byte, initiator bool, cfg Config) (*Link, error) {
	sec, err := newSecureState(linkKey, initiator)
	if err != nil {
		return nil, err
	}
	return newLink(t, cfg, sec), nil
}

func newLink(t Transport, cfg Config, sec *secureState) *Link {
	l := &Link{t: t, sec: sec, legacy: cfg.LegacyWire && sec == nil}
	l.payloadPool.New = func() any { return new([]byte) }
	l.w = newFairWriter(l.writeFrames, cfg.Scheduler, func(error) {
		// A write failure is fatal for the connection. Close the transport so
		// the read loop unblocks; the caller observes the error on ReadFrame.
		_ = t.Close()
	})
	return l
}

// Transport returns the underlying transport (handshake code uses it).
func (l *Link) Transport() Transport { return l.t }

// SchedulerConfig returns the effective fair-writer policy.
func (l *Link) SchedulerConfig() sched.SchedulerConfig { return l.w.cfg }

// ReadFrame reads one frame. The returned frame is owned by the caller and must
// be released.
func (l *Link) ReadFrame() (*wire.Frame, error) {
	if l.sec != nil {
		return l.readSecureFrame()
	}
	var hdr [wire.HeaderSize]byte
	if err := l.t.ReadHeader(hdr[:]); err != nil {
		return nil, err
	}
	var (
		h   wire.Header
		err error
	)
	if l.legacy {
		h, err = l.codec.DecodeHeaderLegacy(hdr[:])
	} else {
		h, err = l.codec.DecodeHeader(hdr[:])
	}
	if err != nil {
		return nil, fmt.Errorf("link: decode header: %w", err)
	}
	payload, err := l.readPayload(h.PayloadLen())
	if err != nil {
		return nil, fmt.Errorf("link: read payload: %w", err)
	}
	f, err := wire.NewFrameWithRelease(h.Class, h.Cmd, h.Src, h.Dst, payload, h.AckSize(), l.putPayload)
	if err != nil {
		return nil, fmt.Errorf("link: build frame: %w", err)
	}
	l.fireTap(false, f)
	return f, nil
}

// readPayload reads exactly n bytes, preferring the caller-buffer extension to
// avoid a per-packet allocation. The returned buffer is owned by the link pool
// and must be released with putPayload (the frame release callback does it).
func (l *Link) readPayload(n int) ([]byte, error) {
	if n <= 0 {
		return nil, nil
	}
	b := l.getPayload(n)
	if pr, ok := l.t.(interface{ ReadPayloadInto([]byte) error }); ok {
		if err := pr.ReadPayloadInto(b); err != nil {
			l.putPayload(b)
			return nil, err
		}
		return b, nil
	}
	p, err := l.t.ReadPayload(n)
	if err != nil {
		l.putPayload(b)
		return nil, err
	}
	copy(b, p)
	return b, nil
}

func (l *Link) getPayload(n int) []byte {
	v := l.payloadPool.Get().(*[]byte)
	b := *v
	if cap(b) < n {
		return make([]byte, n)
	}
	return b[:n]
}

func (l *Link) putPayload(b []byte) {
	if b == nil {
		return
	}
	b = b[:0]
	l.payloadPool.Put(&b)
}

// SendFrame transfers ownership of f to the link's writer. The caller must not
// use f after this call.
func (l *Link) SendFrame(f *wire.Frame) error {
	if f == nil {
		return nil
	}
	return l.w.Send(f)
}

// SendFrameSync transfers ownership of f and waits until the transport write
// completes, returning the write error. Control packets use it so callers keep
// the old synchronous writer error semantics.
func (l *Link) SendFrameSync(f *wire.Frame) error {
	if f == nil {
		return nil
	}
	return l.w.SendSync(f)
}

// SendControl copies payload and queues a control frame. Copying keeps the
// caller's slice reusable; control packets are small and rare.
func (l *Link) SendControl(cmd wire.ControlCmd, src, dst wire.Addr, payload []byte) error {
	p := append([]byte(nil), payload...)
	f, err := wire.NewControl(cmd, src, dst, p)
	if err != nil {
		return err
	}
	return l.w.Send(f)
}

// SendData copies payload and queues an opaque data frame. The link does not
// inspect or modify the copied bytes.
func (l *Link) SendData(src, dst wire.Addr, payload []byte) error {
	p := append([]byte(nil), payload...)
	f, err := wire.NewData(src, dst, p)
	if err != nil {
		return err
	}
	return l.w.Send(f)
}

// SendAck queues an AckPushStreamData frame carrying the acknowledged bytes.
func (l *Link) SendAck(size uint16, src, dst wire.Addr) error {
	return l.w.Send(wire.NewAckData(size, src, dst))
}

// SendEmpty queues a no-payload command such as Close.
func (l *Link) SendEmpty(cmd wire.EmptyCmd, src, dst wire.Addr) error {
	f, err := wire.NewEmpty(cmd, src, dst)
	if err != nil {
		return err
	}
	return l.w.Send(f)
}

// writeFrames encodes headers and hands the batch to the transport. Only the
// fair writer goroutine calls this.
func (l *Link) writeFrames(frames []*wire.Frame) error {
	if l.sec != nil {
		return l.writeSecureFrames(frames)
	}
	if cap(l.out) < len(frames) {
		l.out = make([]FrameBytes, len(frames))
	} else {
		l.out = l.out[:len(frames)]
	}
	for i, f := range frames {
		l.fireTap(true, f)
		var err error
		if l.legacy {
			err = l.codec.EncodeHeaderLegacy(f.Header(), l.out[i].Header[:])
		} else {
			err = l.codec.EncodeHeader(f.Header(), l.out[i].Header[:])
		}
		if err != nil {
			return err
		}
		l.out[i].Payload = f.Payload()
	}
	return l.t.WriteFrames(l.out)
}

// SetReadTimeout sets the read deadline relative to now. Zero clears it.
func (l *Link) SetReadTimeout(d time.Duration) error {
	if d <= 0 {
		return l.t.SetReadDeadline(time.Time{})
	}
	return l.t.SetReadDeadline(time.Now().Add(d))
}

// SetWriteTimeout sets the write deadline relative to now. Zero clears it.
func (l *Link) SetWriteTimeout(d time.Duration) error {
	if d <= 0 {
		return l.t.SetWriteDeadline(time.Time{})
	}
	return l.t.SetWriteDeadline(time.Now().Add(d))
}

// LocalAddr returns the transport's local address.
func (l *Link) LocalAddr() net.Addr { return l.t.LocalAddr() }

// RemoteAddr returns the transport's remote address.
func (l *Link) RemoteAddr() net.Addr { return l.t.RemoteAddr() }

// Close stops the writer and closes the transport. It is idempotent.
func (l *Link) Close() error {
	l.closeOnce.Do(func() {
		_ = l.w.Close()
		l.closeErr = l.t.Close()
	})
	return l.closeErr
}
