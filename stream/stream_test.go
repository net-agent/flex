package stream

import (
	"bytes"
	"io"
	"math/rand"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
)

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
		readBuf = append(readBuf, buf[:rn]...)
		if !assert.Nil(t, err) {
			return
		}
		if readed >= size {
			break
		}
	}
	assert.Equal(t, size, readed)
	assert.True(t, bytes.Equal(readBuf, payload))
}

func TestSetRemoteDomain(t *testing.T) {
	s := New(nil)
	domain := "testdomain"

	s.SetRemoteDomain(domain)
	assert.Equal(t, domain, s.state.RemoteDomain)
}

func Test_directionStr(t *testing.T) {
	type args struct {
		d int
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{"1", args{DIRECTION_LOCAL_TO_REMOTE}, "local-remote"},
		{"2", args{DIRECTION_REMOTE_TO_LOCAL}, "remote-local"},
		{"3", args{0}, "invalid"},
		{"4", args{3}, "invalid"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := directionStr(tt.args.d); got != tt.want {
				t.Errorf("directionStr() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestGetState(t *testing.T) {
	s := New(nil)

	st1 := s.GetState()
	st2 := s.GetState()
	assert.EqualValues(t, st1, st2)

	st1.IsClosed = true
	assert.NotEqualValues(t, st1, st2)
}
