package switcher

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v2/packet"
)

var ctxindex int32

type Context struct {
	id           int
	Domain       string
	Mac          string
	IP           uint16
	Conn         packet.Conn
	writeMut     sync.Mutex
	LastReadTime time.Time
}

func NewContext(domain, mac string, ip uint16, conn packet.Conn) *Context {
	return &Context{
		id:           int(atomic.AddInt32(&ctxindex, 1)),
		Domain:       domain,
		Mac:          mac,
		IP:           ip,
		Conn:         conn,
		LastReadTime: time.Now(),
	}
}

func (ctx *Context) WriteBuffer(buf *packet.Buffer) error {
	ctx.writeMut.Lock()
	defer ctx.writeMut.Unlock()
	return ctx.Conn.WriteBuffer(buf)
}
