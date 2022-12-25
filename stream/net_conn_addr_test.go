package stream

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAddr(t *testing.T) {
	s := New(nil)
	s.SetLocal(1234, 5678)
	s.SetRemote(9876, 5432)
	s.SetNetwork("helloworld")

	assert.Equal(t, uint16(1234), s.state.LocalAddr.IP)
	assert.Equal(t, uint16(9876), s.state.RemoteAddr.IP)
	assert.Equal(t, "1234:5678", s.LocalAddr().String())
	assert.Equal(t, "9876:5432", s.RemoteAddr().String())
	assert.Equal(t, "helloworld", s.LocalAddr().Network())
}

func TestUsedPort(t *testing.T) {
	s1 := New(nil)
	s1.SetUsedPort(1234)
	port := s1.GetUsedPort()
	assert.Equal(t, uint16(1234), port)
}
