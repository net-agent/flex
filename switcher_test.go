package flex

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
)

func testConnect(t *testing.T, switcher *Switcher, domain, mac string) (*Host, error) {
	c1, c2 := net.Pipe()

	go switcher.ServePacketConn(NewTcpPacketConn(c2))

	host, _, err := UpgradeToHost(NewTcpPacketConn(c1), &HostRequest{domain, mac, 0})
	if err != nil {
		t.Error(err)
		return nil, err
	}
	if host == nil {
		t.Error("not equal")
		return nil, err
	}

	return host, nil
}

func TestSwitcherBase(t *testing.T) {
	c1, c2 := net.Pipe()
	var wg sync.WaitGroup

	// server
	wg.Add(1)
	go func() {
		defer wg.Done()
		switcher := NewSwitcher(nil, "")
		ctx, err := switcher.UpgradeToContext(NewTcpPacketConn(c2))
		if err != nil {
			t.Error(err)
			return
		}
		if ctx.host == nil {
			t.Error("not equal")
			return
		}
	}()

	// client
	wg.Add(1)
	go func() {
		defer wg.Done()

		host, _, err := UpgradeToHost(NewTcpPacketConn(c1), &HostRequest{"test", "mac", 0})
		if err != nil {
			t.Error(err)
			return
		}
		if host == nil {
			t.Error("not equal")
			return
		}
	}()

	wg.Wait()
}

func TestSwitcherMult(t *testing.T) {
	switcher := NewSwitcher(nil, "")

	h1, err := testConnect(t, switcher, "test1", "mac1")
	if err != nil {
		t.Error(err)
		return
	}
	h2, err := testConnect(t, switcher, "test2", "mac2")
	if err != nil {
		t.Error(err)
		return
	}

	var wg sync.WaitGroup

	// echo server
	wg.Add(1)
	go func() {
		l, err := h1.Listen(8080)
		if err != nil {
			t.Error(err)
			return
		}
		wg.Done()
		for {
			conn, err := l.Accept()
			if err != nil {
				t.Error(err)
				return
			}
			go func(c net.Conn) {
				buf := make([]byte, 10)
				for {
					rn, err := conn.Read(buf)
					if err != nil {
						t.Error(err)
						return
					}
					wn, err := conn.Write(buf[:rn])
					if err != nil {
						t.Error(err)
						return
					}
					if wn != rn {
						t.Error("not equal")
						return
					}
				}
			}(conn)
		}
	}()

	wg.Wait()
	conn, err := h2.Dial(fmt.Sprintf("%v:8080", "test1"))
	if err != nil {
		t.Error(err)
		return
	}
	payload := []byte("hello world haha")
	go func() {
		wn, err := conn.Write(payload)
		if err != nil {
			t.Error(err)
			return
		}
		if wn != len(payload) {
			t.Error("not equal")
			return
		}
	}()

	buf := make([]byte, len(payload))
	_, err = io.ReadFull(conn, buf)
	if err != nil {
		t.Error(err)
		return
	}
	if !bytes.Equal(buf, payload) {
		t.Error("not equal")
		return
	}

}
