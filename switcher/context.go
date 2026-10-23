package switcher

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v3/internal/idpool"
	"github.com/net-agent/flex/v3/internal/sched"
	"github.com/net-agent/flex/v3/link"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/wire"
)

var (
	errPingWriteFailed = errors.New("ping write buffer failed")
	errPingTimeout     = errors.New("ping timeout")
	errNilContextConn  = errors.New("context conn is nil")
	errContextClosed   = errors.New("context closed")
)

// Context is one authenticated node attached to the switcher. It owns a
// link.Link; the link owns the transport, the fair writer and (later) the
// secure record layer. The context no longer has its own forward queue: the
// link's writer is the single backpressure/order point.
type Context struct {
	id     int
	Domain string
	Mac    string
	IP     uint16
	logger *slog.Logger

	mu       sync.Mutex
	link     *link.Link
	attached bool

	// ipLease 是该 ctx 虚拟 IP 的租约。释放走 Lease.Release（幂等），
	// 避免 detach / 域名替换两条路径各释放一次导致 IP 池记账漂移。
	ipLease *idpool.Lease

	// ready is closed after the handshake response has been written. Peers may
	// resolve this context before that point; SendFrame waits for ready so the
	// response is never preceded by a forwarded data frame.
	ready     chan struct{}
	readyOnce sync.Once
	done      chan struct{}
	closeOnce sync.Once

	AttachTime time.Time
	DetachTime time.Time

	pingIndex atomic.Int32
	pingBack  sync.Map
	Stats     ContextStats
}

type ContextStats struct {
	StreamCount   int32
	BytesReceived int64
	BytesSent     int64
	LastRTT       int64 // nanoseconds, use atomic access
}

// NewContext is the legacy constructor. It wraps conn in a plaintext link and
// marks the context ready immediately; the server uses newContext so it can
// write the handshake response before forwarding starts.
func NewContext(id int, conn packet.Conn, domain, mac string, logger *slog.Logger) *Context {
	var l *link.Link
	if conn != nil {
		var t link.Transport
		if lt, ok := conn.(link.Transport); ok {
			t = lt
		} else {
			t = link.NewPacketTransport(conn)
		}
		l = link.NewDirectWithConfig(t, link.Config{
			Scheduler:  sched.DefaultSchedulerConfig(),
			LegacyWire: true,
		})
	}
	ctx := newContext(id, l, domain, mac, logger)
	ctx.markReady()
	return ctx
}

func newContext(id int, l *link.Link, domain, mac string, logger *slog.Logger) *Context {
	if logger == nil {
		logger = slog.Default()
	}
	return &Context{
		id:         id,
		Domain:     domain,
		Mac:        mac,
		logger:     logger,
		link:       l,
		ready:      make(chan struct{}),
		done:       make(chan struct{}),
		AttachTime: time.Now(),
	}
}

func (ctx *Context) GetID() int { return ctx.id }

// markReady allows forwarded frames to be written.
func (ctx *Context) markReady() { ctx.readyOnce.Do(func() { close(ctx.ready) }) }

func (ctx *Context) isAttached() bool {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	return ctx.attached
}

func (ctx *Context) setAttached(v bool) {
	ctx.mu.Lock()
	ctx.attached = v
	ctx.mu.Unlock()
}

// SendFrame takes ownership of f. It waits for the handshake response window
// to close before writing, so a forwarded packet can never precede the
// handshake response.
func (ctx *Context) SendFrame(f *wire.Frame) error {
	if f == nil {
		return nil
	}
	select {
	case <-ctx.ready:
	case <-ctx.done:
		f.Release()
		return errContextClosed
	}
	if ctx.link == nil {
		f.Release()
		return errNilContextConn
	}
	atomic.AddInt64(&ctx.Stats.BytesSent, int64(f.PayloadSize()+wire.HeaderSize))
	return ctx.link.SendFrame(f)
}

// SendControl builds a control frame and sends it. The payload is copied by the
// frame constructor's caller contract; this helper copies explicitly so the
// caller's slice stays reusable.
func (ctx *Context) SendControl(cmd wire.ControlCmd, src, dst wire.Addr, payload []byte) error {
	f, err := wire.NewControl(cmd, src, dst, append([]byte(nil), payload...))
	if err != nil {
		return err
	}
	return ctx.SendFrame(f)
}

// readFrame reads the next frame from the link.
func (ctx *Context) readFrame() (*wire.Frame, error) {
	if ctx.link == nil {
		return nil, errNilContextConn
	}
	return ctx.link.ReadFrame()
}

// recordIncoming updates receive stats for an incoming frame.
func (ctx *Context) recordIncoming(f *wire.Frame) {
	atomic.AddInt64(&ctx.Stats.BytesReceived, int64(f.PayloadSize()+wire.HeaderSize))
	switch f.Cmd() {
	case wire.Cmd(wire.CmdOpenStream), wire.Cmd(wire.AckOpenStream):
		// 一条流在两端各有一个端点：发起方发 Open，接收方回 OpenAck，
		// 两个方向都计数，才能得到该节点真实的活跃流数量。
		ctx.addStream(1)
	case wire.Cmd(wire.CmdCloseStream), wire.Cmd(wire.AckCloseStream):
		ctx.addStream(-1)
	}
}

// addStream 原子增减活跃流计数，并钳制下界为 0。
func (ctx *Context) addStream(delta int32) int32 {
	for {
		cur := atomic.LoadInt32(&ctx.Stats.StreamCount)
		next := cur + delta
		if next < 0 {
			next = 0
		}
		if atomic.CompareAndSwapInt32(&ctx.Stats.StreamCount, cur, next) {
			return next
		}
	}
}

func (ctx *Context) streamCount() int32 {
	return atomic.LoadInt32(&ctx.Stats.StreamCount)
}

func (ctx *Context) release() {
	ctx.closeOnce.Do(func() {
		close(ctx.done)
		// Unblock any SendFrame waiting for the handshake window.
		ctx.readyOnce.Do(func() { close(ctx.ready) })
		if ctx.link != nil {
			_ = ctx.link.Close()
		}
		ctx.setAttached(false)
		ctx.DetachTime = time.Now()
	})
}

// deliverPingResponse delivers a ping ACK to the waiting ping call.
// 返回 true 仅当 f 真正送入等待通道（所有权转移给 ping 调用方）。
func (ctx *Context) deliverPingResponse(port uint16, f *wire.Frame) bool {
	it, found := ctx.pingBack.Load(port)
	if !found {
		return false
	}
	ch, ok := it.(chan *wire.Frame)
	if !ok {
		return false
	}
	select {
	case ch <- f:
		return true
	default:
		return false
	}
}

func (ctx *Context) ping(timeout time.Duration) (dur time.Duration, retErr error) {
	port := uint16(0xffff & ctx.pingIndex.Add(1))

	f, err := wire.NewControl(
		wire.CmdPingDomain,
		wire.Addr{IP: wire.MaxIP, Port: port},
		wire.Addr{IP: ctx.IP},
		[]byte(ctx.Domain),
	)
	if err != nil {
		return 0, fmt.Errorf("%w: %v", errPingWriteFailed, err)
	}

	ch := make(chan *wire.Frame, 1) // buffered to prevent goroutine leak
	ctx.pingBack.Store(port, ch)
	defer func() {
		ctx.pingBack.Delete(port)
		close(ch)
	}()

	pingStart := time.Now()
	if err := ctx.SendFrame(f); err != nil {
		return 0, errPingWriteFailed
	}

	select {
	case res := <-ch:
		info := string(res.Payload())
		res.Release()
		if info != "" {
			return 0, fmt.Errorf("ping response: %v", info)
		}
	case <-time.After(timeout):
		return 0, errPingTimeout
	}

	dur = time.Since(pingStart)
	atomic.StoreInt64(&ctx.Stats.LastRTT, dur.Nanoseconds())
	return dur, nil
}

// readBuffer is a legacy compatibility helper for tests that still speak
// packet.Buffer. New code uses readFrame.
func (ctx *Context) readBuffer() (*packet.Buffer, error) {
	f, err := ctx.readFrame()
	if err != nil {
		return nil, err
	}
	defer f.Release()
	b := packet.GetBuffer()
	if err := (wire.Codec{}).EncodeHeaderLegacy(f.Header(), b.Head[:]); err != nil {
		packet.PutBuffer(b)
		return nil, err
	}
	b.Payload = append(b.Payload[:0], f.Payload()...)
	return b, nil
}
