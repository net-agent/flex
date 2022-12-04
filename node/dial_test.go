package node

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/vars"
)

func TestChanNil(t *testing.T) {
	ch := make(chan []byte, 10)
	ch <- nil
	ch <- []byte("hello world")
	close(ch)

	for b := range ch {
		fmt.Println(b)
	}
}

func TestDial(t *testing.T) {
	pc1, pc2 := packet.Pipe()

	node1 := New(pc1)
	node2 := New(pc2)
	node1.SetIP(1)
	node2.SetIP(2)
	go node1.Run()
	go node2.Run()

	l, err := node1.Listen(80)
	if err != nil {
		t.Error(err)
		return
	}

	// 重复监听端口
	_, err = node1.Listen(80)
	if err.Error() != "port busy now" {
		t.Error("unexpected err")
		return
	}

	payload := make([]byte, 1024*1024*64)
	rand.Read(payload)
	var wg sync.WaitGroup

	wg.Add(1)
	go func(listener net.Listener) {
		defer wg.Done()

		conn, err := listener.Accept()
		if err != nil {
			t.Error(err)
			return
		}
		defer listener.Close()
		defer conn.Close()

		// conn read and write
		buf := make([]byte, len(payload))

		_, err = io.ReadFull(conn, buf)
		if err != nil {
			t.Error()
			return
		}
		if !bytes.Equal(buf, payload) {
			t.Error("not equal")
			return
		}
	}(l)

	conn, err := node2.DialIP(node1.GetIP(), 80)
	if err != nil {
		t.Error(err)
		return
	}
	defer conn.Close()

	_, err = conn.Write(payload)
	if err != nil {
		t.Error(err)
		return
	}

	wg.Wait()

	<-time.After(time.Millisecond * 100)
	hasStream := false
	node1.streams.Range(func(key, val interface{}) bool {
		hasStream = true
		return false
	})
	if hasStream {
		t.Error("node1 stream leak")
		return
	}
	hasStream = false
	node2.streams.Range(func(key, val interface{}) bool {
		hasStream = true
		return false
	})
	if hasStream {
		t.Error("node2 stream leak")
		return
	}
}

func TestDialConcurrency(t *testing.T) {
	pc1, pc2 := packet.Pipe()

	node1 := New(pc1)
	node2 := New(pc2)

	ExampleOf2NodeTest(t, node1, node2, 0)
}

func TestNodeLocalLoop(t *testing.T) {
	if !EnableLocalLoop {
		return
	}
	pc, _ := packet.Pipe()
	node := New(pc)
	go node.Run()

	l, err := node.Listen(80)
	if err != nil {
		t.Error(err)
		return
	}

	payload := []byte("hello world")
	var wg sync.WaitGroup
	wg.Add(1)
	go func(l net.Listener) {
		defer wg.Done()
		defer l.Close()

		c, err := l.Accept()
		if err != nil {
			t.Error(err)
			return
		}
		buf := make([]byte, len(payload))
		_, err = io.ReadFull(c, buf)
		if err != nil {
			t.Error(err)
			return
		}
		if !bytes.Equal(buf, payload) {
			t.Error("not equal")
			return
		}
	}(l)

	// _, err = node.DialIP(localIP, 80)
	// if err != nil {
	// 	log.Printf("expected error: %v\n", err)
	// } else {
	// 	t.Error("unexpected nil err")
	// 	return
	// }

	c, err := node.DialIP(vars.LocalIP, 80)
	if err != nil {
		t.Error(err)
		return
	}

	_, err = c.Write(payload)
	if err != nil {
		t.Error(err)
		return
	}

	wg.Wait()
	c.Close()
}

func TestDialAddr(t *testing.T) {
	c1, c2 := packet.Pipe()
	node1 := New(c1)
	node2 := New(c2)

	node1.SetDomain("test1")
	node1.SetIP(1)
	node2.SetDomain("test2")
	node2.SetIP(2)
	go node1.Run()
	go node2.Run()

	_, err := node1.Dial("invalidhostport")
	if !strings.HasPrefix(err.Error(), "split address") {
		t.Error("unexpected err")
		return
	}

	_, err = node1.Dial("local:655s6") // invalid port number
	if !strings.HasPrefix(err.Error(), "parse port") {
		t.Error("unexpected err")
		return
	}

	// for coverage test: node.DialDomain
	_, err = node1.Dial("local:1234")
	if err == nil {
		t.Error("unexpected nil err")
		return
	}
	_, err = node1.Dial("test2:1234")
	if err == nil {
		t.Error("unexpected nil err")
		return
	}

	// for coverage test: node.DialIP
	_, err = node1.Dial("123:456")
	if err == nil {
		t.Error("unexpected nil err")
		return
	}
}

func TestPing(t *testing.T) {
	c1, c2 := packet.Pipe()
	node1 := New(c1)
	node2 := New(c2)

	node1.SetDomain("test1")
	node1.SetIP(1)
	node2.SetDomain("test2")
	node2.SetIP(2)
	go node1.Run()
	go node2.Run()

	_, err := node1.PingDomain("invaliddomain", time.Second)
	if err == nil {
		t.Error("unexpected nil error")
		return
	}

	d, err := node1.PingDomain("test2", time.Second)
	if err != nil {
		t.Error(err)
		return
	}

	fmt.Printf("ping domain duration=%v\n", d)
}
