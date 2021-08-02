package switcher

import (
	"sync"

	"github.com/net-agent/flex/packet"
)

type Context struct {
	Domain   string
	Mac      string
	IP       uint16
	Conn     packet.Conn
	writeMut sync.Mutex
}

func NewContext(domain, mac string, ip uint16, conn packet.Conn) *Context {
	return &Context{
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
