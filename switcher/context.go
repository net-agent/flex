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

var ctxindex int32

var (
	errPingWriteFailed = errors.New("ping write buffer failed")
	errPingTimeout     = errors.New("ping timeout")
	errNilContextConn  = errors.New("context conn is nil")
)

type Context struct {
	id           int
	Domain       string
	Mac          string
	IP           uint16
	Conn         packet.Conn
	LastReadTime time.Time

	AttachTime time.Time
	DetachTime time.Time

	pingIndex int32
	pingBack  sync.Map
	attached  bool
	Stats     ContextStats
}

type ContextStats struct {
	StreamCount   int32
	BytesReceived int64
	BytesSent     int64
	LastRTT       time.Duration
}

func NewContext(conn packet.Conn, domain, mac string) *Context {
	return &Context{
		id:           int(atomic.AddInt32(&ctxindex, 1)),
		Domain:       domain,
		Mac:          mac,
		IP:           0,
		Conn:         conn,
		LastReadTime: time.Now(),
		AttachTime:   time.Now(),
	}
}

func (ctx *Context) GetID() int {
	return ctx.id
}

func (ctx *Context) WriteBuffer(buf *packet.Buffer) error {
	if ctx.Conn == nil {
		return errNilContextConn
	}

	// Update stats
	atomic.AddInt64(&ctx.Stats.BytesSent, int64(buf.PayloadSize()+packet.HeaderSz))

	return ctx.Conn.WriteBuffer(buf)
}

func (ctx *Context) Ping(timeout time.Duration) (dur time.Duration, retErr error) {
	port := uint16(0xffff & atomic.AddInt32(&ctx.pingIndex, 1))

	pbuf := packet.NewBuffer(nil)
	pbuf.SetCmd(packet.CmdPingDomain)
	pbuf.SetSrc(vars.SwitcherIP, port)
	pbuf.SetDist(ctx.IP, 0)
	pbuf.SetPayload([]byte(ctx.Domain))

	ch := make(chan *packet.Buffer)
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
	ctx.Stats.LastRTT = dur
	return dur, nil
}
