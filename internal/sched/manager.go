package sched

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v3/packet"
)

const (
	// DefaultControlQueueSize 控制包通道的缓冲区大小。
	// 控制包（如 CmdOpenStream、AckPushStreamData 等）通过独立的高优先级通道发送，
	// 不经过 per-stream 队列。该值决定了控制包通道的容量，当通道满时写入方将阻塞。
	// 128 足以应对突发的控制包高峰（如同时打开多条 stream）。
	DefaultControlQueueSize = 128

	// DefaultQuantum 每条流（SID）在一轮调度中最多允许发送的数据包数量。
	// 这是 Deficit Round Robin (DRR) 算法的核心参数：
	//   - 调度器从 readyQueue 取出一个 SID 后，最多 Drain(quantum) 个包一次性写入底层连接，
	//     然后让出发送机会给下一个就绪的 SID。
	//   - 值越小，流间切换越频繁，公平性越好，但调度开销增大；
	//     值越大，单流连续发送越多，吞吐效率越高，但公平性下降。
	//   - 默认值 2 偏向公平性：2 个包约 2KB（假设 1024B payload），
	//     大约对应 1-2 个 TCP MSS。
	DefaultQuantum = 2

	// ReadyQueueSize 就绪队列（readyQueue channel）的缓冲区大小。
	// readyQueue 存放有待发送数据的 SID。当某条流有新数据到达且尚未在队列中时，
	// 其 SID 被 push 进 readyQueue；调度循环从中取出 SID 并发送其数据。
	// 1024 的容量足以支撑大量并发流，避免因队列满而导致写入方阻塞。
	ReadyQueueSize = 1024
)

var ErrWriterClosed = errors.New("fair writer closed")

// cloneBuffer creates a deep copy of a packet.Buffer so that the original
// can be safely reused by the caller (e.g. Sender's pre-allocated buffers).
// 克隆体取自 packet 池，写完由 FairWriter 统一 PutBuffer 归还。
func cloneBuffer(buf *packet.Buffer) *packet.Buffer {
	clone := packet.GetBuffer()
	clone.Head = buf.Head // array copy (value type)
	if len(buf.Payload) > 0 {
		if cap(clone.Payload) < len(buf.Payload) {
			clone.Payload = make([]byte, len(buf.Payload))
		} else {
			clone.Payload = clone.Payload[:len(buf.Payload)]
		}
		copy(clone.Payload, buf.Payload)
	}
	return clone
}

type FairWriter struct {
	writer packet.Writer
	cfg    SchedulerConfig

	// scratch 只在 loop goroutine 内使用，复用以避免每次 visit 分配
	// []*packet.Buffer（旧实现每次 processStream 都 make 一次）。
	scratch []*packet.Buffer

	controlCh  chan *packet.Buffer
	streams    map[uint64]*StreamQueue
	mu         sync.Mutex // protects streams map only
	readyQueue chan uint64
	done       chan struct{}
	closeOne   sync.Once
	closed     atomic.Bool
}

// StreamQueue is a per-stream packet queue using slice+mutex instead of channel.
type StreamQueue struct {
	mu     sync.Mutex
	queue  []*packet.Buffer
	active atomic.Bool // whether this stream's SID is in readyQueue

	// closed 标记该队列已见到 close 类包（该方向流的最后一个包），
	// 仅由 FairWriter.mu 保护读写，供 processStream 在排空后回收条目
	closed bool
}

func (sq *StreamQueue) Push(buf *packet.Buffer) {
	sq.mu.Lock()
	sq.queue = append(sq.queue, buf)
	sq.mu.Unlock()
}

// Drain removes and returns up to n packets from the queue.
//
// The FairWriter no longer uses Drain: it calls drainInto/drainCostInto with a
// reusable scratch slice. Drain is kept for callers/tests that need an owning
// slice.
func (sq *StreamQueue) Drain(n int) []*packet.Buffer {
	sq.mu.Lock()
	l := len(sq.queue)
	if l == 0 {
		sq.mu.Unlock()
		return nil
	}
	if n > l {
		n = l
	}
	out := make([]*packet.Buffer, n)
	copy(out, sq.queue[:n])
	copy(sq.queue, sq.queue[n:])
	sq.queue = sq.queue[:l-n]
	sq.mu.Unlock()
	return out
}

// drainInto removes up to limit packets into dst and returns the count.
// dst is owned by the caller (the FairWriter scratch); the queue only copies
// pointers into it. The caller must pass a dst with len(dst) >= limit.
func (sq *StreamQueue) drainInto(dst []*packet.Buffer, limit int) int {
	sq.mu.Lock()
	defer sq.mu.Unlock()

	n := limit
	if n > len(sq.queue) {
		n = len(sq.queue)
	}
	if n > len(dst) {
		n = len(dst)
	}
	if n <= 0 {
		return 0
	}

	copy(dst[:n], sq.queue[:n])
	copy(sq.queue, sq.queue[n:])
	sq.queue = sq.queue[:len(sq.queue)-n]
	return n
}

// drainCostInto removes the longest prefix of packets whose charged cost
// max(payload, minCharge) fits budget. The first packet is always taken, so a
// single packet larger than the budget is an allowed overshoot.
//
// 该函数是 B 方案（成本计费）的核心：预算以近似线缆成本计，
// 每次 visit 的包数天然被 budget/minCharge 限制，1B 包无法独占链路。
func (sq *StreamQueue) drainCostInto(dst []*packet.Buffer, budget, minCharge int) int {
	sq.mu.Lock()
	defer sq.mu.Unlock()

	limit := len(dst)
	if limit > len(sq.queue) {
		limit = len(sq.queue)
	}

	n, spent := 0, 0
	for n < limit {
		charge := len(sq.queue[n].Payload)
		if charge < minCharge {
			charge = minCharge
		}
		if n > 0 && spent+charge > budget {
			break
		}
		spent += charge
		n++
	}
	if n == 0 {
		return 0
	}

	copy(dst[:n], sq.queue[:n])
	copy(sq.queue, sq.queue[n:])
	sq.queue = sq.queue[:len(sq.queue)-n]
	return n
}

func (sq *StreamQueue) Len() int {
	sq.mu.Lock()
	n := len(sq.queue)
	sq.mu.Unlock()
	return n
}

// NewFairWriter creates a writer with the default cost-budget policy.
// Passing an explicit quantum selects the packet-quantum policy (compatibility
// with the previous API); without it, the cost-budget default is used.
func NewFairWriter(w packet.Writer, quantum ...int) *FairWriter {
	if len(quantum) > 0 && quantum[0] > 0 {
		return NewFairWriterWithConfig(w, SchedulerConfig{
			Policy:         PolicyPacketQuantum,
			QuantumPackets: quantum[0],
		})
	}
	return NewFairWriterWithConfig(w, DefaultSchedulerConfig())
}

// NewFairWriterWithConfig creates a writer with an explicit scheduling policy.
// The policy is immutable for the lifetime of the writer.
func NewFairWriterWithConfig(w packet.Writer, cfg SchedulerConfig) *FairWriter {
	return newFairWriter(w, cfg, true)
}

// newFairWriter optionally defers starting the loop, which lets tests enqueue a
// deterministic backlog before the first scheduling decision.
func newFairWriter(w packet.Writer, cfg SchedulerConfig, start bool) *FairWriter {
	cfg = cfg.normalized()
	fw := &FairWriter{
		writer:     w,
		cfg:        cfg,
		scratch:    make([]*packet.Buffer, cfg.visitLimit()),
		controlCh:  make(chan *packet.Buffer, DefaultControlQueueSize),
		streams:    make(map[uint64]*StreamQueue),
		readyQueue: make(chan uint64, ReadyQueueSize),
		done:       make(chan struct{}),
	}
	if start {
		go fw.loop()
	}
	return fw
}

// WriteBuffer 入队一个 buffer 的深拷贝，调用方保留原 buffer 的所有权。
// 深拷贝是必须的：调用方可能复用预分配的 Buffer（如 stream Sender 的
// dataBuf/closeBuf），异步写出时原 buffer 可能已被覆写。
func (fw *FairWriter) WriteBuffer(buf *packet.Buffer) error {
	if fw.closed.Load() {
		return ErrWriterClosed
	}
	clone := cloneBuffer(buf)
	if err := fw.enqueue(clone); err != nil {
		packet.PutBuffer(clone)
		return err
	}
	return nil
}

// WriteBufferOwned 与 WriteBuffer 行为一致，但不复制 buffer：
// 调用方将所有权转移给 FairWriter，写完后由 FairWriter 归还 packet 池。
// 返回后调用方不得再访问 buf 及其 Payload；返回错误表示所有权未转移，
// 回收责任仍在调用方。
func (fw *FairWriter) WriteBufferOwned(buf *packet.Buffer) error {
	if fw.closed.Load() {
		return ErrWriterClosed
	}
	return fw.enqueue(buf)
}

// enqueue 把 buffer 放入调度队列。入队成功的 buffer 由写出路径（loop /
// drainControl / processStream）在写完后统一 PutBuffer 归还。
func (fw *FairWriter) enqueue(buf *packet.Buffer) error {
	// Per-stream ordered commands: CmdPushStreamData, CmdCloseStream, AckCloseStream
	// These must go through the per-stream queue to preserve ordering with data packets.
	// All other commands (CmdOpenStream, AckPushStreamData, CmdPingDomain, etc.) are
	// cross-stream control packets that can use the high-priority control channel.
	cmd := buf.Cmd()
	if cmd != packet.CmdPushStreamData &&
		cmd != packet.CmdCloseStream &&
		cmd != packet.AckCloseStream {
		select {
		case fw.controlCh <- buf:
			return nil
		case <-fw.done:
			return ErrWriterClosed
		}
	}

	sid := buf.SID()
	fw.mu.Lock()
	sq, exists := fw.streams[sid]
	if !exists {
		sq = &StreamQueue{}
		fw.streams[sid] = sq
	}
	// Push 与条目回收（processStream 的 delete）同在 fw.mu 下串行，
	// 避免"回收入队检查"与"生产入队"之间的孤儿队列竞态
	sq.Push(buf)
	if cmd == packet.CmdCloseStream || cmd == packet.AckCloseStream {
		sq.closed = true
	}
	fw.mu.Unlock()

	// Activate stream if not already in readyQueue (atomic CAS, no mutex needed)
	if sq.active.CompareAndSwap(false, true) {
		select {
		case fw.readyQueue <- sid:
		case <-fw.done:
			return ErrWriterClosed
		}
	}

	return nil
}

func (fw *FairWriter) SetWriteTimeout(dur time.Duration) {
	fw.writer.SetWriteTimeout(dur)
}

func (fw *FairWriter) Close() error {
	fw.closeOne.Do(func() {
		fw.closed.Store(true)
		close(fw.done)
	})
	return nil
}

func (fw *FairWriter) loop() {
	for {
		select {
		case <-fw.done:
			return
		case buf := <-fw.controlCh:
			fw.writer.WriteBuffer(buf)
			packet.PutBuffer(buf)
			continue
		case sid := <-fw.readyQueue:
			// Preemption: drain control channel first
			fw.drainControl()
			fw.processStream(sid)
		}
	}
}

func (fw *FairWriter) drainControl() {
	for {
		select {
		case buf := <-fw.controlCh:
			fw.writer.WriteBuffer(buf)
			packet.PutBuffer(buf)
		default:
			return
		}
	}
}

func (fw *FairWriter) processStream(sid uint64) {
	fw.mu.Lock()
	sq, exists := fw.streams[sid]
	fw.mu.Unlock()
	if !exists {
		return
	}

	n := fw.takePackets(sq)
	if n > 0 {
		bufs := fw.scratch[:n]
		if bw, ok := fw.writer.(packet.BatchWriter); ok {
			bw.WriteBufferBatch(bufs)
		} else {
			for _, buf := range bufs {
				fw.writer.WriteBuffer(buf)
			}
		}
		// 内置 Writer 均为同步写、返回后不持有 buffer，此处统一归还。
		// 清空 scratch 中的指针，避免其延长 buffer 生命周期。
		for i := 0; i < n; i++ {
			packet.PutBuffer(bufs[i])
			fw.scratch[i] = nil
		}
	}

	// Re-queue if more data, otherwise reap or deactivate
	if sq.Len() > 0 {
		fw.requeue(sid)
		return
	}

	fw.mu.Lock()
	if sq.closed && sq.Len() == 0 {
		// 该方向的流已结束（close 类包是其最后一个包）且队列排空，回收条目。
		// SID 被复用时条目按需重建，因此及时删除不会丢包
		delete(fw.streams, sid)
		fw.mu.Unlock()
		return
	}
	fw.mu.Unlock()

	sq.active.Store(false)
	// Double-check: producer may have pushed between Drain and Store
	if sq.Len() > 0 && sq.active.CompareAndSwap(false, true) {
		fw.requeue(sid)
	}
}

// takePackets drains one visit worth of packets into the reusable scratch.
func (fw *FairWriter) takePackets(sq *StreamQueue) int {
	if fw.cfg.Policy == PolicyCostBudget {
		return sq.drainCostInto(fw.scratch, fw.cfg.QuantumBytes, fw.cfg.MinChargeBytes)
	}
	return sq.drainInto(fw.scratch, fw.cfg.QuantumPackets)
}

// requeue 把 SID 放回就绪队列；队列满时退化为异步投递（done 后放弃，避免 goroutine 泄漏）
func (fw *FairWriter) requeue(sid uint64) {
	select {
	case fw.readyQueue <- sid:
	default:
		go func() {
			select {
			case fw.readyQueue <- sid:
			case <-fw.done:
			}
		}()
	}
}
