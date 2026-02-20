package switcher

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/vars"
)

var (
	errPingWriteFailed = errors.New("ping write buffer failed")
	errPingTimeout     = errors.New("ping timeout")
	errNilContextConn  = errors.New("context conn is nil")
)

type Context struct {
	id     int
	Domain string
	Mac    string
	IP     uint16

	mu           sync.Mutex
	conn         packet.Conn
	attached     bool
	lastReadTime atomic.Value // stores time.Time

	AttachTime time.Time
	DetachTime time.Time

	pingIndex int32
	pingBack  sync.Map
	Stats     ContextStats
}

type ContextStats struct {
	StreamCount   int32
	BytesReceived int64
	BytesSent     int64
	LastRTT       int64 // nanoseconds, use atomic access
}

func NewContext(id int, conn packet.Conn, domain, mac string) *Context {
	ctx := &Context{
		id:         id,
		Domain:     domain,
		Mac:        mac,
		IP:         0,
		conn:       conn,
		AttachTime: time.Now(),
	}
	ctx.lastReadTime.Store(time.Now())
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
	defer ctx.mu.Unlock()
	ctx.conn = c
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

func (ctx *Context) SetLastReadTime(t time.Time) {
	ctx.lastReadTime.Store(t)
}

func (ctx *Context) GetLastReadTime() time.Time {
	v := ctx.lastReadTime.Load()
	if v == nil {
		return time.Time{}
	}
	return v.(time.Time)
}

func (ctx *Context) ReadBuffer() (*packet.Buffer, error) {
	c := ctx.getConn()
	if c == nil {
		return nil, errNilContextConn
	}
	return c.ReadBuffer()
}

func (ctx *Context) WriteBuffer(buf *packet.Buffer) error {
	c := ctx.getConn()
	if c == nil {
		return errNilContextConn
	}

	// Update stats
	atomic.AddInt64(&ctx.Stats.BytesSent, int64(buf.PayloadSize()+packet.HeaderSz))

	return c.WriteBuffer(buf)
}

func (ctx *Context) Ping(timeout time.Duration) (dur time.Duration, retErr error) {
	port := uint16(0xffff & atomic.AddInt32(&ctx.pingIndex, 1))

	pbuf := packet.NewBuffer(nil)
	pbuf.SetCmd(packet.CmdPingDomain)
	pbuf.SetSrc(vars.SwitcherIP, port)
	pbuf.SetDist(ctx.IP, 0)
	pbuf.SetPayload([]byte(ctx.Domain))

	ch := make(chan *packet.Buffer, 1) // buffered to prevent goroutine leak
	ctx.pingBack.Store(port, ch)
	defer func() {
		ctx.pingBack.Delete(port)
		close(ch)
	}()

	pingStart := time.Now()
	err := ctx.WriteBuffer(pbuf)
	if err != nil {
		return 0, errPingWriteFailed
	}

	select {
	case pbuf := <-ch:
		info := string(pbuf.Payload)
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
