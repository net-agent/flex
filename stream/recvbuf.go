package stream

import (
	"errors"
	"io"
	"sync"

	"github.com/net-agent/flex/v3/wire"
)

// ErrRecvBufferFull 表示接收端缓冲已满，而对端仍在流控窗口之外继续推送数据。
// 正常情况下基于字节的窗口保证不会发生；一旦出现即说明对端违反约定，
// 必须 fail-loud 关闭流，绝不能静默丢包（丢包会破坏两端窗口记账）。
var ErrRecvBufferFull = errors.New("stream recv buffer full")

// errRecvClosed 表示接收侧已经关闭，新到的数据应当丢弃。
var errRecvClosed = errors.New("stream recv side closed")

// errEmptyPayload 表示空数据包，直接丢弃不上账。
var errEmptyPayload = errors.New("empty payload")

const (
	// coalesceThreshold 以下的 payload 会被合并进尾部 chunk。
	// 海量小包（交互式协议常见）若各自占用一个 Frame，会把内存放大
	// 数十倍；合并后队列对象数只与总字节数有关。
	coalesceThreshold = 1024

	// recvChunkSize 是合并 chunk 的容量。
	recvChunkSize = 16 * KB
)

// recvChunk is either a zero-copy view of a Frame payload (frame != nil) or a
// coalesced []byte owned by the queue. A Frame payload is immutable, so small
// packets are copied into a coalesced chunk rather than appended to a frame.
type recvChunk struct {
	data  []byte
	frame *wire.Frame
}

// recvBuffer 是单条流的有界接收队列，容量以“字节”而不是“包数”计。
//
// 约束：
//   - 以字节数为上限，与发送端窗口同量纲，合法对端永远不会溢出；
//   - append 永不阻塞，避免单条慢流把 dispatcher goroutine 拖住；
//   - 大包零拷贝入队，小包合并，出队消费后 Frame 归还池子。
type recvBuffer struct {
	mu     sync.Mutex
	chunks []*recvChunk
	head   int
	bytes  int
	limit  int
	closed bool

	notify chan struct{} // 有新数据，容量 1
	done   chan struct{} // 读侧关闭
}

func newRecvBuffer(limit int32) *recvBuffer {
	n := int(limit)
	if n <= 0 {
		n = int(DefaultWindowSize)
	}
	// 至少容纳一个最大分片，避免小窗口配置下单个合法分片就溢出。
	if n < DefaultSplitSize {
		n = DefaultSplitSize
	}
	return &recvBuffer{
		limit:  n,
		notify: make(chan struct{}, 1),
		done:   make(chan struct{}),
	}
}

// append queues a data frame. It returns nil when ownership transferred to the
// queue (coalesced frames are released by the queue); on error the caller still
// owns the frame.
func (q *recvBuffer) append(f *wire.Frame) error {
	n := len(f.Payload())
	if n == 0 {
		return errEmptyPayload
	}

	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return errRecvClosed
	}
	if q.bytes+n > q.limit {
		q.mu.Unlock()
		return ErrRecvBufferFull
	}

	queued := false
	if n < coalesceThreshold {
		queued = q.coalesceLocked(f.Payload())
	}
	if !queued {
		q.chunks = append(q.chunks, &recvChunk{data: f.Payload(), frame: f})
	}
	q.bytes += n
	q.mu.Unlock()

	if queued {
		f.Release()
	}
	q.signal()
	return nil
}

// coalesceLocked 尝试把小 payload 追加到尾部 chunk。调用方持有 q.mu。
func (q *recvBuffer) coalesceLocked(p []byte) bool {
	if len(q.chunks) > q.head {
		tail := q.chunks[len(q.chunks)-1]
		if tail.frame == nil && cap(tail.data)-len(tail.data) >= len(p) {
			tail.data = append(tail.data, p...)
			return true
		}
	}

	chunk := &recvChunk{data: make([]byte, 0, recvChunkSize)}
	chunk.data = append(chunk.data, p...)
	q.chunks = append(q.chunks, chunk)
	return true
}

// read 从队首拷贝数据。返回 (0, nil) 表示暂时无数据且未关闭。
func (q *recvBuffer) read(p []byte) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if q.bytes == 0 {
		if q.closed {
			return 0, io.EOF
		}
		return 0, nil
	}

	total := 0
	for total < len(p) && q.head < len(q.chunks) {
		c := q.chunks[q.head]
		n := copy(p[total:], c.data)
		total += n
		c.data = c.data[n:]
		if len(c.data) == 0 {
			if c.frame != nil {
				c.frame.Release()
			}
			q.chunks[q.head] = nil
			q.head++
		}
	}
	q.bytes -= total

	switch {
	case q.head == len(q.chunks):
		q.chunks = q.chunks[:0]
		q.head = 0
	case q.head >= 64 && q.head*2 >= len(q.chunks):
		// 及时压缩头部空洞，避免 slice 无限增长
		copy(q.chunks, q.chunks[q.head:])
		q.chunks = q.chunks[:len(q.chunks)-q.head]
		q.head = 0
	}

	return total, nil
}

// close 关闭读侧。已缓冲的数据保留，允许调用方继续排空（与 net.Conn 一致：
// Close 之后的读会排空缓冲再返回 EOF）。
func (q *recvBuffer) close() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	q.closed = true
	close(q.done)
	return true
}

// discard 释放尚未被消费的所有 chunk（流完全关闭后调用）。
func (q *recvBuffer) discard() {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := q.head; i < len(q.chunks); i++ {
		if c := q.chunks[i]; c != nil && c.frame != nil {
			c.frame.Release()
		}
	}
	q.chunks = nil
	q.head = 0
	q.bytes = 0
}

func (q *recvBuffer) signal() {
	select {
	case q.notify <- struct{}{}:
	default:
	}
}

func (q *recvBuffer) pending() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.bytes
}
