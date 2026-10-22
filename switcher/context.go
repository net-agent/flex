package switcher

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v3/internal/idpool"
	"github.com/net-agent/flex/v3/packet"
)

var (
	errPingWriteFailed       = errors.New("ping write buffer failed")
	errPingTimeout           = errors.New("ping timeout")
	errNilContextConn        = errors.New("context conn is nil")
	errContextClosed         = errors.New("context closed")
	errForwardEnqueueTimeout = errors.New("forward enqueue timeout")
)

type Context struct {
	id     int
	Domain string
	Mac    string
	IP     uint16
	logger *slog.Logger

	mu       sync.Mutex
	conn     packet.Conn
	attached bool

	// ipLease 是该 ctx 虚拟 IP 的租约。释放走 Lease.Release（幂等），
	// 避免 detach / 域名替换两条路径各释放一次导致 IP 池记账漂移。
	ipLease *idpool.Lease

	forwardCh   chan *packet.Buffer
	forwardDone chan struct{}
	closeOnce   sync.Once

	// connReady 在 setConn 首次绑定非 nil 连接时关闭。
	// 握手应答写完到 setConn 之间，其他节点可能已经命中本 ctx 并转发数据包；
	// 此时不能丢包，转发循环应等待连接就绪后再写出（保证应答先于任何转发）。
	connReady     chan struct{}
	connReadyOnce sync.Once

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

func NewContext(id int, conn packet.Conn, domain, mac string, logger *slog.Logger) *Context {
	if logger == nil {
		logger = slog.Default()
	}
	ctx := &Context{
		id:          id,
		Domain:      domain,
		Mac:         mac,
		IP:          0,
		logger:      logger,
		conn:        conn,
		forwardCh:   make(chan *packet.Buffer, 256),
		forwardDone: make(chan struct{}),
		connReady:   make(chan struct{}),
		AttachTime:  time.Now(),
	}
	go ctx.runForwardLoop()
	return ctx
}

func (ctx *Context) GetID() int {
	return ctx.id
}

func (ctx *Context) getConn() packet.Conn {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	return ctx.conn
}

func (ctx *Context) setConn(c packet.Conn) {
	ctx.mu.Lock()
	ctx.conn = c
	if c != nil {
		ctx.connReadyOnce.Do(func() {
			if ctx.connReady != nil {
				close(ctx.connReady)
			}
		})
	}
	ctx.mu.Unlock()
}

func (ctx *Context) isAttached() bool {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	return ctx.attached
}

func (ctx *Context) setAttached(v bool) {
	ctx.mu.Lock()
	defer ctx.mu.Unlock()
	ctx.attached = v
}

func (ctx *Context) readBuffer() (*packet.Buffer, error) {
	c := ctx.getConn()
	if c == nil {
		return nil, errNilContextConn
	}
	return c.ReadBuffer()
}

// ownedBufferWriter 由 sched.FairConn 实现：调用方把 buffer 所有权转移给写方，
// 写方在异步写完后归还 packet 池。返回错误表示所有权未转移（回收责任仍在调用方）。
type ownedBufferWriter interface {
	WriteBufferOwned(buf *packet.Buffer) error
}

// writeBuffer 直接写底层连接。它是受限出口：只允许 runForwardLoop 调用，
// 其他所有写路径必须走 enqueueForward，保证 per-context 的包序与背压策略收口在一处。
//
// 同时它是 buffer 生命周期的统一终点：fair 连接转移所有权（FairWriter 写后归还），
// 裸连接同步写完后就地归还；失败时 buffer 未转移，也在此归还。
func (ctx *Context) writeBuffer(buf *packet.Buffer) error {
	c := ctx.getConn()
	if c == nil {
		// 连接尚未绑定（握手应答与 setConn 之间的窗口）：等待就绪而不是丢包。
		if ctx.connReady == nil {
			packet.PutBuffer(buf)
			return errNilContextConn
		}
		select {
		case <-ctx.connReady:
			c = ctx.getConn()
		case <-ctx.forwardDone:
			packet.PutBuffer(buf)
			return errContextClosed
		}
	}
	if c == nil {
		packet.PutBuffer(buf)
		return errNilContextConn
	}

	// Update stats
	atomic.AddInt64(&ctx.Stats.BytesSent, int64(buf.PayloadSize()+packet.HeaderSz))

	if ow, ok := c.(ownedBufferWriter); ok {
		if err := ow.WriteBufferOwned(buf); err != nil {
			packet.PutBuffer(buf)
			return err
		}
		return nil
	}

	err := c.WriteBuffer(buf)
	packet.PutBuffer(buf)
	return err
}

// recordIncoming updates receive stats for an incoming packet.
func (ctx *Context) recordIncoming(pbuf *packet.Buffer) {
	atomic.AddInt64(&ctx.Stats.BytesReceived, int64(pbuf.PayloadSize()+packet.HeaderSz))
	switch pbuf.Cmd() {
	case packet.CmdOpenStream, packet.AckOpenStream:
		// 一条流在两端各有一个端点：发起方发 Open，接收方回 OpenAck，
		// 两个方向都计数，才能得到该节点真实的活跃流数量。
		ctx.addStream(1)
	case packet.CmdCloseStream, packet.AckCloseStream:
		ctx.addStream(-1)
	}
}

// addStream 原子增减活跃流计数，并钳制下界为 0。
// 旧实现直接 AddInt32，接收方被 Close 时计数会变成负数，导致可观测性失真，
// 也会让后续的流配额判断失效。
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
	// 整个释放过程只允许执行一次（replace 与连接断开可能并发触发）。
	// sync.Once 保证函数体的效果对所有 Do 调用方可见。
	ctx.closeOnce.Do(func() {
		close(ctx.forwardDone)
		c := ctx.getConn()
		if c != nil {
			c.Close()
		}
		ctx.setConn(nil)
		ctx.setAttached(false)
		ctx.DetachTime = time.Now()
	})
}

// runForwardLoop is the single consumer for forwardCh, preserving per-destination packet order.
func (ctx *Context) runForwardLoop() {
	for {
		select {
		case pbuf, ok := <-ctx.forwardCh:
			if !ok {
				return
			}
			if err := ctx.writeBuffer(pbuf); err != nil {
				ctx.logger.Warn("forward write failed", "ctx_id", ctx.id, "domain", ctx.Domain, "error", err)
			}
		case <-ctx.forwardDone:
			return
		}
	}
}

// enqueueForward puts a packet into the forward channel without blocking the caller's read loop.
// 慢路径最多等待 5 秒；超时说明对端消费停滞，此时丢包会让上层流静默损坏，
// 因此采取 fail-loud：释放该 ctx（断开连接、触发 registry 清理），并返回错误。
func (ctx *Context) enqueueForward(pbuf *packet.Buffer) error {
	// Fast path: non-blocking try.
	select {
	case ctx.forwardCh <- pbuf:
		return nil
	case <-ctx.forwardDone:
		return errContextClosed
	default:
	}

	// Slow path: wait with timeout.
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	select {
	case ctx.forwardCh <- pbuf:
		return nil
	case <-ctx.forwardDone:
		return errContextClosed
	case <-timer.C:
		ctx.release()
		return errForwardEnqueueTimeout
	}
}

// deliverPingResponse delivers a ping ACK to the waiting ping call.
// 返回 true 仅当 pbuf 真正送入等待通道（所有权转移给 ping 调用方）；
// 返回 false 时 pbuf 的回收责任仍在调用方。
func (ctx *Context) deliverPingResponse(port uint16, pbuf *packet.Buffer) bool {
	it, found := ctx.pingBack.Load(port)
	if !found {
		return false
	}
	ch, ok := it.(chan *packet.Buffer)
	if !ok {
		return false
	}
	// Defensive send: channel may be closed or full if ping timed out
	select {
	case ch <- pbuf:
		return true
	default:
		return false
	}
}

func (ctx *Context) ping(timeout time.Duration) (dur time.Duration, retErr error) {
	port := uint16(0xffff & ctx.pingIndex.Add(1))

	pbuf := packet.NewBuffer()
	pbuf.SetCmd(packet.CmdPingDomain)
	pbuf.SetSrc(packet.SwitcherIP, port)
	pbuf.SetDist(ctx.IP, 0)
	_ = pbuf.SetPayload([]byte(ctx.Domain))

	ch := make(chan *packet.Buffer, 1) // buffered to prevent goroutine leak
	ctx.pingBack.Store(port, ch)
	defer func() {
		ctx.pingBack.Delete(port)
		close(ch)
	}()

	pingStart := time.Now()
	err := ctx.enqueueForward(pbuf)
	if err != nil {
		packet.PutBuffer(pbuf) // 未入队，所有权未转移
		return 0, errPingWriteFailed
	}

	select {
	case pbuf := <-ch:
		info := string(pbuf.Payload)
		packet.PutBuffer(pbuf) // 消费完毕，归还（该 buffer 已被 transfer 给本 goroutine）
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
