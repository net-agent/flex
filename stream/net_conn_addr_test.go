package stream

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAddr(t *testing.T) {
	s := New(nil, false)
	s.SetLocal(1234, 5678)
	s.SetRemote(9876, 5432)
	s.SetNetwork("helloworld")

	assert.Equal(t, uint16(1234), s.local.IP())
	assert.Equal(t, uint16(9876), s.remote.IP())
	assert.Equal(t, "1234:5678", s.LocalAddr().String())
	assert.Equal(t, "9876:5432", s.RemoteAddr().String())
	assert.Equal(t, "helloworld", s.LocalAddr().Network())
}

func TestUsedPort(t *testing.T) {
	s1 := New(nil, false)
	s1.SetLocal(0, 1234)
	port, err := s1.GetUsedPort()
	if err == nil {
		t.Error("unexepected err")
		return
	}
	if port != 1234 {
		t.Error("lcoal port not equal")
		return
	}

	s2 := New(nil, true)
	s2.SetLocal(0, 3456)
	port, err = s2.GetUsedPort()
	if err != nil {
		t.Error(err)
		return
	}
	if port != 3456 {
		t.Error("lcoal port not equal")
		return
	}
}
