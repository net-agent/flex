package flex

import (
	"testing"
	"time"
)

func TestListenerClose(t *testing.T) {
	l := NewListener()

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

	_, err := l.Accept()
	if err == nil {
		t.Error("unexpected nil error")
		return
	}
}
