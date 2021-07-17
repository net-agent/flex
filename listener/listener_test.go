package flex

import (
	"testing"
	"time"
)

func TestListenerClose(t *testing.T) {
	l := NewListener()

	err := l.pushStream(nil)
	if err == nil {
		t.Error("unexpected error")
		return
	}

	go func() {
		<-time.After(time.Millisecond * 100)
		err := l.Close()
		if err != nil {
			t.Error(err)
			return
		}
		err = l.Close()
		if err == nil {
			t.Error("unexpected nil error")
			return
		}
	}()

	_, err = l.Accept()
	if err == nil {
		t.Error("unexpected nil error")
		return
	}

	err = l.pushStream(NewStream(nil, true))
	if err == nil {
		t.Error("unexpected nil error")
		return
	}
}
