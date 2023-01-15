package event

import (
	"errors"
	"io"
	"time"
)

var (
	ErrPayloadChanClosed = errors.New("payload chan closed")
	ErrWaitReplyTimeout  = errors.New("accept reply timeout")
	ErrUnexpectedNilData = errors.New("unexpected nil data")
)

type Waiter interface {
	Wait(timeout time.Duration) (interface{}, error)
}

type Context struct {
	Dispatched  int32
	Removed     bool
	IsOnce      bool
	Key         interface{}
	PayloadChan chan *Reply
}

type Reply struct {
	Err  error
	Data interface{}
}

func (ctx *Context) Clean() {
	if ctx.Removed {
		return
	}

	ctx.Removed = true
	if ctx.PayloadChan != nil {
		close(ctx.PayloadChan)
	}

	ctx.Key = nil
}

func (ctx *Context) Wait(timeout time.Duration) (interface{}, error) {
	if ctx.PayloadChan == nil {
		return nil, io.EOF
	}
	select {
	case payload, ok := <-ctx.PayloadChan:
		if !ok {
			return nil, ErrPayloadChanClosed
		}
		if payload.Err != nil {
			return nil, payload.Err
		}
		return payload.Data, nil
	case <-time.After(timeout):
		return nil, ErrWaitReplyTimeout
	}
}
