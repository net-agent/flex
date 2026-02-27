package stream

import (
	"bytes"
	"io"
	"math/rand"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestGetReadWriteSize(t *testing.T) {
	t.Run("net.Pipe echo", func(t *testing.T) {
		c1, c2 := net.Pipe()
		testPipeConn(t, c1, c2)
	})

	t.Run("stream.Pipe echo", func(t *testing.T) {
		s1, s2 := Pipe()
		testPipeConn(t, s1, s2)

		s1Read, s1Write := s1.GetReadWriteSize()
		s2Read, s2Write := s2.GetReadWriteSize()
		assert.Equal(t, s1Read, s1Write)
		assert.Equal(t, s2Read, s2Write)
		assert.Equal(t, s1Read, s2Read)
	})
}

func testPipeConn(t *testing.T, conn1, conn2 net.Conn) {
	t.Helper()

	const size = 1024 * 1024 * 32
	payload := make([]byte, size)
	rand.Read(payload)

	go io.Copy(conn2, conn2)
	go func() {
		wn, err := conn1.Write(payload)
		assert.Nil(t, err)
		assert.Equal(t, size, wn)
	}()

	readBuf := make([]byte, size)
	_, err := io.ReadFull(conn1, readBuf)
	assert.Nil(t, err)
	assert.True(t, bytes.Equal(readBuf, payload))
}

func TestSetRemoteDomain(t *testing.T) {
	s := New(nil, 0)
	domain := "testdomain"

	s.SetRemoteDomain(domain)
	assert.Equal(t, domain, s.state.RemoteDomain)
}

func TestDirectionStr(t *testing.T) {
	tests := []struct {
		name      string
		direction Direction
		want      string
	}{
		{"local to remote", DirectionOutbound, "local-remote"},
		{"remote to local", DirectionInbound, "remote-local"},
		{"zero is invalid", 0, "invalid"},
		{"out of range is invalid", 3, "invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, directionStr(tt.direction))
		})
	}
}

func TestGetState(t *testing.T) {
	s := New(nil, 0)

	t.Run("returns a copy", func(t *testing.T) {
		st1 := s.GetState()
		st2 := s.GetState()
		assert.EqualValues(t, st1, st2)

		st1.IsClosed = true
		assert.NotEqualValues(t, st1, st2, "mutating copy should not affect original")
	})
}

func TestConcurrentReadWriteClose(t *testing.T) {
	s1, s2 := Pipe()

	var wg sync.WaitGroup

	// Writer goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			s1.Write([]byte("ping"))
		}
	}()

	// Reader goroutine
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 64)
		for {
			_, err := s2.Read(buf)
			if err != nil {
				return
			}
		}
	}()

	// Close goroutine — close after a short delay
	wg.Add(1)
	go func() {
		defer wg.Done()
		time.Sleep(testShortTimeout)
		s1.Close()
	}()

	wg.Wait()
}

// --- 缺口补充：构造器边界 ---

func TestNewWithNegativeWindowSize(t *testing.T) {
	s := New(&mockWriter{}, -1)
	assert.Equal(t, DefaultWindowSize, s.window.Available(), "negative window should fallback to default")
	assert.GreaterOrEqual(t, cap(s.recvQueue), 16, "channel cap should have minimum 16")
}

func TestNewWithSmallWindowSize(t *testing.T) {
	s := New(&mockWriter{}, 1)
	// chanCap = 1 / DefaultSplitSize * 4 = 0, should be clamped to 16
	assert.Equal(t, int32(1), s.window.Available())
	assert.Equal(t, 16, cap(s.recvQueue), "small window should clamp chanCap to 16")
}

func TestNewDialStream(t *testing.T) {
	w := &mockWriter{}
	s := NewDialStream(w, "local.test", 10, 20, "remote.test", 30, 40, 0)

	assert.Equal(t, DirectionOutbound, s.state.Direction)
	assert.Equal(t, "local.test", s.state.LocalDomain)
	assert.Equal(t, "remote.test", s.state.RemoteDomain)
	assert.Equal(t, uint16(10), s.state.LocalAddr.IP)
	assert.Equal(t, uint16(20), s.state.LocalAddr.Port)
	assert.Equal(t, uint16(30), s.state.RemoteAddr.IP)
	assert.Equal(t, uint16(40), s.state.RemoteAddr.Port)
	assert.Equal(t, uint16(20), s.GetBoundPort())
}

func TestNewAcceptStream(t *testing.T) {
	w := &mockWriter{}
	s := NewAcceptStream(w, "local.test", 10, 20, "remote.test", 30, 40, 0)

	assert.Equal(t, DirectionInbound, s.state.Direction)
	assert.Equal(t, "local.test", s.state.LocalDomain)
	assert.Equal(t, "remote.test", s.state.RemoteDomain)
	assert.Equal(t, uint16(10), s.state.LocalAddr.IP)
	assert.Equal(t, uint16(20), s.state.LocalAddr.Port)
	assert.Equal(t, uint16(30), s.state.RemoteAddr.IP)
	assert.Equal(t, uint16(40), s.state.RemoteAddr.Port)
}

// --- 缺口补充：String() 方法 ---

func TestStreamString(t *testing.T) {
	s := NewDialStream(&mockWriter{}, "mylocal", 1, 2, "myremote", 3, 4, 0)
	str := s.String()
	assert.Contains(t, str, "local-remote")
	assert.Contains(t, str, "1:2")
	assert.Contains(t, str, "3:4")

	s2 := NewAcceptStream(&mockWriter{}, "mylocal", 5, 6, "myremote", 7, 8, 0)
	str2 := s2.String()
	assert.Contains(t, str2, "remote-local")
}

// --- 缺口补充：State 计数器绝对值精确性 ---

func TestStateCountersAbsoluteValues(t *testing.T) {
	s1, s2 := Pipe()

	const msgCount = 100
	const msgSize = 256
	payload := make([]byte, msgSize)
	rand.Read(payload)

	// 写入
	go func() {
		for i := 0; i < msgCount; i++ {
			s1.Write(payload)
		}
		s1.Close()
	}()

	// 读取
	buf := make([]byte, 1024)
	var totalRead int
	for {
		n, err := s2.Read(buf)
		totalRead += n
		if err != nil {
			break
		}
	}

	expectedTotal := int64(msgCount * msgSize)
	assert.Equal(t, expectedTotal, int64(totalRead), "total read bytes mismatch")

	// s1 的 BytesWritten 应该等于发送的总字节数
	assert.Equal(t, expectedTotal, s1.state.BytesWritten, "s1 BytesWritten mismatch")

	// s2 的 BytesRead 应该等于读取的总字节数
	assert.Equal(t, expectedTotal, s2.state.BytesRead, "s2 BytesRead mismatch")

	// s2 的 RecvDataSize 应该等于接收的总字节数
	assert.Equal(t, expectedTotal, s2.state.RecvDataSize, "s2 RecvDataSize mismatch")
}

// --- 缺口补充：并发 Read+Write+Close 数据完整性 ---

func TestConcurrentReadWriteCloseDataIntegrity(t *testing.T) {
	s1, s2 := Pipe()

	const totalBytes = 1024 * 64
	payload := make([]byte, totalBytes)
	rand.Read(payload)

	var writeErr error
	var readBuf bytes.Buffer

	var wg sync.WaitGroup

	// Writer
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, writeErr = s1.Write(payload)
		s1.Close()
	}()

	// Reader
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := s2.Read(buf)
			if n > 0 {
				readBuf.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	wg.Wait()

	assert.Nil(t, writeErr)
	assert.Equal(t, totalBytes, readBuf.Len(), "should receive all written data")
	assert.True(t, bytes.Equal(payload, readBuf.Bytes()), "data content should match")
}

// --- 缺口补充：SetLogger ---

func TestSetLogger(t *testing.T) {
	s := New(nil, 0)

	// nil logger should not change
	origLogger := s.logger
	s.SetLogger(nil)
	assert.Equal(t, origLogger, s.logger, "nil logger should be ignored")
}
