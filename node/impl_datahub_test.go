package node

import (
	"bytes"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/stream"
	"github.com/stretchr/testify/assert"
)

func TestGetStreamBySID(t *testing.T) {
	hub := &DataHub{}
	var err error

	// 测试分支：stream not found
	_, err = hub.GetStreamBySID(100, false)
	assert.Equal(t, err, errStreamNotFound, "cover test: errStreamNotFound")

	// 测试分支：convert failed
	hub.streams.Store(uint64(100), 1234)
	loadAndDelete := true
	_, err = hub.GetStreamBySID(100, loadAndDelete)
	assert.Equal(t, err, errConvertStreamFailed, "cover test: errConvertStreamFailed")

	_, loaded := hub.streams.Load(uint64(100))
	assert.False(t, loaded, "test loadAndDelete flag")

	// 测试分支：正常通过
	s := stream.New(nil)
	hub.streams.Store(uint64(200), s)
	loadAndDelete = false
	retStream, err := hub.GetStreamBySID(uint64(200), loadAndDelete)
	assert.Nil(t, err, "test loadAndDelete flag")
	assert.Equal(t, retStream, s, "retStream should equal to s")

	_, loaded = hub.streams.Load(uint64(200))
	assert.True(t, loaded, "test loadAndDelete flag")
}

func TestHandleErr_SIDNotFound(t *testing.T) {
	d := &DataHub{}
	pbuf := packet.NewBufferWithCmd(0)

	d.HandleCmdPushStreamData(pbuf)
	d.HandleAckPushStreamData(pbuf)
	d.HandleCmdCloseStream(pbuf)
	d.HandleAckCloseStream(pbuf)
}

func TestStreamList(t *testing.T) {
	n1, n2 := Pipe("test1", "test2")
	times := int(10)

	l, err := n2.Listen(80)
	assert.Nil(t, err)

	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go io.Copy(c, c)
		}
	}()

	var testStep1Done = make(chan struct{}, 1)
	var closeWaiter sync.WaitGroup
	var sendWaiter sync.WaitGroup
	for i := 0; i < times; i++ {
		sendWaiter.Add(1)
		closeWaiter.Add(1)
		go func(index int) {
			start := time.Now()
			payload := make([]byte, 64*1024*1024)
			c, err := n1.Dial("test2:80")
			if !assert.Nil(t, err) {
				panic(err)
			}
			if !assert.NotNil(t, c) {
				panic("nil stream")
			}

			go c.Write(payload)
			buf := make([]byte, len(payload))
			_, err = io.ReadFull(c, buf)
			assert.Nil(t, err)
			assert.True(t, bytes.Equal(payload, buf))
			log.Printf("[%v] send ok: %v\n", index, time.Since(start))
			sendWaiter.Done()

			<-testStep1Done

			c.Close()
			log.Printf("[%v] close ok: %v\n", index, time.Since(start))
			closeWaiter.Done()
		}(i)
	}

	sendWaiter.Wait()
	list1 := n1.GetStreamStateList()
	list2 := n2.GetStreamStateList()
	assert.Equal(t, len(list1), len(list2))
	assert.NotEqual(t, len(list1), 0)
	log.Println("len(list1)=", len(list1))

	closed1 := n1.GetClosedStreamStateList(0)
	assert.Equal(t, int(0), len(closed1))

	close(testStep1Done) // 关闭后，所有等待的地方都会收到消息，进入下一阶段

	closeWaiter.Wait()
	list1 = n1.GetStreamStateList()
	list2 = n2.GetStreamStateList()
	assert.Equal(t, len(list1), len(list2))
	assert.Equal(t, len(list1), 0)
	log.Println("len(list1)=", len(list1))

	closed1 = n1.GetClosedStreamStateList(0)
	assert.Equal(t, times, len(closed1))

}
