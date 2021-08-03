package switcher

import (
	"sync"
	"sync/atomic"

	"github.com/net-agent/flex/packet"
)

var ctxindex int32

type Context struct {
	id       int
	Domain   string
	Mac      string
	IP       uint16
	Conn     packet.Conn
	writeMut sync.Mutex
}

func NewContext(domain, mac string, ip uint16, conn packet.Conn) *Context {
	return &Context{
		id:     int(atomic.AddInt32(&ctxindex, 1)),
		Domain: domain,
		Mac:    mac,
		IP:     ip,
		Conn:   conn,
	}
}

func (ctx *Context) WriteBuffer(buf *packet.Buffer) error {
	ctx.writeMut.Lock()
	defer ctx.writeMut.Unlock()
	return ctx.Conn.WriteBuffer(buf)
}
