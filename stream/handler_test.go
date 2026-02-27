package stream

import (
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/packet"
	"github.com/stretchr/testify/assert"
)

func TestHandleCmdData(t *testing.T) {
	t.Run("empty payload is ignored", func(t *testing.T) {
		s1, _ := Pipe()
		emptyBuf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
		s1.HandleCmdPushStreamData(emptyBuf)
	})

	t.Run("write to closed stream is ignored", func(t *testing.T) {
		s1, s2 := Pipe()
		err := s2.Close()
		assert.Nil(t, err)

		s1.HandleCmdPushStreamData(packet.NewBufferWithCmd(packet.CmdPushStreamData))
	})
}

func TestHandleCmdData_AppendTimeout(t *testing.T) {
	s := New(nil, 0)
	pbuf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
	pbuf.SetPayload([]byte("hello"))

	s.recvQueue = make(chan []byte)
	s.recvPushTimeout = testMedTimeout
	s.HandleCmdPushStreamData(pbuf)
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
			pbuf.SetPayload([]byte("concurrent"))
			s.HandleCmdPushStreamData(pbuf)
		}()
	}
	wg.Wait()
}

// --- 缺口补充：HandleCmdPushStreamData + CloseRead 软死锁 ---

func TestHandleCmdPushStreamData_CloseReadWhileChannelFull(t *testing.T) {
	// 验证当 bytesChan 满时，CloseRead 不会被 HandleCmdPushStreamData 长时间阻塞
	s := New(&mockWriter{}, 0)
	s.recvPushTimeout = testMedTimeout
	// 填满 bytesChan
	for i := 0; i < cap(s.recvQueue); i++ {
		s.recvQueue <- []byte("fill")
	}

	// 启动一个 handler，它会阻塞在 bytesChan 发送上
	handlerDone := make(chan struct{})
	go func() {
		defer close(handlerDone)
		pbuf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
		pbuf.SetPayload([]byte("blocked"))
		s.HandleCmdPushStreamData(pbuf)
	}()

	// CloseRead 应该在 appendDataTimeout 内完成（handler 释放 RLock 后）
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- s.CloseRead()
	}()

	select {
	case err := <-closeDone:
		assert.Nil(t, err)
	case <-time.After(s.recvPushTimeout + testLongTimeout):
		t.Fatal("CloseRead blocked too long — soft deadlock detected")
	}

	// handler 也应该退出
	select {
	case <-handlerDone:
	case <-time.After(testLongTimeout):
		t.Fatal("HandleCmdPushStreamData did not exit after CloseRead")
	}
}

// --- 缺口补充：State 计数器精确性 ---

func TestHandlerStateCounters(t *testing.T) {
	s := New(&mockWriter{}, 0)

	// 发送 3 个数据包
	for i := 0; i < 3; i++ {
		pbuf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
		pbuf.SetPayload([]byte("12345")) // 5 bytes each
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

// --- C1: currBuf 不超过 MaxPayloadSize 防御性验证 ---

func TestHandleCmdPushStreamData_PayloadBoundedByMaxPayloadSize(t *testing.T) {
	s := New(&mockWriter{}, 0)

	// 发送 MaxPayloadSize 大小的 payload
	maxPayload := make([]byte, packet.MaxPayloadSize)
	pbuf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
	err := pbuf.SetPayload(maxPayload)
	assert.Nil(t, err)
	s.HandleCmdPushStreamData(pbuf)

	// 从 bytesChan 取出，验证长度
	select {
	case buf := <-s.recvQueue:
		assert.Equal(t, packet.MaxPayloadSize, len(buf),
			"payload in bytesChan should be exactly MaxPayloadSize")
	default:
		t.Fatal("bytesChan should have one entry")
	}

	// 超过 MaxPayloadSize 的 payload 应该被 SetPayload 拒绝
	overPayload := make([]byte, packet.MaxPayloadSize+1)
	pbuf2 := packet.NewBufferWithCmd(packet.CmdPushStreamData)
	err = pbuf2.SetPayload(overPayload)
	assert.NotNil(t, err, "SetPayload should reject payload > MaxPayloadSize")
}
