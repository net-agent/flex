package stream

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/packet"
	"github.com/stretchr/testify/assert"
)

func TestCloseErr(t *testing.T) {
	t.Run("double close returns net.ErrClosed", func(t *testing.T) {
		s1, s2 := Pipe()
		err := s1.Close()
		assert.Nil(t, err)

		err = s1.Close()
		assert.ErrorIs(t, err, net.ErrClosed, "second Close should return net.ErrClosed")

		err = s2.CloseRead()
		assert.Equal(t, ErrReaderIsClosed, err)

		err = s2.CloseWrite()
		assert.Equal(t, ErrWriterIsClosed, err)
	})
}

func TestCloseErr_SendCloseFailed(t *testing.T) {
	c, _ := net.Pipe()
	pc := packet.NewWithConn(c)
	pc.SetWriteTimeout(testMedTimeout)

	s := New(pc, 0)
	err := s.Close()
	assert.NotNil(t, err)
	t.Log("expected error:", err)
}

func TestCloseErr_WaitAckTimeout(t *testing.T) {
	c1, c2 := net.Pipe()
	pc := packet.NewWithConn(c1)
	s := New(pc, 0)

	// drain the close command so write doesn't block
	go func() {
		buf := make([]byte, 1000)
		c2.Read(buf)
	}()

	s.closeAckTimeout = testMedTimeout
	err := s.Close()
	assert.Equal(t, ErrWaitCloseAckTimeout, err)
}

func TestPassiveClose(t *testing.T) {
	s := New(&mockWriter{}, 0)

	pbuf := packet.NewBufferWithCmd(packet.CmdCloseStream)
	s.HandleCmdCloseStream(pbuf)

	assert.True(t, isReadClosed(s), "rclosed should be true after passive close")
	assert.True(t, isWriteClosed(s), "wclosed should be true after passive close")
}

// TestCloseInterruptsBlockedWrite verifies that Close() immediately wakes up
// a Write blocked on window via the closeCh channel (P1 fix).
func TestCloseInterruptsBlockedWrite(t *testing.T) {
	s := New(&mockWriter{}, 1024)
	s.window.SetSize(0)
	s.closeAckTimeout = testShortTimeout

	done := make(chan error, 1)
	go func() {
		_, err := s.Write([]byte("blocked"))
		done <- err
	}()

	time.Sleep(testShortTimeout)
	s.Close()

	select {
	case err := <-done:
		assert.Equal(t, ErrWriterIsClosed, err, "Write should return ErrWriterIsClosed after Close")
	case <-time.After(testLongTimeout):
		t.Fatal("Write did not unblock after Close")
	}
}

func TestGracefulClose(t *testing.T) {
	s1, s2 := Pipe()

	// Close s1 — s2's handler will send CloseAck back
	err := s1.Close()
	assert.Nil(t, err, "graceful close should succeed")

	// s2 should also be fully closed by HandleCmdCloseStream
	time.Sleep(testShortTimeout)
	assert.True(t, isReadClosed(s2))
	assert.True(t, isWriteClosed(s2))
}

// --- 缺口补充：并发 Close 确定性 ---

func TestConcurrentCloseExactlyOneSucceeds(t *testing.T) {
	s := New(&mockWriter{}, 0)
	s.closeAckTimeout = testShortTimeout

	const goroutines = 5
	results := make(chan error, goroutines)

	// 预先发送一个 closeAck 让至少一个 Close 能成功
	go func() {
		time.Sleep(10 * time.Millisecond)
		s.HandleAckCloseStream(packet.NewBuffer())
	}()

	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- s.Close()
		}()
	}
	wg.Wait()
	close(results)

	var nilCount, errCount int
	for err := range results {
		if err == nil {
			nilCount++
		} else {
			errCount++
		}
	}

	// CloseWrite 有互斥保护，只有第一个能成功走完整流程
	// 其他的会在 CloseWrite 处返回 ErrWriterIsClosed
	assert.Equal(t, 1, nilCount, "exactly one Close should succeed")
	assert.Equal(t, goroutines-1, errCount, "others should return error")
}

// --- 缺口补充：Close 后 Read 和 Write 的错误类型 ---

func TestCloseErrorTypes(t *testing.T) {
	s1, s2 := Pipe()

	err := s1.Close()
	assert.Nil(t, err)

	time.Sleep(testShortTimeout)

	// s1 写端已关闭
	_, err = s1.Write([]byte("data"))
	assert.Equal(t, ErrWriterIsClosed, err)

	// s1 读端已关闭（Close 的 defer CloseRead）
	_, err = s1.Read(make([]byte, 10))
	assert.Equal(t, io.EOF, err)

	// s2 被动关闭后
	_, err = s2.Write([]byte("data"))
	assert.Equal(t, ErrWriterIsClosed, err)

	_, err = s2.Read(make([]byte, 10))
	assert.Equal(t, io.EOF, err)
}

// --- B2: 双向同时 Close ---

func TestBidirectionalSimultaneousClose(t *testing.T) {
	s1, s2 := Pipe()

	var wg sync.WaitGroup
	results := make(chan error, 2)

	wg.Add(1)
	go func() {
		defer wg.Done()
		results <- s1.Close()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		results <- s2.Close()
	}()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("bidirectional close deadlocked")
	}

	close(results)

	// 两端都应该完全关闭
	assert.True(t, isReadClosed(s1), "s1 rclosed")
	assert.True(t, isWriteClosed(s1), "s1 wclosed")
	assert.True(t, isReadClosed(s2), "s2 rclosed")
	assert.True(t, isWriteClosed(s2), "s2 wclosed")

	// 至少一端应该成功收到 CloseAck（返回 nil）
	var nilCount int
	for err := range results {
		if err == nil {
			nilCount++
		}
	}
	assert.GreaterOrEqual(t, nilCount, 1, "at least one side should close gracefully")
}
