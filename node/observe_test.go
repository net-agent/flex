package node

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/internal/idpool"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/stream"
	"github.com/stretchr/testify/assert"
)

func TestInspect(t *testing.T) {
	n1, n2 := Pipe("test1", "test2")
	defer n1.Close()
	defer n2.Close()

	// Pipe 返回的节点异步进入 Serve
	waitForCondition(t, 5*time.Second, n1.IsRunning, "running node should report IsRunning=true")
	assert.False(t, New(nil).IsRunning(), "node not serving should report IsRunning=false")

	snap1 := n1.Inspect()
	assert.True(t, snap1.Running, "snapshot should report running")
	assert.False(t, snap1.At.IsZero(), "snapshot should carry sample time")
	assert.GreaterOrEqual(t, snap1.Info.Uptime, int64(0), "uptime should be non-negative")

	// 端口池：shared（listenHub/dialer/streamHub 共用）与 pinger
	assert.Len(t, snap1.PortPools, 2)
	assert.Equal(t, "shared", snap1.PortPools[0].Name)
	assert.Equal(t, 64536, snap1.PortPools[0].Capacity) // New() 默认范围 1000..0xFFFF
	assert.Equal(t, 0, snap1.PortPools[0].InUse)
	assert.Equal(t, "pinger", snap1.PortPools[1].Name)
	assert.Equal(t, 65535, snap1.PortPools[1].Capacity) // pinger 范围 1..0xFFFF
	assert.Equal(t, 0, snap1.PortPools[1].InUse)

	// 心跳组件
	assert.Equal(t, DefaultHeartbeatInterval, snap1.Heartbeat.Interval)
	assert.False(t, snap1.Heartbeat.LastWrite.IsZero(), "last write time should be initialized")
	assert.False(t, snap1.Heartbeat.LastWrite.After(time.Now()), "last write time should not be in the future")

	// pending 计数：手工注册/移除，确定性覆盖
	_, err := n1.dialer.pending.Register(999) // 避开 dialer 端口分配起点 1000
	assert.Nil(t, err)
	_, err = n1.pinger.pending.Register(60000) // 避开 pinger 顺序分配的低段端口
	assert.Nil(t, err)
	snapPending := n1.Inspect()
	assert.Equal(t, 1, snapPending.Pending.Dial)
	assert.Equal(t, 1, snapPending.Pending.Ping)
	n1.dialer.pending.Remove(999)
	n1.pinger.pending.Remove(60000)

	// 建立一条真实流，验证 listeners/streams/pool 占用
	l, err := n2.Listen(80)
	assert.Nil(t, err)
	s, err := n1.Dial("test2:80")
	assert.Nil(t, err)

	snap2 := n1.Inspect()
	assert.GreaterOrEqual(t, snap2.Info.Uptime, snap1.Info.Uptime, "uptime should be monotonic")
	assert.Len(t, snap2.Streams, 1, "dial side should have one active stream")
	assert.Equal(t, 1, snap2.PortPools[0].InUse, "dialed stream should hold one shared port")
	assert.Equal(t, 0, snap2.Pending.Dial, "pending dial should be drained after dial returns")

	snapAccept := n2.Inspect()
	assert.Len(t, snapAccept.Listeners, 1)
	assert.Equal(t, uint16(80), snapAccept.Listeners[0].Port)
	assert.Len(t, snapAccept.Streams, 1, "accept side should have one active stream")

	s.Close()
	l.Close()
}

func TestTraceStreamOpenClosed(t *testing.T) {
	old := DetachRetention
	DetachRetention = 0
	defer func() { DetachRetention = old }()

	type counter struct {
		opens  int32
		closes int32
	}
	var c1, c2 counter

	// 手工建对连节点，以便在 Serve 之前注入 Trace
	pc1, pc2 := packet.Pipe()
	n1 := New(pc1)
	n2 := New(pc2)
	n1.SetDomain("trace1")
	n1.SetIP(1)
	n2.SetDomain("trace2")
	n2.SetIP(2)
	n1.SetTrace(&Trace{
		StreamOpen:   func(st *stream.State) { atomic.AddInt32(&c1.opens, 1) },
		StreamClosed: func(st *stream.State) { atomic.AddInt32(&c1.closes, 1) },
	})
	n2.SetTrace(&Trace{
		StreamOpen:   func(st *stream.State) { atomic.AddInt32(&c2.opens, 1) },
		StreamClosed: func(st *stream.State) { atomic.AddInt32(&c2.closes, 1) },
	})
	go n1.Serve()
	go n2.Serve()
	defer n1.Close()
	defer n2.Close()

	l, err := n2.Listen(80)
	assert.Nil(t, err)
	go func() {
		c, err := l.Accept()
		if err == nil {
			io.Copy(io.Discard, c) // 等对端关闭后退出，再关闭本端，避免 close 握手竞态
			c.Close()
		}
	}()

	s, err := n1.Dial("trace2:80")
	assert.Nil(t, err)

	// Dial 成功路径：dial 侧与 accept 侧各触发一次 StreamOpen
	waitForCondition(t, 5*time.Second, func() bool {
		return atomic.LoadInt32(&c1.opens) == 1 && atomic.LoadInt32(&c2.opens) == 1
	}, "StreamOpen should fire once on each side")

	s.Close()

	// 双向关闭后：两侧各记录一次 StreamClosed
	waitForCondition(t, 5*time.Second, func() bool {
		return atomic.LoadInt32(&c1.closes) == 1 && atomic.LoadInt32(&c2.closes) == 1
	}, "StreamClosed should fire once on each side")
}

func TestTraceHeartbeatFail(t *testing.T) {
	failErr := errors.New("boom")

	var failCount int32
	var gotErr atomic.Value
	n := New(nil)
	defer n.Close()
	n.SetTrace(&Trace{
		HeartbeatFail: func(err error) {
			gotErr.Store(err.Error())
			atomic.AddInt32(&failCount, 1)
		},
	})
	n.heartbeat.SetChecker(func() error { return failErr })
	n.heartbeat.interval = time.Millisecond

	go n.heartbeat.run(time.NewTicker(time.Millisecond*10), n.done, func() {})

	waitForCondition(t, 5*time.Second, func() bool {
		return atomic.LoadInt32(&failCount) == 1
	}, "HeartbeatFail should fire once")
	assert.Equal(t, failErr.Error(), gotErr.Load().(string))
	assert.Equal(t, int64(1), n.Inspect().Failures.HeartbeatFailed, "heartbeat failure should be counted")
}

func TestTracePortExhausted(t *testing.T) {
	var sharedCount, pingerCount int32
	n := New(nil)
	defer n.Close()
	n.SetTrace(&Trace{
		PortExhausted: func(pool string, err error) {
			switch pool {
			case "shared":
				atomic.AddInt32(&sharedCount, 1)
			case "pinger":
				atomic.AddInt32(&pingerCount, 1)
			}
		},
	})

	// 耗尽共享池，Dial 应触发一次 PortExhausted("shared", ...)
	for {
		_, err := n.dialer.portm.Allocate()
		if err != nil {
			break
		}
	}
	_, err := n.DialIP(1, 80)
	assert.ErrorIs(t, err, idpool.ErrPoolExhausted)
	assert.Equal(t, int32(1), atomic.LoadInt32(&sharedCount))

	// 耗尽 pinger 独立池，PingDomain 应触发一次 PortExhausted("pinger", ...)
	for {
		_, err := n.pinger.portm.Allocate()
		if err != nil {
			break
		}
	}
	_, err = n.PingDomain("test2", time.Second)
	assert.ErrorIs(t, err, idpool.ErrPoolExhausted)
	assert.Equal(t, int32(1), atomic.LoadInt32(&pingerCount))

	assert.Equal(t, int64(2), n.Inspect().Failures.PortExhausted, "each pool exhaustion should be counted")
}

func TestTraceNilSafe(t *testing.T) {
	var nilTrace *Trace
	nilTrace.streamOpen(nil)
	nilTrace.streamClosed(nil)
	nilTrace.heartbeatFail(nil)
	nilTrace.portExhausted("shared", nil)

	empty := &Trace{}
	empty.streamOpen(nil)
	empty.streamClosed(nil)
	empty.heartbeatFail(nil)
	empty.portExhausted("shared", nil)

	// 未 SetTrace 的节点，埋点应为 no-op
	n := New(nil)
	defer n.Close()
	n.trace.streamOpen(nil)
	n.trace.streamClosed(nil)

	var nilNode *Node
	nilNode.traceStreamOpen(nil)
	nilNode.traceStreamClosed(nil)
}

type captureHandler struct {
	mu    sync.Mutex
	msgs  []string
	attrs []map[string]string
}

func (h *captureHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }
func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.msgs = append(h.msgs, r.Message)
	m := make(map[string]string, r.NumAttrs())
	r.Attrs(func(a slog.Attr) bool {
		m[a.Key] = a.Value.String()
		return true
	})
	h.attrs = append(h.attrs, m)
	return nil
}
func (h *captureHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(_ string) slog.Handler      { return h }
func (h *captureHandler) contains(msg string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, m := range h.msgs {
		if m == msg {
			return true
		}
	}
	return false
}
func (h *captureHandler) containsAttr(msg, key, val string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i, m := range h.msgs {
		if m == msg && h.attrs[i][key] == val {
			return true
		}
	}
	return false
}

func TestSetLoggerPropagatesToComponents(t *testing.T) {
	h := &captureHandler{}
	n := New(nil)
	defer n.Close()
	n.SetDomain("loggerdom")
	n.SetLogger(slog.New(h))

	// dispatcher 路径：在未启动的节点上 dispatch 会打 warn 日志，
	// 且应带上惰性读取的 domain 属性（覆盖原 init 拷贝导致 domain 恒为空的问题）
	n.WriteBuffer(packet.NewBuffer())
	assert.True(t, h.containsAttr("dispatch buffer on stopped node", "domain", "loggerdom"),
		"dispatcher warn log should go to the logger set by SetLogger, with domain attr")

	// heartbeat 路径：checker 失败时打 "check alive failed" warn 日志
	n.heartbeat.SetChecker(func() error { return errors.New("boom") })
	n.heartbeat.interval = time.Millisecond
	go n.heartbeat.run(time.NewTicker(time.Millisecond*10), n.done, func() {})

	waitForCondition(t, 5*time.Second, func() bool {
		return h.contains("check alive failed")
	}, "heartbeat warn log should go to the logger set by SetLogger")

	// nil logger 不应清空已有 logger
	prev := n.logger
	n.SetLogger(nil)
	assert.Equal(t, prev, n.logger)
}

func TestSnapshotRatesSince(t *testing.T) {
	base := time.Now()
	prev := Snapshot{
		At:   base,
		Info: NodeInfo{BytesRead: 1000, BytesWritten: 2000},
		Streams: []*stream.State{
			{Index: 1, BytesRead: 100, BytesWritten: 100},
			{Index: 2, BytesRead: 50, BytesWritten: 50},
		},
	}
	cur := Snapshot{
		At:   base.Add(2 * time.Second),
		Info: NodeInfo{BytesRead: 3000, BytesWritten: 6000},
		Streams: []*stream.State{
			{Index: 1, BytesRead: 300, BytesWritten: 500},
			{Index: 3, BytesRead: 1, BytesWritten: 1}, // 新出现的流不参与速率计算
		},
	}

	r := cur.RatesSince(prev)
	assert.NotNil(t, r)
	assert.Equal(t, 2*time.Second, r.Elapsed)
	assert.Equal(t, 1000.0, r.BytesReadPerSec)
	assert.Equal(t, 2000.0, r.BytesWrittenPerSec)
	assert.Len(t, r.Streams, 1, "only streams present in both snapshots should be rated")
	assert.Equal(t, int32(1), r.Streams[0].Index)
	assert.Equal(t, 100.0, r.Streams[0].BytesReadPerSec)
	assert.Equal(t, 200.0, r.Streams[0].BytesWrittenPerSec)

	assert.Nil(t, prev.RatesSince(cur), "negative elapsed should return nil")
	assert.Nil(t, cur.RatesSince(cur), "zero elapsed should return nil")
}

func TestFailureCounters(t *testing.T) {
	t.Run("dial rejected", func(t *testing.T) {
		n1, n2 := Pipe("test1", "test2")
		defer n1.Close()
		defer n2.Close()

		// test2 未监听 9999 端口，Dial 会被对端拒绝
		_, err := n1.Dial("test2:9999")
		assert.Error(t, err)
		assert.Equal(t, int64(1), n1.Inspect().Failures.DialRejected)
	})

	t.Run("dial timeout", func(t *testing.T) {
		n := New(nil)
		defer n.Close()
		n.SetIP(1)
		n.SetDialTimeout(time.Millisecond * 20)

		// dist 为本节点：包被本地分发（节点未 Serve，直接丢弃），Dial 等到超时
		_, err := n.DialIP(1, 80)
		assert.ErrorIs(t, err, ErrWaitResponseTimeout)
		assert.Equal(t, int64(1), n.Inspect().Failures.DialTimeout)
	})

	t.Run("dial write failed and ping failed", func(t *testing.T) {
		n := New(nil) // pconn 为 nil，写底层连接必失败
		defer n.Close()

		_, err := n.DialIP(1, 80)
		assert.ErrorIs(t, err, ErrWriteDialPbufFailed)
		assert.Equal(t, int64(1), n.Inspect().Failures.DialWriteFailed)

		_, err = n.PingDomain("test2", time.Second)
		assert.Error(t, err)
		assert.Equal(t, int64(1), n.Inspect().Failures.PingFailed)
	})

	t.Run("ping timeout", func(t *testing.T) {
		n1, n2 := Pipe("test1", "test2")
		defer n1.Close()
		defer n2.Close()
		n2.pinger.SetIgnorePing(true)

		_, err := n1.PingDomain("test2", time.Millisecond*20)
		assert.ErrorIs(t, err, ErrPingDomainTimeout)
		assert.Equal(t, int64(1), n1.Inspect().Failures.PingFailed)
	})
}

func TestHeartbeatLastRTT(t *testing.T) {
	n := New(nil)
	defer n.Close()

	assert.Equal(t, time.Duration(0), n.Inspect().Heartbeat.LastRTT, "RTT should be zero before first probe")

	n.heartbeat.setLastRTT(123 * time.Millisecond)
	assert.Equal(t, 123*time.Millisecond, n.Inspect().Heartbeat.LastRTT, "snapshot should expose last probed RTT")
}
