package stream

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/packet"
	"github.com/stretchr/testify/assert"
)

func TestConnReader(t *testing.T) {
	s1, s2 := Pipe()

	buf := []byte{1}
	s1.readTimeout = time.Millisecond * 100
	_, err := io.ReadFull(s1, buf)
	assert.Equal(t, ErrReadFromStreamTimeout, err)

	s2.bucketSz = 1
	s2.waitDataAckTimeout = time.Millisecond * 100
	_, err = s2.Write([]byte{1, 2, 3})
	assert.Equal(t, ErrWaitForDataAckTimeout, err)

	err = s2.Close()
	assert.Nil(t, err)

	_, err = s2.Read(buf)
	assert.Equal(t, io.EOF, err)

	_, err = s1.Write([]byte{1, 2, 3})
	assert.Equal(t, ErrWriterIsClosed, err)
}

func TestReadErr_SendDataAckFailed(t *testing.T) {
	c, _ := net.Pipe()
	pc := packet.NewConnWriter(c)
	s := New(pc, 0)
	timeout := time.Millisecond * 100

	pc.SetWriteTimeout(timeout)

	// 模拟有数据的状态
	s.currBuf = []byte("12345abcde12345abcde")
	n, err := s.Read(make([]byte, 10))
	assert.Equal(t, 10, n)
	assert.Nil(t, err)

	// ack是异步写回的，所以需要等一点时间，让异步代码超时并打印错误
	<-time.After(timeout + time.Millisecond*200)
}
