package stream

import (
	"io"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/packet"
	"github.com/stretchr/testify/assert"
)

func TestHandleCmdData(t *testing.T) {
	t.Run("empty payload is dropped", func(t *testing.T) {
		s := New(&mockWriter{}, 0)
		emptyBuf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
		s.HandleCmdPushStreamData(emptyBuf)
		assert.Equal(t, 0, s.available())
	})

	t.Run("write to closed stream is ignored", func(t *testing.T) {
		s1, s2 := Pipe()
		err := s2.Close()
		assert.Nil(t, err)

		buf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
		_ = buf.SetPayload([]byte("late"))
		s1.HandleCmdPushStreamData(buf)
		assert.Equal(t, 0, s1.available())
	})
}

// 旧实现在接收队列满时阻塞 dispatcher goroutine 最多 2s 后静默关闭，
// 导致发送端无感知地继续写、数据永久丢失。新实现：append 永不阻塞，
// 超限只可能来自违反窗口的对端，并且必须主动向对端发送 Close。
func TestHandleCmdData_OverflowFailsLoud(t *testing.T) {
	fw := &recordingWriter{}
	s := New(fw, 1) // recv limit 被钳制到 DefaultSplitSize
	defer s.CloseRead()

	full := packet.NewBufferWithCmd(packet.CmdPushStreamData)
	assert.NoError(t, full.SetPayload(make([]byte, DefaultSplitSize)))
	s.HandleCmdPushStreamData(full)
	assert.Equal(t, DefaultSplitSize, s.available())

	// 超出上限：不应阻塞，也不应静默丢弃
	over := packet.NewBufferWithCmd(packet.CmdPushStreamData)
	assert.NoError(t, over.SetPayload([]byte("x")))

	done := make(chan struct{})
	go func() {
		defer close(done)
		s.HandleCmdPushStreamData(over)
	}()
	select {
	case <-done:
	case <-time.After(testLongTimeout):
		t.Fatal("HandleCmdPushStreamData blocked on full buffer")
	}

	// failClose 异步执行：等待 Close 包写出 & 双向关闭
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if isReadClosed(s) && isWriteClosed(s) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	assert.True(t, isReadClosed(s), "overflow must close read side")
	assert.True(t, isWriteClosed(s), "overflow must close write side")
	assert.Equal(t, "overflow", s.GetState().CloseReason)

	fw.mu.Lock()
	sentClose := false
	for _, h := range fw.bufs {
		if h[0] == packet.CmdCloseStream {
			sentClose = true
		}
	}
	fw.mu.Unlock()
	assert.True(t, sentClose, "peer must be notified with CmdCloseStream")
}

func TestHandleAckData(t *testing.T) {
	pbuf := packet.NewBufferWithCmd(packet.AckPushStreamData)

	t.Run("normal notify", func(t *testing.T) {
		s := New(nil, 0)
		s.HandleAckPushStreamData(pbuf)
	})

	t.Run("full event channel uses default branch", func(t *testing.T) {
		s := New(nil, 0)
		// WindowGuard with zero-cap event channel to test non-blocking Release
		s.window = NewWindowGuard(0, 0)
		s.HandleAckPushStreamData(pbuf)
	})
}

func TestHandleCmdCloseStream(t *testing.T) {
	s := New(&mockWriter{}, 0)
	pbuf := packet.NewBufferWithCmd(packet.CmdCloseStream)

	s.HandleCmdCloseStream(pbuf)

	assert.True(t, isReadClosed(s), "rclosed should be true")
	assert.True(t, isWriteClosed(s), "wclosed should be true")
}

// 对端"先发数据、后发 Close"时，缓冲中的数据必须仍可读（不能被 Close 丢弃）。
func TestHandleCmdCloseStream_KeepsBufferedData(t *testing.T) {
	s := New(&mockWriter{}, 0)

	data := packet.NewBufferWithCmd(packet.CmdPushStreamData)
	assert.NoError(t, data.SetPayload([]byte("tail-data")))
	s.HandleCmdPushStreamData(data)

	s.HandleCmdCloseStream(packet.NewBufferWithCmd(packet.CmdCloseStream))

	buf := make([]byte, 32)
	n, err := s.Read(buf)
	assert.NoError(t, err)
	assert.Equal(t, "tail-data", string(buf[:n]))

	n, err = s.Read(buf)
	assert.Equal(t, 0, n)
	assert.Equal(t, io.EOF, err)
}

func TestHandleAckCloseStream_DuplicateAck(t *testing.T) {
	s := New(&mockWriter{}, 0)
	pbuf := packet.NewBufferWithCmd(packet.AckCloseStream)

	// First ack fills the channel
	s.HandleAckCloseStream(pbuf)
	// Second ack should not block (default branch)
	s.HandleAckCloseStream(pbuf)
}

func TestConcurrentHandleCmdPushStreamData(t *testing.T) {
	s := New(&mockWriter{}, 0)
	var wg sync.WaitGroup
	const goroutines = 20

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pbuf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
			_ = pbuf.SetPayload([]byte("concurrent"))
			s.HandleCmdPushStreamData(pbuf)
		}()
	}
	wg.Wait()

	assert.Equal(t, goroutines*len("concurrent"), s.available())
}

// 新实现中 append 不阻塞，因此 CloseRead 不会被在途数据包拖延。
func TestHandleCmdPushStreamData_CloseReadNotBlocked(t *testing.T) {
	s := New(&mockWriter{}, 0)

	// 持续投递，直到缓冲区接近上限
	fill := packet.NewBufferWithCmd(packet.CmdPushStreamData)
	assert.NoError(t, fill.SetPayload(make([]byte, DefaultSplitSize)))
	for i := 0; i < 8; i++ {
		s.HandleCmdPushStreamData(fill)
		fill = packet.NewBufferWithCmd(packet.CmdPushStreamData)
		assert.NoError(t, fill.SetPayload(make([]byte, DefaultSplitSize)))
	}

	closeDone := make(chan error, 1)
	go func() { closeDone <- s.CloseRead() }()

	select {
	case err := <-closeDone:
		assert.NoError(t, err)
	case <-time.After(testLongTimeout):
		t.Fatal("CloseRead blocked by in-flight appends")
	}
}

// --- 缺口补充：State 计数器精确性 ---

func TestHandlerStateCounters(t *testing.T) {
	s := New(&mockWriter{}, 0)

	// 发送 3 个数据包
	for i := 0; i < 3; i++ {
		pbuf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
		_ = pbuf.SetPayload([]byte("12345")) // 5 bytes each
		s.HandleCmdPushStreamData(pbuf)
	}

	assert.Equal(t, int32(3), s.state.RecvBufferCount)
	assert.Equal(t, int64(15), s.state.RecvDataSize)

	// 发送 2 个 ACK
	for i := 0; i < 2; i++ {
		ackBuf := packet.NewBufferWithCmd(packet.AckPushStreamData)
		ackBuf.SetDataACKSize(100)
		s.HandleAckPushStreamData(ackBuf)
	}

	assert.Equal(t, int32(5), s.state.RecvBufferCount) // 3 + 2
	assert.Equal(t, int64(200), s.state.RecvAckTotal)
}

// --- B4: HandleCmdCloseStream 中 SendCmdCloseAck 失败 ---

func TestHandleCmdCloseStream_SendCloseAckFails(t *testing.T) {
	fw := &failOnCmdWriter{failCmd: packet.AckCloseStream}
	s := New(fw, 0)

	pbuf := packet.NewBufferWithCmd(packet.CmdCloseStream)
	s.HandleCmdCloseStream(pbuf)

	// CloseRead 和 CloseWrite 在 SendCmdCloseAck 之前执行，应该仍然生效
	assert.True(t, isReadClosed(s), "rclosed should be true even if SendCmdCloseAck fails")
	assert.True(t, isWriteClosed(s), "wclosed should be true even if SendCmdCloseAck fails")
}

// --- C1: 接收数据不超过 MaxPayloadSize 防御性验证 ---

func TestHandleCmdPushStreamData_PayloadBoundedByMaxPayloadSize(t *testing.T) {
	s := New(&mockWriter{}, 0)

	// 发送 MaxPayloadSize 大小的 payload
	maxPayload := make([]byte, packet.MaxPayloadSize)
	pbuf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
	err := pbuf.SetPayload(maxPayload)
	assert.Nil(t, err)
	s.HandleCmdPushStreamData(pbuf)

	assert.Equal(t, packet.MaxPayloadSize, s.available())

	buf := make([]byte, packet.MaxPayloadSize)
	n, err := s.Read(buf)
	assert.Nil(t, err)
	assert.Equal(t, packet.MaxPayloadSize, n)

	// 超过 MaxPayloadSize 的 payload 应该被 SetPayload 拒绝
	overPayload := make([]byte, packet.MaxPayloadSize+1)
	pbuf2 := packet.NewBufferWithCmd(packet.CmdPushStreamData)
	err = pbuf2.SetPayload(overPayload)
	assert.NotNil(t, err, "SetPayload should reject payload > MaxPayloadSize")
}
