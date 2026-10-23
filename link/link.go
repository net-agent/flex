package link

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/net-agent/flex/v3/internal/sched"
	"github.com/net-agent/flex/v3/wire"
)

// Link is one established flex connection. It owns the transport, the write
// scheduler and (once the handshake lands) the secure record layer. Callers
// only see frames.
type Link struct {
	t     Transport
	codec wire.Codec
	w     *fairWriter

	// out is writer-goroutine scratch for header encoding. It is never touched
	// by readers, so no lock is needed.
	out []FrameBytes

	closeOnce sync.Once
	closeErr  error
}

// NewDirect wraps a transport without a handshake or encryption. It is the
// direct node-to-node mode and the plaintext foundation used by tests. The
// handshake/secure constructors will be added on top of the same Link.
func NewDirect(t Transport, cfg ...sched.SchedulerConfig) *Link {
	c := sched.DefaultSchedulerConfig()
	if len(cfg) > 0 {
		c = cfg[0]
	}
	l := &Link{t: t}
	l.w = newFairWriter(l.writeFrames, c, func(error) {
		// A write failure is fatal for the connection. Close the transport so
		// the read loop unblocks; the caller observes the error on ReadFrame.
		_ = t.Close()
	})
	return l
}

// Transport returns the underlying transport (handshake code uses it).
func (l *Link) Transport() Transport { return l.t }

// ReadFrame reads one frame. The returned frame is owned by the caller and must
// be released.
func (l *Link) ReadFrame() (*wire.Frame, error) {
	var hdr [wire.HeaderSize]byte
	if err := l.t.ReadHeader(hdr[:]); err != nil {
		return nil, err
	}
	h, err := l.codec.DecodeHeader(hdr[:])
	if err != nil {
		return nil, fmt.Errorf("link: decode header: %w", err)
	}
	payload, err := l.t.ReadPayload(h.PayloadLen())
	if err != nil {
		return nil, fmt.Errorf("link: read payload: %w", err)
	}
	f, err := wire.NewFrame(h.Class, h.Cmd, h.Src, h.Dst, payload, h.AckSize())
	if err != nil {
		return nil, fmt.Errorf("link: build frame: %w", err)
	}
	return f, nil
}

// SendFrame transfers ownership of f to the link's writer. The caller must not
// use f after this call.
func (l *Link) SendFrame(f *wire.Frame) error {
	if f == nil {
		return nil
	}
	return l.w.Send(f)
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
	if cap(l.out) < len(frames) {
		l.out = make([]FrameBytes, len(frames))
	} else {
		l.out = l.out[:len(frames)]
	}
	for i, f := range frames {
		if err := l.codec.EncodeHeader(f.Header(), l.out[i].Header[:]); err != nil {
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
