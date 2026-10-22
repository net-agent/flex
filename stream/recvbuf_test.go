package stream

import (
	"bytes"
	"io"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/packet"
	"github.com/stretchr/testify/assert"
)

func newTestBuf(payload []byte) *packet.Buffer {
	b := packet.NewBufferWithCmd(packet.CmdPushStreamData)
	_ = b.SetPayload(payload)
	return b
}

// 接收队列必须按字节计量：小包不会因为“包数”提前触发上限。
func TestRecvBufferByteBound(t *testing.T) {
	q := newRecvBuffer(DefaultSplitSize) // 16KB

	payload := make([]byte, 1024)
	for i := 0; i < 16; i++ {
		payload[0] = byte(i)
		assert.NoError(t, q.append(newTestBuf(payload)))
	}
	assert.Equal(t, DefaultSplitSize, q.pending())

	// 再放 1 字节即超限
	assert.ErrorIs(t, q.append(newTestBuf([]byte("x"))), ErrRecvBufferFull)

	// 已有数据必须完整可读
	out := make([]byte, DefaultSplitSize)
	n, err := q.read(out)
	assert.NoError(t, err)
	assert.Equal(t, DefaultSplitSize, n)
	assert.Equal(t, 0, q.pending())
}

// 海量小包必须被合并，队列对象数与包数无关。
func TestRecvBufferCoalescesSmallPackets(t *testing.T) {
	q := newRecvBuffer(64 * KB)

	const packets = 500
	const size = 20
	want := make([]byte, 0, packets*size)
	for i := 0; i < packets; i++ {
		p := bytes.Repeat([]byte{byte(i)}, size)
		want = append(want, p...)
		assert.NoError(t, q.append(newTestBuf(p)))
	}

	q.mu.Lock()
	chunks := len(q.chunks) - q.head
	q.mu.Unlock()
	assert.LessOrEqual(t, chunks, 2, "small packets should be coalesced, got %d chunks", chunks)
	assert.Equal(t, len(want), q.pending())

	out := make([]byte, len(want))
	n, err := q.read(out)
	assert.NoError(t, err)
	assert.Equal(t, len(want), n)
	assert.Equal(t, want, out[:n])
}

// 大包按零拷贝入队，读完后 Buffer 归还池子（Head 被清零）。
func TestRecvBufferLargePacketZeroCopyAndRelease(t *testing.T) {
	q := newRecvBuffer(DefaultSplitSize)

	b := newTestBuf(make([]byte, 2*KB))
	assert.NoError(t, q.append(b))

	q.mu.Lock()
	assert.Same(t, b, q.chunks[q.head], "large packet should be queued without copy")
	q.mu.Unlock()

	out := make([]byte, 2*KB)
	n, _ := q.read(out)
	assert.Equal(t, 2*KB, n)
	assert.Equal(t, packet.Header{}, b.Head, "buffer must be returned to the pool after consumption")
}

// 队列满时必须立即返回错误，绝不能阻塞 dispatcher。
func TestRecvBufferFullNeverBlocks(t *testing.T) {
	q := newRecvBuffer(DefaultSplitSize)
	fill := newTestBuf(make([]byte, DefaultSplitSize))
	assert.NoError(t, q.append(fill))

	done := make(chan error, 1)
	go func() { done <- q.append(newTestBuf([]byte("x"))) }()

	select {
	case err := <-done:
		assert.ErrorIs(t, err, ErrRecvBufferFull)
	case <-time.After(testLongTimeout):
		t.Fatal("append blocked on a full buffer")
	}
}

// 关闭后允许排空已缓冲数据，然后返回 EOF。
func TestRecvBufferCloseDrains(t *testing.T) {
	q := newRecvBuffer(DefaultSplitSize)
	assert.NoError(t, q.append(newTestBuf([]byte("abc"))))
	q.close()

	out := make([]byte, 8)
	n, err := q.read(out)
	assert.NoError(t, err)
	assert.Equal(t, 3, n)

	n, err = q.read(out)
	assert.Equal(t, 0, n)
	assert.Equal(t, io.EOF, err)

	// 关闭后新数据被拒绝
	assert.ErrorIs(t, q.append(newTestBuf([]byte("late"))), errRecvClosed)
}

func TestRecvBufferDiscardReleasesAll(t *testing.T) {
	q := newRecvBuffer(DefaultSplitSize)
	b1 := newTestBuf(make([]byte, 2*KB))
	b2 := newTestBuf(make([]byte, 2*KB))
	assert.NoError(t, q.append(b1))
	assert.NoError(t, q.append(b2))

	q.discard()
	assert.Equal(t, 0, q.pending())
	assert.Equal(t, packet.Header{}, b1.Head)
	assert.Equal(t, packet.Header{}, b2.Head)
}
