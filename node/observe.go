package node

import (
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v3/stream"
)

// Snapshot 是 Node 内部状态的一致性快照，全部字段通过只读方式收集。
type Snapshot struct {
	At        time.Time       `json:"at"` // 快照采样时间，用于跨快照计算速率
	Info      NodeInfo        `json:"info"`
	Running   bool            `json:"running"`
	Listeners []ListenerInfo  `json:"listeners"`
	Streams   []*stream.State `json:"streams"`
	PortPools []PoolStats     `json:"port_pools"`
	Pending   PendingStats    `json:"pending"`
	Heartbeat HeartbeatStats  `json:"heartbeat"`
	Failures  FailureStats    `json:"failures"`
}

// PoolStats 描述一个虚拟端口池的占用情况。
type PoolStats struct {
	Name     string `json:"name"` // "shared"（listenHub/dialer/streamHub 共用）与 "pinger"
	InUse    int    `json:"in_use"`
	Capacity int    `json:"capacity"`
}

// PendingStats 描述等待对端应答的请求数量。
type PendingStats struct {
	Dial int `json:"dial"`
	Ping int `json:"ping"`
}

// HeartbeatStats 描述心跳组件的运行状态。
type HeartbeatStats struct {
	Interval  time.Duration `json:"interval"`
	LastWrite time.Time     `json:"last_write"`
	LastRTT   time.Duration `json:"last_rtt"` // 最近一次探活测得的到中转节点的 RTT，未测得时为 0
}

// FailureStats 累计节点运行期间的失败事件次数。
// 与 Trace 的一次性事件钩子互补：钩子可能漏接，计数器始终可拉取。
type FailureStats struct {
	DialTimeout     int64 `json:"dial_timeout"`      // Dial 等待应答超时
	DialRejected    int64 `json:"dial_rejected"`     // Dial 被对端拒绝
	DialWriteFailed int64 `json:"dial_write_failed"` // Dial 请求写入底层连接失败
	PingFailed      int64 `json:"ping_failed"`       // Ping 超时、被拒或写失败
	WatchFailed     int64 `json:"watch_failed"`      // Watch/Unwatch 超时、被拒或写失败
	HeartbeatFailed int64 `json:"heartbeat_failed"`  // 心跳探活失败
	PortExhausted   int64 `json:"port_exhausted"`    // 端口池耗尽（shared 与 pinger 合计）
}

// Rates 描述两个 Snapshot 之间的流量速率。
type Rates struct {
	Elapsed            time.Duration `json:"elapsed"`
	BytesReadPerSec    float64       `json:"bytes_read_per_sec"`
	BytesWrittenPerSec float64       `json:"bytes_written_per_sec"`
	Streams            []StreamRate  `json:"streams,omitempty"`
}

// StreamRate 描述单条流在两个 Snapshot 之间的速率。
type StreamRate struct {
	Index              int32   `json:"index"`
	BytesReadPerSec    float64 `json:"bytes_read_per_sec"`
	BytesWrittenPerSec float64 `json:"bytes_written_per_sec"`
}

// RatesSince 以 prev 快照为基准计算速率，prev 应先于 cur 采样。
// 两个快照的采样间隔非正时返回 nil。
// Streams 只包含两个快照中都存在的流（按 State.Index 匹配）。
func (cur Snapshot) RatesSince(prev Snapshot) *Rates {
	elapsed := cur.At.Sub(prev.At)
	if elapsed <= 0 {
		return nil
	}
	sec := elapsed.Seconds()
	r := &Rates{
		Elapsed:            elapsed,
		BytesReadPerSec:    float64(cur.Info.BytesRead-prev.Info.BytesRead) / sec,
		BytesWrittenPerSec: float64(cur.Info.BytesWritten-prev.Info.BytesWritten) / sec,
	}

	prevStreams := make(map[int32]*stream.State, len(prev.Streams))
	for _, st := range prev.Streams {
		if st != nil {
			prevStreams[st.Index] = st
		}
	}
	for _, st := range cur.Streams {
		if st == nil {
			continue
		}
		old, ok := prevStreams[st.Index]
		if !ok {
			continue
		}
		r.Streams = append(r.Streams, StreamRate{
			Index:              st.Index,
			BytesReadPerSec:    float64(st.BytesRead-old.BytesRead) / sec,
			BytesWrittenPerSec: float64(st.BytesWritten-old.BytesWritten) / sec,
		})
	}
	return r
}

// IsRunning 返回节点的 dispatcher 是否正在运行。
func (node *Node) IsRunning() bool {
	return node.dispatcher.isRunning()
}

// Inspect 返回 Node 内部状态的一致性快照。
func (node *Node) Inspect() Snapshot {
	return Snapshot{
		At:        time.Now(),
		Info:      *node.GetInfo(),
		Running:   node.IsRunning(),
		Listeners: node.GetListeners(),
		Streams:   node.GetStreamStates(),
		PortPools: []PoolStats{
			{Name: "shared", InUse: node.streamHub.portm.InUse(), Capacity: node.streamHub.portm.Capacity()},
			{Name: "pinger", InUse: node.pinger.portm.InUse(), Capacity: node.pinger.portm.Capacity()},
		},
		Pending: PendingStats{
			Dial: node.dialer.pending.Len(),
			Ping: node.pinger.pending.Len(),
		},
		Heartbeat: HeartbeatStats{
			Interval:  node.heartbeat.interval,
			LastWrite: time.Unix(0, node.heartbeat.lastWriteNano()),
			LastRTT:   node.heartbeat.lastRTT(),
		},
		Failures: FailureStats{
			DialTimeout:     atomic.LoadInt64(&node.failures.DialTimeout),
			DialRejected:    atomic.LoadInt64(&node.failures.DialRejected),
			DialWriteFailed: atomic.LoadInt64(&node.failures.DialWriteFailed),
			PingFailed:      atomic.LoadInt64(&node.failures.PingFailed),
			WatchFailed:     atomic.LoadInt64(&node.failures.WatchFailed),
			HeartbeatFailed: atomic.LoadInt64(&node.failures.HeartbeatFailed),
			PortExhausted:   atomic.LoadInt64(&node.failures.PortExhausted),
		},
	}
}

// Trace 定义节点级生命周期事件钩子，借鉴 net/http/httptrace 的设计：
// 字段为 nil 即关闭，新增字段向后兼容。通过 SetTrace 注入，仅应在 Serve 之前调用。
type Trace struct {
	StreamOpen    func(*stream.State)
	StreamClosed  func(*stream.State)
	HeartbeatFail func(err error)
	PortExhausted func(pool string, err error)
}

// SetTrace 设置节点级事件钩子。仅应在 Serve 之前调用。
func (node *Node) SetTrace(t *Trace) {
	node.trace = t
}

func (t *Trace) streamOpen(st *stream.State) {
	if t != nil && t.StreamOpen != nil {
		t.StreamOpen(st)
	}
}

func (t *Trace) streamClosed(st *stream.State) {
	if t != nil && t.StreamClosed != nil {
		t.StreamClosed(st)
	}
}

func (t *Trace) heartbeatFail(err error) {
	if t != nil && t.HeartbeatFail != nil {
		t.HeartbeatFail(err)
	}
}

func (t *Trace) portExhausted(pool string, err error) {
	if t != nil && t.PortExhausted != nil {
		t.PortExhausted(pool, err)
	}
}

// 以下两个转发方法供 streamHub 调用。streamHub 的部分单测以 nil host 初始化，
// 在 *Node 上统一做 nil 兜底，使调用点保持一行：hub.host.traceStreamOpen(st)。
func (node *Node) traceStreamOpen(st *stream.State) {
	if node != nil {
		node.trace.streamOpen(st)
	}
}

func (node *Node) traceStreamClosed(st *stream.State) {
	if node != nil {
		node.trace.streamClosed(st)
	}
}

// tracePortExhausted 累计端口耗尽失败并触发 Trace 钩子，供 dialer/pinger 调用。
func (node *Node) tracePortExhausted(pool string, err error) {
	if node == nil {
		return
	}
	atomic.AddInt64(&node.failures.PortExhausted, 1)
	node.trace.portExhausted(pool, err)
}
