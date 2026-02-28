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
	//   - 默认值 4 在公平性和吞吐之间取得平衡：4 个包约 4KB（假设 1024B payload），
	//     大约对应 2-3 个 TCP MSS，适合批量写入。
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
func cloneBuffer(buf *packet.Buffer) *packet.Buffer {
	clone := &packet.Buffer{}
	clone.Head = buf.Head // array copy (value type)
	if len(buf.Payload) > 0 {
		clone.Payload = make([]byte, len(buf.Payload))
		copy(clone.Payload, buf.Payload)
	}
	return clone
}

type FairWriter struct {
	writer  packet.Writer
	quantum int

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
}

func (sq *StreamQueue) Push(buf *packet.Buffer) {
	sq.mu.Lock()
	sq.queue = append(sq.queue, buf)
	sq.mu.Unlock()
}

// Drain removes and returns up to n packets from the queue.
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

func (sq *StreamQueue) Len() int {
	sq.mu.Lock()
	n := len(sq.queue)
	sq.mu.Unlock()
	return n
}

func NewFairWriter(w packet.Writer, quantum ...int) *FairWriter {
	q := DefaultQuantum
	if len(quantum) > 0 && quantum[0] > 0 {
		q = quantum[0]
	}
	fw := &FairWriter{
		writer:     w,
		quantum:    q,
		controlCh:  make(chan *packet.Buffer, DefaultControlQueueSize),
		streams:    make(map[uint64]*StreamQueue),
		readyQueue: make(chan uint64, ReadyQueueSize),
		done:       make(chan struct{}),
	}
	go fw.loop()
	return fw
}

func (fw *FairWriter) WriteBuffer(buf *packet.Buffer) error {
	if fw.closed.Load() {
		return ErrWriterClosed
	}

	// Per-stream ordered commands: CmdPushStreamData, CmdCloseStream, AckCloseStream
	// These must go through the per-stream queue to preserve ordering with data packets.
	// All other commands (CmdOpenStream, AckPushStreamData, CmdPingDomain, etc.) are
	// cross-stream control packets that can use the high-priority control channel.
	cmd := buf.Cmd()
	if cmd != packet.CmdPushStreamData &&
		cmd != packet.CmdCloseStream &&
		cmd != packet.AckCloseStream {
		// Control packets: deep copy because the caller may reuse the Buffer
		// (e.g. Sender.dataAckPbuf is pre-allocated and reused).
		clone := cloneBuffer(buf)
		select {
		case fw.controlCh <- clone:
			return nil
		case <-fw.done:
			return ErrWriterClosed
		}
	}

	// Per-stream ordered packets: deep copy because Sender reuses pre-allocated
	// Buffer objects (dataPbuf, closePbuf, closeAckPbuf). Without copying, the
	// loop() goroutine may read stale/overwritten Head and Payload when it
	// asynchronously drains the StreamQueue.
	clone := cloneBuffer(buf)
	sid := clone.SID()
	fw.mu.Lock()
	sq, exists := fw.streams[sid]
	if !exists {
		sq = &StreamQueue{}
		fw.streams[sid] = sq
	}
	fw.mu.Unlock()

	sq.Push(clone)

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

	bufs := sq.Drain(fw.quantum)
	if len(bufs) > 0 {
		if bw, ok := fw.writer.(packet.BatchWriter); ok {
			bw.WriteBufferBatch(bufs)
		} else {
			for _, buf := range bufs {
				fw.writer.WriteBuffer(buf)
			}
		}
	}

	// Re-queue if more data, otherwise deactivate
	if sq.Len() > 0 {
		select {
		case fw.readyQueue <- sid:
		default:
			go func() { fw.readyQueue <- sid }()
		}
	} else {
		sq.active.Store(false)
		// Double-check: producer may have pushed between Drain and Store
		if sq.Len() > 0 && sq.active.CompareAndSwap(false, true) {
			select {
			case fw.readyQueue <- sid:
			default:
				go func() { fw.readyQueue <- sid }()
			}
		}
	}
}
