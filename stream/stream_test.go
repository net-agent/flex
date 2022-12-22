package stream

import (
	"bytes"
	"io"
	"math/rand"
	"net"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConnStrings(t *testing.T) {
	c := New(nil, true)

	c.SetLocal(123, 456)
	c.SetRemote(789, 543)

	if c.String() != "<123:456,789:543>" {
		t.Error("not equal")
		return
	}

	if c.State() == "" {
		t.Error("unexpected empty state")
		return
	}

	if c.Dialer() != "self" {
		t.Error("not equal")
		return
	}

	err := c.SetDialer("haah")
	if !strings.HasPrefix(err.Error(), "conn is dialer") {
		t.Error("unexpected err")
		return
	}

	c.isDialer = false
	c.SetDialer("hello")
	if c.Dialer() != "hello" {
		t.Error("not equal")
		return
	}
}

func TestGetReadWriteSize(t *testing.T) {
	c1, c2 := net.Pipe()
	testPipeConn(t, c1, c2)

	s1, s2 := Pipe()
	testPipeConn(t, s1, s2)

	s1Read, s1Write := s1.GetReadWriteSize()
	s2Read, s2Write := s2.GetReadWriteSize()
	assert.Equal(t, s1Read, s2Write)
	assert.Equal(t, s2Read, s1Write)
	assert.Equal(t, s1Read, s1Write)
}

func testPipeConn(t *testing.T, conn1, conn2 net.Conn) {
	size := 1024 * 1024 * 32
	payload := make([]byte, size)
	rand.Read(payload)

	go func() {
		io.Copy(conn2, conn2)
	}()
	go func() {
		wn, err := conn1.Write(payload)
		assert.Nil(t, err)
		assert.Equal(t, size, wn)
	}()

	readBuf := []byte{}
	readed := 0
	buf := make([]byte, 10240)
	for {
		rn, err := conn1.Read(buf)
		readed += rn
		if !assert.Nil(t, err) {
			return
		}
		readBuf = append(readBuf, buf[:rn]...)
		if readed >= size {
			break
		}
	}
	assert.Equal(t, size, readed)
	assert.True(t, bytes.Equal(readBuf, payload))
}
