package stream

import (
	"strings"
	"testing"
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
