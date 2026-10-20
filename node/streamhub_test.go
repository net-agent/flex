package node

import (
	"bytes"
	"io"
	"log"
	"sync"
	"sync/atomic"
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

	closed1, _ := n1.GetClosedStates(0)
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
		closed, _ := n1.GetClosedStates(0)
		return len(closed) == times
	}, "closed states should be eventually recorded")
	closed1, _ = n1.GetClosedStates(0)
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
		states, _ := hub.GetClosedStates(0)
		return len(states) == 1
	}, "closed state should be eventually recorded")
	closed, _ := hub.GetClosedStates(0)
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

func TestGetClosedStatesIncremental(t *testing.T) {
	hub := &StreamHub{}
	hub.init(nil, nil)

	// 空缓冲区：无数据，pos 保持 0
	states, pos := hub.GetClosedStates(0)
	assert.Empty(t, states)
	assert.Equal(t, int64(0), pos)

	hub.recordClosedState(&stream.State{Index: 1})
	hub.recordClosedState(&stream.State{Index: 2})

	states, pos = hub.GetClosedStates(pos)
	assert.Len(t, states, 2)
	assert.Equal(t, int32(1), states[0].Index)
	assert.Equal(t, int64(2), pos)

	// 用最新 pos 重复拉取：无新数据
	states, pos2 := hub.GetClosedStates(pos)
	assert.Empty(t, states)
	assert.Equal(t, pos, pos2)

	// 新增一条后增量拉取
	hub.recordClosedState(&stream.State{Index: 3})
	states, pos = hub.GetClosedStates(pos)
	assert.Len(t, states, 1)
	assert.Equal(t, int32(3), states[0].Index)
	assert.Equal(t, int64(3), pos)
}

func TestGetClosedStatesRingWrap(t *testing.T) {
	hub := &StreamHub{}
	hub.init(nil, nil)

	for i := 0; i < maxClosedStates+10; i++ {
		hub.recordClosedState(&stream.State{Index: int32(i)})
	}

	// pos 指向的记录已被覆盖：从最早可用记录开始返回
	states, pos := hub.GetClosedStates(0)
	assert.Len(t, states, maxClosedStates)
	assert.Equal(t, int64(maxClosedStates+10), pos)
	assert.Equal(t, int32(10), states[0].Index, "oldest available record should be Index=10")

	// pos 落在可用范围内：按序返回
	states, _ = hub.GetClosedStates(pos - 5)
	assert.Len(t, states, 5)
}

func TestCloseStreamByIndex(t *testing.T) {
	hub := &StreamHub{}
	hub.init(nil, nil)

	s := stream.New(nil, 0)
	index := s.GetState().Index
	hub.streams.Store(uint64(100), s)

	// 找不到：未知 index 返回 false
	assert.False(t, hub.closeStreamByIndex(index+1000))

	// 找到并关闭：返回 true，CloseReason 记为 "manual"
	assert.True(t, hub.closeStreamByIndex(index))
	st := s.GetState()
	assert.True(t, st.IsClosed)
	assert.True(t, st.ReadClosed)
	assert.True(t, st.WriteClosed)
	assert.Equal(t, "manual", st.CloseReason)

	// 已完全关闭的滞留流（滞留期内仍在 hub 中）视为未找到
	assert.False(t, hub.closeStreamByIndex(index))
}

func TestNodeCloseStream(t *testing.T) {
	n1, n2 := Pipe("test1", "test2")
	defer n1.Close()
	defer n2.Close()

	l, err := n2.Listen(80)
	assert.Nil(t, err)
	go func() {
		c, err := l.Accept()
		if err == nil {
			io.Copy(io.Discard, c) // 保持连接，直到对端关闭
			c.Close()
		}
	}()

	s, err := n1.Dial("test2:80")
	assert.Nil(t, err)
	index := s.GetState().Index

	// 未知 index 返回 false
	assert.False(t, n1.CloseStream(index+1000))

	// 手动关闭指定流：返回 true，CloseReason 记为 "manual"
	assert.True(t, n1.CloseStream(index))
	st := s.GetState()
	assert.True(t, st.IsClosed)
	assert.Equal(t, "manual", st.CloseReason)

	// 滞留期满后流从 hub 移除，且关闭状态记录 manual 原因
	waitForCondition(t, 5*time.Second, func() bool {
		return len(n1.GetStreamStates()) == 0
	}, "manually closed stream should be detached after retention")
	waitForCondition(t, 5*time.Second, func() bool {
		closed, _ := n1.GetClosedStates(0)
		return len(closed) == 1 && closed[0].CloseReason == "manual"
	}, "closed state should record manual reason")
}

func TestNodeCloseStreamsReasonNode(t *testing.T) {
	n1, n2 := Pipe("test1", "test2")
	defer n2.Close()

	l, err := n2.Listen(80)
	assert.Nil(t, err)
	go func() {
		c, err := l.Accept()
		if err == nil {
			io.Copy(io.Discard, c)
			c.Close()
		}
	}()

	_, err = n1.Dial("test2:80")
	assert.Nil(t, err)

	// 节点关闭：所有活跃流的 CloseReason 记为 "node"
	n1.Close()
	waitForCondition(t, 5*time.Second, func() bool {
		closed, _ := n1.GetClosedStates(0)
		return len(closed) == 1 && closed[0].CloseReason == "node"
	}, "node close should record reason=node")
}

// StreamClosed 事件应在 detach 时刻（双向关闭齐备）同步触发，而非滞留期届满时；
// 事件触发时流仍可寻址，滞留期继续承担 SID 寻址与晚到包消化职责。
func TestAttachStream_StreamClosedFiresAtDetach(t *testing.T) {
	n := New(nil)
	sid := uint64(777)

	var closes int32
	var addressableAtEvent int32
	var lastState atomic.Value // *stream.State
	n.SetTrace(&Trace{
		StreamClosed: func(st *stream.State) {
			atomic.AddInt32(&closes, 1)
			lastState.Store(st)
			if _, err := n.streamHub.getStream(sid); err == nil {
				atomic.StoreInt32(&addressableAtEvent, 1)
			}
		},
	})
	// 滞留期足够长：若事件等清理时才发，下面的即时断言会失败
	n.streamHub.detachRetention = time.Hour

	s := stream.New(nil, 0)
	assert.Nil(t, n.streamHub.attachStream(s, sid))

	assert.Nil(t, s.CloseRead())
	assert.Nil(t, s.CloseWrite()) // detach 在此发生，回调同步执行

	// CloseWrite 返回时事件已触发，无需等待滞留期
	assert.Equal(t, int32(1), atomic.LoadInt32(&closes), "StreamClosed should fire at detach, not after retention")
	assert.Equal(t, int32(1), atomic.LoadInt32(&addressableAtEvent), "stream should still be addressable when the event fires")

	st := lastState.Load().(*stream.State)
	assert.True(t, st.IsClosed)
	assert.False(t, st.Closed.IsZero())
	assert.Equal(t, "local", st.CloseReason)

	// 滞留期内流仍可寻址
	_, err := n.streamHub.getStream(sid)
	assert.Nil(t, err, "stream should remain addressable during retention")
}

// 同一流的事件只触发一次：滞留期届满后的清理只写 closedStates 历史，不再重复发事件。
func TestAttachStream_StreamClosedNotRepeatedAtCleanup(t *testing.T) {
	n := New(nil)
	var closes int32
	n.SetTrace(&Trace{
		StreamClosed: func(st *stream.State) { atomic.AddInt32(&closes, 1) },
	})
	n.streamHub.detachRetention = 20 * time.Millisecond

	s := stream.New(nil, 0)
	assert.Nil(t, n.streamHub.attachStream(s, uint64(778)))
	assert.Nil(t, s.CloseRead())
	assert.Nil(t, s.CloseWrite())
	assert.Equal(t, int32(1), atomic.LoadInt32(&closes), "event should fire at detach")

	waitForCondition(t, 5*time.Second, func() bool {
		states, _ := n.streamHub.GetClosedStates(0)
		return len(states) == 1
	}, "closed state should be recorded once after retention")
	assert.Equal(t, int32(1), atomic.LoadInt32(&closes), "event must not fire again at cleanup")
}
