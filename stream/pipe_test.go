package stream

import (
	"bytes"
	"crypto/rand"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v4/packet"
	"github.com/stretchr/testify/assert"
)

func TestPipe(t *testing.T) {
	payload := make([]byte, 1024*1024*10)
	rand.Read(payload)

	s1, s2 := Pipe()

	t.Log("s1:", s1.String(), s1.GetState().String())
	t.Log("s2:", s2.String(), s2.GetState().String())

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()

		_, err := s1.Write(payload)
		assert.Nil(t, err)

		err = s1.Close()
		assert.Nil(t, err)

		<-time.After(testMedTimeout)
	}()

	buf := make([]byte, len(payload))
	_, err := io.ReadFull(s2, buf)
	assert.Nil(t, err)
	assert.True(t, bytes.Equal(payload, buf))

	wg.Wait()
}

func TestReadAndRoutePbufFailed(t *testing.T) {
	c, _ := packet.Pipe()
	c.SetReadTimeout(testMedTimeout)

	// ReadBuffer 超时返回错误，channel 应被正常关闭
	cmdCh := make(chan *packet.Buffer, 1)
	ackCh := make(chan *packet.Buffer, 1)
	demuxPackets(cmdCh, ackCh, c)
	_, ok := <-cmdCh
	assert.False(t, ok, "cmdCh should be closed after read error")
	_, ok = <-ackCh
	assert.False(t, ok, "ackCh should be closed after read error")

	// nil channel 应安全返回，不 panic
	demuxPackets(nil, nil, c)
}

func TestRouteErr(t *testing.T) {
	cmdCh := make(chan *packet.Buffer, 10)
	ackCh := make(chan *packet.Buffer, 10)

	// 两个路由 goroutine 各自接管一个 buffer 的所有权，不能共享同一对象
	cmdCh <- packet.NewBufferWithCmd(0)
	ackCh <- packet.NewBufferWithCmd(0)
	go routeCmd(nil, cmdCh)
	go routeAck(nil, ackCh)

	close(cmdCh)
	close(ackCh)
}

// --- B6: Pipe goroutine 清理验证 ---

func TestPipeCloseStateCleanup(t *testing.T) {
	s1, s2 := Pipe()

	// 正常关闭 s1
	err := s1.Close()
	assert.Nil(t, err)

	// 等待 s2 被动关闭传播
	time.Sleep(testMedTimeout)

	// 验证两端状态都正确关闭
	assert.True(t, isReadClosed(s1), "s1 rclosed after close")
	assert.True(t, isWriteClosed(s1), "s1 wclosed after close")
	assert.True(t, isReadClosed(s2), "s2 rclosed after passive close")
	assert.True(t, isWriteClosed(s2), "s2 wclosed after passive close")

	// s2 读写应该返回正确的错误
	_, err = s2.Write([]byte("data"))
	assert.Equal(t, ErrWriterIsClosed, err)

	_, err = s2.Read(make([]byte, 10))
	assert.Equal(t, io.EOF, err)

	// Pipe 在两端都 detach 后会关闭底层 packet.Pipe，demuxPackets 的
	// ReadBuffer 返回错误、route goroutine 随 channel 关闭退出。
}
