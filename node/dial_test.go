package node

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v2/packet"
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
	port := uint16(80)
	payloadSize := 1024 * 1024 * 64
	node1, node2 := Pipe("test1", "test2")

	// 第一步：监听端口
	l, err := node1.Listen(port)
	if err != nil {
		t.Error(err)
		return
	}

	// 第二步：创建简单echo服务
	go func() {
		for {
			c, _ := l.Accept()
			go io.Copy(c, c)
		}
	}()

	// 第三步：从客户端创建连接
	conn, err := node2.DialIP(node1.GetIP(), port)
	if err != nil {
		t.Error(err)
		return
	}
	defer conn.Close()

	// 第四步：准备客户端数据
	payload := make([]byte, payloadSize)
	rand.Read(payload)

	// 第五步：将数据发送至服务端
	var writerGroup sync.WaitGroup
	writerGroup.Add(1)
	go func() {
		defer writerGroup.Done()
		_, err = conn.Write(payload)
		if err != nil {
			t.Error(err)
			return
		}
	}()

	// 第六步，读取服务端返回的数据，并校验正确性
	resp := make([]byte, payloadSize)
	_, err = io.ReadFull(conn, resp)
	if err != nil {
		t.Error(err)
		return
	}
	if !bytes.Equal(payload, resp) {
		t.Error("not equal")
		return
	}

	writerGroup.Wait()
}

// func TestDialConcurrency(t *testing.T) {
// 	pc1, pc2 := packet.Pipe()

// 	node1 := New(pc1)
// 	node2 := New(pc2)

// 	ExampleOf2NodeTest(t, node1, node2, 0)
// }

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
