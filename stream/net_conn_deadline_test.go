package stream

import (
	"strings"
	"testing"
	"time"
)

func TestDeadline(t *testing.T) {
	c := New(nil, true)
	past := time.Now()
	<-time.After(10 * time.Millisecond)

	err := c.SetWriteDeadline(past)
	if !strings.HasSuffix(err.Error(), "in future") {
		t.Error("unexpected err")
		return
	}

	// test deadline cancel
	c.SetDeadline(time.Now().Add(time.Millisecond * 150))
	<-time.After(time.Millisecond * 50)
	c.SetDeadline(time.Time{})
	<-time.After(time.Millisecond * 150)
	if c.rclosed || c.wclosed {
		t.Error("unexpected close")
		return
	}

	// test deadline close
	c.SetDeadline(time.Now().Add(time.Millisecond * 100))
	<-time.After(time.Millisecond * 200)
	if !c.rclosed || !c.wclosed {
		t.Error("conn should closed")
		return
	}

	err = c.SetReadDeadline(time.Now().Add(time.Second))
	if err == nil {
		t.Error("unexpected nil err")
		return
	}
	err = c.SetWriteDeadline(time.Now().Add(time.Second))
	if err == nil {
		t.Error("unexpected nil err")
		return
	}
}
