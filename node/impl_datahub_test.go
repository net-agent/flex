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
	d.HandleCmdPushStreamDataAck(pbuf)
	d.HandleCmdCloseStream(pbuf)
	d.HandleCmdCloseStreamAck(pbuf)
}

func TestStreamList(t *testing.T) {
	n1, n2 := Pipe("test1", "test2")

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

	var closeWaiter sync.WaitGroup
	var sendWaiter sync.WaitGroup
	for i := 0; i < 10; i++ {
		sendWaiter.Add(1)
		closeWaiter.Add(1)
		go func() {
			start := time.Now()
			payload := make([]byte, 64*1024*1024)
			c, err := n1.Dial("test2:80")
			assert.Nil(t, err)

			go c.Write(payload)
			buf := make([]byte, len(payload))
			_, err = io.ReadFull(c, buf)
			assert.Nil(t, err)
			assert.True(t, bytes.Equal(payload, buf))
			sendWaiter.Done()

			<-time.After(time.Millisecond * 100)
			c.Close()
			log.Printf("send data ok: %v\n", time.Since(start))
			closeWaiter.Done()
		}()
	}

	sendWaiter.Wait()
	list1 := n1.GetDataStreamList()
	list2 := n2.GetDataStreamList()
	assert.Equal(t, len(list1), len(list2))
	assert.NotEqual(t, len(list1), 0)
	log.Println("len(list1)=", len(list1))

	closeWaiter.Wait()
	list1 = n1.GetDataStreamList()
	list2 = n2.GetDataStreamList()
	assert.Equal(t, len(list1), len(list2))
	assert.Equal(t, len(list1), 0)
	log.Println("len(list1)=", len(list1))
}
