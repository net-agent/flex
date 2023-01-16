package event

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestContextClean(t *testing.T) {
	ctx := &Context{}

	ctx.Clean()
	ctx.Clean()
}

func TestContextWait(t *testing.T) {
	bus := &Bus{}

	w, err := bus.Listen("test")
	assert.Nil(t, err)

	// test case: timeout
	_, err = w.Wait(time.Millisecond * 100)
	assert.Equal(t, ErrWaitReplyTimeout, err)

	// test case: payload.Err
	testErr := errors.New("testtest")
	bus.Dispatch("test", testErr, nil)
	_, err = w.Wait(time.Millisecond * 100)
	assert.Equal(t, testErr, err)

	// test case: payload.Data nil
	bus.Dispatch("test", nil, nil)
	_, err = w.Wait(time.Millisecond * 100)
	assert.Nil(t, err)

	// test case: normal
	bus.Dispatch("test", nil, "hello")
	resp, err := w.Wait(time.Millisecond * 100)
	assert.Nil(t, err)
	assert.Equal(t, resp, "hello")

	// test case: payload chan closed
	close(w.(*Context).PayloadChan)
	_, err = w.Wait(time.Second)
	assert.Equal(t, ErrPayloadChanClosed, err)
}
