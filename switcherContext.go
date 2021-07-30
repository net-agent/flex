package flex

import (
	"errors"
	"time"
)

type SwContext struct {
	host           *Host
	id             uint64 // 用于断线重连
	ip             HostIP
	domain         string
	mac            string
	chanRecoverErr chan error
}

func (ctx *SwContext) TryRecover() error {
	select {
	case err, ok := <-ctx.chanRecoverErr:
		if !ok {
			return errors.New("unexpected chan close error")
		}
		return err
	case <-time.After(time.Second * 5):
		return errors.New("wait connection timeout")
	}
}

func (sw *Switcher) attach(ctx *SwContext) error {
	_, loaded := sw.domainCtxs.LoadOrStore(ctx.domain, ctx)
	if loaded {
		return errors.New("exist domain")
	}

	sw.ctxs.Store(ctx.ip, ctx)
	sw.ctxids.Store(ctx.id, ctx)

	return nil
}

func (sw *Switcher) detach(ctx *SwContext) {
	sw.domainCtxs.Delete(ctx.domain)
	sw.ctxs.Delete(ctx.ip)
	sw.ctxids.Delete(ctx.id)
}
