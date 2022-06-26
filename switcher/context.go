package switcher

import (
	"errors"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/vars"
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

	AttachTime time.Time
	DetachTime time.Time

	pingIndex int32
	pingBack  sync.Map
	attached  bool
}

func NewContext(domain, mac string, ip uint16, conn packet.Conn) *Context {
	return &Context{
		id:           int(atomic.AddInt32(&ctxindex, 1)),
		Domain:       domain,
		Mac:          mac,
		IP:           ip,
		Conn:         conn,
		LastReadTime: time.Now(),
		AttachTime:   time.Now(),
	}
}

func (ctx *Context) WriteBuffer(buf *packet.Buffer) error {
	ctx.writeMut.Lock()
	defer ctx.writeMut.Unlock()
	return ctx.Conn.WriteBuffer(buf)
}

func (ctx *Context) Ping(timeout time.Duration) (dur time.Duration, retErr error) {
	ch := make(chan error)
	defer close(ch)
	index := uint16(0xffff & atomic.AddInt32(&ctx.pingIndex, 1))
	ctx.pingBack.Store(index, ch)
	defer ctx.pingBack.Delete(index)

	pingStart := time.Now()
	log.Printf("[SEND-PING] domain='%v' ip='%v' index='%v'\n", ctx.Domain, ctx.IP, index)
	defer func() {
		log.Printf("[RECV-PING] domain='%v' ip='%v' index='%v' err='%v' dur=%v\n", ctx.Domain, ctx.IP, index, retErr, dur)
	}()

	go func() {
		pbuf := packet.NewBuffer(nil)
		pbuf.SetCmd(packet.CmdAlive)
		pbuf.SetSrc(vars.SwitcherIP, index)
		pbuf.SetDist(ctx.IP, 0)
		err := ctx.WriteBuffer(pbuf)
		if err != nil {
			ch <- err
		}
	}()

	select {
	case err := <-ch:
		return time.Since(pingStart), err
	case <-time.After(timeout):
		return time.Since(pingStart), errors.New("ping timeout")
	}
}
