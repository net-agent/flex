package event

import (
	"errors"
	"sync"
	"sync/atomic"
)

var (
	ErrEventHasListened      = errors.New("event has listened")
	ErrContextNotFound       = errors.New("context not found")
	ErrInvalidContextPointer = errors.New("invalid context pointer")
	ErrContextDispatched     = errors.New("context dispatched")
)

type Bus struct {
	contexts sync.Map
}

func (eb *Bus) Listen(key interface{}) (Waiter, error) {
	ctx := &Context{
		Dispatched:  0,
		Removed:     false,
		IsOnce:      false,
		Key:         key,
		PayloadChan: make(chan *Reply, 1024),
	}
	_, found := eb.contexts.LoadOrStore(key, ctx)
	if found {
		return nil, ErrEventHasListened
	}

	return ctx, nil
}

func (eb *Bus) ListenOnce(key interface{}) (Waiter, error) {
	ctx := &Context{
		Dispatched:  0,
		Removed:     false,
		IsOnce:      true,
		Key:         key,
		PayloadChan: make(chan *Reply, 1),
	}
	_, found := eb.contexts.LoadOrStore(key, ctx)
	if found {
		return nil, ErrEventHasListened
	}
	return ctx, nil
}

func (eb *Bus) Dispatch(key interface{}, replyErr error, replyPayload interface{}) (retErr error) {
	it, found := eb.contexts.Load(key)
	if !found {
		return ErrContextNotFound
	}
	ctx := it.(*Context)
	count := atomic.AddInt32(&ctx.Dispatched, 1)
	if ctx.IsOnce {
		if count > 1 {
			return ErrContextDispatched
		}

		// 在Dispatch后，清理context
		defer func() {
			eb.contexts.Delete(key)
			ctx.Clean()
		}()
	}

	ctx.PayloadChan <- &Reply{replyErr, replyPayload}
	return nil
}

func (eb *Bus) StopListen(key interface{}) error {
	it, found := eb.contexts.LoadAndDelete(key)
	if !found {
		return ErrContextNotFound
	}
	ctx := it.(*Context)
	ctx.Clean()
	return nil
}
