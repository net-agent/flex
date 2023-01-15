package event

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestListen(t *testing.T) {
	bus := &Bus{}

	w, err := bus.Listen("test event")
	assert.Nil(t, err)

	bus.Dispatch("test event", nil, "hello, world")

	resp, err := w.Wait(time.Second)
	assert.Nil(t, err)
	assert.Equal(t, resp, "hello, world")

	// 重复listen
	_, err = bus.Listen("test event")
	assert.Equal(t, ErrEventHasListened, err)

	// 正常停止监听
	err = bus.StopListen("test event")
	assert.Nil(t, err)

	// 触发派发失败
	err = bus.Dispatch("test event", nil, "1234")
	assert.Equal(t, ErrContextNotFound, err)

	// 触发停止监听失败
	err = bus.StopListen("test event")
	assert.Equal(t, ErrContextNotFound, err)
}

func TestListenOnce(t *testing.T) {
	bus := &Bus{}

	w, err := bus.ListenOnce("test")
	assert.Nil(t, err)

	_, err = bus.ListenOnce("test")
	assert.Equal(t, ErrEventHasListened, err)

	go w.Wait(time.Second)

	err = bus.Dispatch("test", nil, "hello")
	assert.Nil(t, err)
	err = bus.Dispatch("test", nil, "world")
	assert.Equal(t, ErrContextNotFound, err)
}

func TestListenOnceErr_concurrency(t *testing.T) {
	bus := &Bus{}

	w, err := bus.ListenOnce("test")
	assert.Nil(t, err)

	w.(*Context).Dispatched = 1
	err = bus.Dispatch("test", nil, nil)
	assert.Equal(t, ErrContextDispatched, err)
}
