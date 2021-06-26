package flex

import (
	"bytes"
	"io"
	"net"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/net-agent/cipherconn"
)

func h1Test(t *testing.T, h1 *Host) {
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
}
func h2Test(t *testing.T, h1, h2 *Host) {

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

func TestUpgraderAndSwitcher(t *testing.T) {
	addr := "localhost:50800"
	password := "hahaha"

	go func() {
		sw := NewSwitcher(nil)
		sw.Run(addr, password)
	}()

	<-time.After(time.Millisecond * 100)

	//
	// 创建连接
	conn, err := net.Dial("tcp4", addr)
	if err != nil {
		t.Error(err)
		return
	}
	cc, err := cipherconn.New(conn, password)
	if err != nil {
		t.Error(err)
		return
	}

	// 升级协议
	h1, _, err := UpgradeToHost(
		NewTcpPacketConn(cc),
		&HostRequest{Domain: "test", Mac: ""},
		true)
	if err != nil {
		t.Error(err)
		return
	}

	// host1 服务
	h1Test(t, h1)

	// 创建连接
	conn, err = net.Dial("tcp4", addr)
	if err != nil {
		t.Error(err)
		return
	}
	cc, err = cipherconn.New(conn, password)
	if err != nil {
		t.Error(err)
		return
	}

	// 升级协议
	h2, _, err := UpgradeToHost(
		NewTcpPacketConn(cc),
		&HostRequest{Domain: "test2", Mac: ""},
		true)
	if err != nil {
		t.Error(err)
		return
	}

	// host2 服务
	h2Test(t, h1, h2)
}

func TestUpgraderAndSwitcherForWebsocket(t *testing.T) {
	addr := "localhost:50801"
	path := "/wsconn"
	go func() {
		upgrader := websocket.Upgrader{}
		sw := NewSwitcher(nil)
		http.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			wsconn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				t.Error(err)
				return
			}
			go sw.ServePacketConn(NewWsPacketConn(wsconn))
		})

		http.ListenAndServe(addr, nil)
	}()

	<-time.After(time.Millisecond * 100)

	//
	// 创建连接
	u := url.URL{Scheme: "ws", Host: addr, Path: path}
	wsconn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Error(err)
		return
	}

	// 升级协议
	h1, _, err := UpgradeToHost(
		NewWsPacketConn(wsconn),
		&HostRequest{Domain: "test", Mac: ""},
		true)
	if err != nil {
		t.Error(err)
		return
	}

	// host1 服务
	h1Test(t, h1)

	// 创建连接
	wsconn, _, err = websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		t.Error(err)
		return
	}

	h2, _, err := UpgradeToHost(
		NewWsPacketConn(wsconn),
		&HostRequest{Domain: "test2", Mac: ""},
		true)
	if err != nil {
		t.Error(err)
		return
	}

	h2Test(t, h1, h2)
}
