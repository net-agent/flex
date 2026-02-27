package node

import (
	"bytes"
	"io"
	"log"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/internal/idpool"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/stream"
	"github.com/stretchr/testify/assert"
)

func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	assert.Fail(t, msg)
}

func TestGetStreamBySID(t *testing.T) {
	hub := &StreamHub{}
	var err error

	// 测试分支：stream not found
	_, err = hub.getStream(100)
	assert.Equal(t, err, errStreamNotFound, "cover test: errStreamNotFound")

	// 测试分支：convert failed (detachStream)
	hub.streams.Store(uint64(100), 1234)
	_, err = hub.detachStream(100)
	assert.Equal(t, err, errInvalidStreamType, "cover test: errInvalidStreamType")

	_, loaded := hub.streams.Load(uint64(100))
	assert.False(t, loaded, "test detachStream deletes entry")

	// 测试分支：正常通过
	s := stream.New(nil, 0)
	hub.streams.Store(uint64(200), s)
	retStream, err := hub.getStream(uint64(200))
	assert.Nil(t, err, "test getStream")
	assert.Equal(t, retStream, s, "retStream should equal to s")

	_, loaded = hub.streams.Load(uint64(200))
	assert.True(t, loaded, "getStream should not delete entry")

	// 测试分支：detachStream 正常通过
	retStream, err = hub.detachStream(uint64(200))
	assert.Nil(t, err, "test detachStream")
	assert.Equal(t, retStream, s, "retStream should equal to s")

	_, loaded = hub.streams.Load(uint64(200))
	assert.False(t, loaded, "detachStream should delete entry")
}

func TestHandleErr_SIDNotFound(t *testing.T) {
	d := &StreamHub{}
	pbuf := packet.NewBufferWithCmd(0)

	d.handleCmdPushStreamData(pbuf)
	d.handleAckPushStreamData(pbuf)
	d.handleCmdCloseStream(pbuf)
	d.handleAckCloseStream(pbuf)
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
			go func() {
				io.Copy(c, c)
				c.Close()
			}()
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
	list1 := n1.GetStreamStates()
	list2 := n2.GetStreamStates()
	assert.Equal(t, len(list1), len(list2))
	assert.NotEqual(t, len(list1), 0)
	log.Println("len(list1)=", len(list1))

	closed1 := n1.GetClosedStates(0)
	assert.Equal(t, 0, len(closed1))

	close(testStep1Done) // 关闭后，所有等待的地方都会收到消息，进入下一阶段

	closeWaiter.Wait()
	// 关闭后 StreamHub 会保留一段时间用于消化晚到包，等待延迟清理完成。
	waitForCondition(t, 5*time.Second, func() bool {
		return len(n1.GetStreamStates()) == 0 && len(n2.GetStreamStates()) == 0
	}, "all streams should be detached after retention period")

	list1 = n1.GetStreamStates()
	list2 = n2.GetStreamStates()
	assert.Equal(t, len(list1), len(list2))
	assert.Equal(t, len(list1), 0)
	log.Println("len(list1)=", len(list1))

	waitForCondition(t, 5*time.Second, func() bool {
		return len(n1.GetClosedStates(0)) == times
	}, "closed states should be eventually recorded")
	closed1 = n1.GetClosedStates(0)
	assert.Equal(t, times, len(closed1))

}

func TestAttachStream_OnDetachReleasesPortAndRecordsState(t *testing.T) {
	pool, err := idpool.New(1000, 1000)
	assert.Nil(t, err)

	hub := &StreamHub{}
	hub.init(nil, pool)

	port, err := pool.Allocate()
	assert.Nil(t, err)
	assert.Equal(t, 1, pool.InUse())

	sid := uint64(12345)
	s := stream.New(nil, 0)
	s.SetBoundPort(port)

	err = hub.attachStream(s, sid)
	assert.Nil(t, err)

	err = s.CloseRead()
	assert.Nil(t, err)
	err = s.CloseWrite()
	assert.Nil(t, err)

	waitForCondition(t, 5*time.Second, func() bool {
		_, err = hub.getStream(sid)
		return err == errStreamNotFound
	}, "stream should be auto-detached on full close")
	_, err = hub.getStream(sid)
	assert.Equal(t, errStreamNotFound, err, "stream should be auto-detached on full close")

	waitForCondition(t, 5*time.Second, func() bool {
		return pool.InUse() == 0
	}, "bound port should be released on detach")
	assert.Equal(t, 0, pool.InUse(), "bound port should be released on detach")

	waitForCondition(t, 5*time.Second, func() bool {
		return len(hub.GetClosedStates(0)) == 1
	}, "closed state should be eventually recorded")
	closed := hub.GetClosedStates(0)
	assert.Equal(t, 1, len(closed), "closed state should be recorded once")
}

func TestAttachStream_OnDetachDoesNotDeleteReusedSID(t *testing.T) {
	pool, err := idpool.New(1000, 1001)
	assert.Nil(t, err)

	hub := &StreamHub{}
	hub.init(nil, pool)

	sid := uint64(888)

	port1, err := pool.Allocate()
	assert.Nil(t, err)
	s1 := stream.New(nil, 0)
	s1.SetBoundPort(port1)
	assert.Nil(t, hub.attachStream(s1, sid))

	// Simulate external map replacement before old stream's detach callback fires.
	hub.streams.Delete(sid)

	port2, err := pool.Allocate()
	assert.Nil(t, err)
	s2 := stream.New(nil, 0)
	s2.SetBoundPort(port2)
	assert.Nil(t, hub.attachStream(s2, sid))

	assert.Nil(t, s1.CloseRead())
	assert.Nil(t, s1.CloseWrite())

	got, err := hub.getStream(sid)
	assert.Nil(t, err)
	assert.Equal(t, s2, got, "old stream detach callback must not remove the new stream under same SID")
}
