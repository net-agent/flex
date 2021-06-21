package flex

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func TestUpgraderAndSwitcher(t *testing.T) {
	addr := "localhost:50800"
	password := "hahaha"

	go func() {
		sw := NewSwitcher(nil, password)
		sw.Run(addr)
	}()

	<-time.After(time.Millisecond * 100)
	conn, err := net.Dial("tcp4", addr)
	if err != nil {
		t.Error(err)
		return
	}

	h1, err := UpgradeToHost(conn, password, &HostRequest{Domain: "test", Mac: ""})
	if err != nil {
		t.Error(err)
		return
	}
	l, err := h1.Listen(80)
	if err != nil {
		t.Error(err)
		return
	}
	go func(l net.Listener) {
		for {
			s, err := l.Accept()
			if err != nil {
				t.Error(err)
				return
			}

			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c)
			}(s)
		}
	}(l)

	conn, err = net.Dial("tcp4", addr)
	if err != nil {
		t.Error(err)
		return
	}

	h2, err := UpgradeToHost(conn, password, &HostRequest{Domain: "test2", Mac: ""})
	if err != nil {
		t.Error(err)
		return
	}

	s, err := h2.Dial("test:80")
	if err != nil {
		t.Error(err)
		return
	}

	payload := []byte("hello world")
	go s.Write(payload)

	buf := make([]byte, len(payload))
	n, err := io.ReadFull(s, buf)
	if err != nil && err != io.EOF {
		t.Error(err)
		return
	}
	if n != len(payload) {
		t.Error("not equal")
		return
	}

	if !bytes.Equal(payload, buf) {
		t.Error("not equal")
		return
	}
}
