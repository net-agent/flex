package stream

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/net-agent/flex/v3/packet"
	"github.com/stretchr/testify/assert"
)

func TestSendData(t *testing.T) {
	t.Run("nil data returns nil", func(t *testing.T) {
		sender := &sender{}
		err := sender.SendData(nil)
		assert.Nil(t, err)
	})

	t.Run("oversize data returns error", func(t *testing.T) {
		sender := &sender{}
		err := sender.SendData(make([]byte, packet.MaxPayloadSize+1))
		assert.Equal(t, ErrSendDataOversize, err)
	})
}

func TestSendData_Normal(t *testing.T) {
	rw := &recordingWriter{}
	var counter int32
	sender := newSender(rw, &counter)

	err := sender.SendData([]byte("hello"))
	assert.Nil(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&counter))
	assert.Equal(t, 1, rw.count())
}

func TestSendDataAck(t *testing.T) {
	rw := &recordingWriter{}
	var counter int32
	sender := newSender(rw, &counter)

	err := sender.SendDataAck(1024)
	assert.Nil(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&counter))
	assert.Equal(t, 1, rw.count())
}

func TestSendClose(t *testing.T) {
	rw := &recordingWriter{}
	var counter int32
	sender := newSender(rw, &counter)

	err := sender.SendClose()
	assert.Nil(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&counter))
	assert.Equal(t, 1, rw.count())
}

func TestSendCloseAck(t *testing.T) {
	rw := &recordingWriter{}
	var counter int32
	sender := newSender(rw, &counter)

	err := sender.SendCloseAck()
	assert.Nil(t, err)
	assert.Equal(t, int32(1), atomic.LoadInt32(&counter))
	assert.Equal(t, 1, rw.count())
}

func TestConcurrentSendData(t *testing.T) {
	rw := &recordingWriter{}
	var counter int32
	sender := newSender(rw, &counter)

	var wg sync.WaitGroup
	const goroutines = 20
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sender.SendData([]byte("data"))
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(goroutines), atomic.LoadInt32(&counter))
	assert.Equal(t, goroutines, rw.count())
}

func TestConcurrentSendDataAck(t *testing.T) {
	rw := &recordingWriter{}
	var counter int32
	sender := newSender(rw, &counter)

	var wg sync.WaitGroup
	const goroutines = 20
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sender.SendDataAck(512)
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(goroutines), atomic.LoadInt32(&counter))
	assert.Equal(t, goroutines, rw.count())
}
