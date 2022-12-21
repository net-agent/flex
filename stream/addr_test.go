package stream

import "testing"

func TestAddr(t *testing.T) {
	s := New(nil, false)

	s.SetLocal(1234, 5678)
	s.SetRemote(9876, 5432)

	if s.LocalAddr().String() != "1234:5678" {
		t.Error("LocalAddr not equal")
		return
	}

	if s.RemoteAddr().String() != "9876:5432" {
		t.Error("RemoteAddr not equal")
	}
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
