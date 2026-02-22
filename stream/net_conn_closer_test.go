package stream

import (
	"log"
	"net"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/packet"
	"github.com/stretchr/testify/assert"
)

func TestCloseErr(t *testing.T) {
	s1, s2 := Pipe()
	err := s1.Close()
	assert.Nil(t, err)

	err = s1.Close()
	assert.NotNil(t, err)

	err = s2.CloseRead()
	assert.Equal(t, ErrReaderIsClosed, err)

	err = s2.CloseWrite()
	assert.Equal(t, ErrWriterIsClosed, err)
}

func TestCloseErr_SendCloseFailed(t *testing.T) {
	c, _ := net.Pipe()
	pc := packet.NewWithConn(c)
	pc.SetWriteTimeout(time.Millisecond * 100)

	s := New(pc, 0)
	err := s.Close()
	assert.NotNil(t, err)
	log.Println(err)
}

func TestCloseErr_WaitAckTimeout(t *testing.T) {
	c1, c2 := net.Pipe()
	pc := packet.NewWithConn(c1)
	s := New(pc, 0)

	go func() {
		buf := make([]byte, 1000)
		c2.Read(buf)
	}()

	s.closeAckTimeout = time.Millisecond * 100
	err := s.Close()
	assert.Equal(t, ErrWaitCloseAckTimeout, err)
}
