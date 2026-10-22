package stream

import (
	"errors"
	"io"
	"sync"

	"github.com/net-agent/flex/v3/packet"
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
	// 海量小包（交互式协议常见）若各自占用一个 Buffer，会把内存放大
	// 数十倍；合并后队列对象数只与总字节数有关。
	coalesceThreshold = 1024

	// recvChunkSize 是合并 chunk 的容量。
	recvChunkSize = 16 * KB
)

// recvBuffer 是单条流的有界接收队列，容量以“字节”而不是“包数”计。
//
// 旧实现用 chan []byte（容量 = window/split*4 = 512 个包）承载接收数据，
// 与基于字节的流控窗口是两套计量单位：1 字节的小包只需 512 字节就会占满
// 队列，导致一条只传了几百字节的流被误判为拥塞并被关闭。
//
// 新实现的约束：
//   - 以字节数为上限，与发送端窗口同量纲，合法对端永远不会溢出；
//   - append 永不阻塞，避免单条慢流把 dispatcher goroutine 拖住（跨流队头阻塞）；
//   - 大包零拷贝入队，小包合并，出队消费后 Buffer 归还 packet 池。
type recvBuffer struct {
	mu     sync.Mutex
	chunks []*packet.Buffer
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

// append 入队一个数据包。返回 nil 表示所有权已转移给队列
// （小包合并时队列会自行归还入参 Buffer）；返回错误时调用方仍持有 Buffer。
func (q *recvBuffer) append(buf *packet.Buffer) error {
	n := len(buf.Payload)
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
		queued = q.coalesceLocked(buf.Payload)
	}
	if !queued {
		q.chunks = append(q.chunks, buf)
	}
	q.bytes += n
	q.mu.Unlock()

	if queued {
		// payload 已复制进尾部 chunk，入参可以立即归还池子
		packet.PutBuffer(buf)
	}
	q.signal()
	return nil
}

// coalesceLocked 尝试把小 payload 追加到尾部 chunk。调用方持有 q.mu。
func (q *recvBuffer) coalesceLocked(p []byte) bool {
	if len(q.chunks) > q.head {
		tail := q.chunks[len(q.chunks)-1]
		if cap(tail.Payload)-len(tail.Payload) >= len(p) {
			tail.Payload = append(tail.Payload, p...)
			return true
		}
	}

	chunk := packet.GetBuffer()
	chunk.Payload = make([]byte, 0, recvChunkSize)
	chunk.Payload = append(chunk.Payload, p...)
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
		n := copy(p[total:], c.Payload)
		total += n
		c.Payload = c.Payload[n:]
		if len(c.Payload) == 0 {
			packet.PutBuffer(c)
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

// discard 释放尚未被消费的所有 Buffer（流完全关闭后调用）。
func (q *recvBuffer) discard() {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := q.head; i < len(q.chunks); i++ {
		packet.PutBuffer(q.chunks[i])
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
