package stream

import (
	"net"
	"testing"
	"time"

	"github.com/net-agent/flex/v2/packet"
	"github.com/stretchr/testify/assert"
)

func TestWriteFailed(t *testing.T) {
	c, _ := net.Pipe()
	pc := packet.NewConnWriter(c)
	s := New(pc, 0)
	timeout := time.Millisecond * 100

	pc.SetWriteTimeout(timeout)

	_, err := s.Write(nil)
	assert.Nil(t, err)

	_, err = s.write([]byte{1, 2, 3, 4, 5})
	assert.NotNil(t, err)
}
