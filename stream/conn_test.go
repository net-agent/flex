package stream

import (
	"strings"
	"testing"

	"github.com/net-agent/flex/v2/packet"
)

func TestConnStrings(t *testing.T) {
	c := New(true)

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

func TestWaitOpenResp(t *testing.T) {
	c := New(true)

	go func() {
		pbuf1 := packet.NewBuffer(nil)
		pbuf1.SetPayload([]byte("test1"))
		c.Opened(pbuf1)

		pbuf2 := packet.NewBuffer(nil)
		pbuf2.SetPayload(nil)
		c.Opened(pbuf2)
	}()

	_, err := c.WaitOpenResp()
	if err == nil {
		t.Error("unexpected nil err")
		return
	}
	if err.Error() != "test1" {
		t.Error("unexpected err")
		return
	}
	_, err = c.WaitOpenResp()
	if err != nil {
		t.Error(err)
		return
	}
	_, err = c.WaitOpenResp()
	if err == nil {
		t.Error("unexpected nil err")
		return
	}

	if !strings.Contains(err.Error(), "timeout") {
		t.Error("unexpected err")
		return
	}
}
