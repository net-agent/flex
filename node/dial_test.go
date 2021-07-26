package node

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/net-agent/flex/packet"
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
	go node1.Run()
	go node2.Run()

	l, err := node1.Listen(80)
	if err != nil {
		t.Error(err)
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

	conn, err := node2.DialIP(0, 80)
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
	var wg sync.WaitGroup
	var wg2 sync.WaitGroup
	pc1, pc2 := packet.Pipe()

	node1 := New(pc1)
	node2 := New(pc2)

	wg2.Add(1)
	go func() {
		defer wg2.Done()
		node1.Run()
		fmt.Printf("node1 closed\n")
	}()

	wg2.Add(1)
	go func() {
		defer wg2.Done()
		node2.Run()
		fmt.Printf("node2 closed\n")
	}()

	l, err := node1.Listen(80)
	if err != nil {
		t.Error(err)
		return
	}

	wg.Add(1)
	go func(listener net.Listener) {
		defer wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				fmt.Printf("listener stopped, err=%v\n", err)
				return
			}

			wg.Add(1)
			go func() {
				defer wg.Done()
				n, err := io.Copy(conn, conn)
				fmt.Printf("copy stopped, n=%v err=%v\n", n, err)
			}()
		}
	}(l)

	payloads := [][]byte{}
	for _, sz := range []int{
		0,
		1,
		10,
		1024,
		1024 * 1024,
		1024 * 1024 * 64,
		1024 * 1024 * 64,
		1024 * 1024 * 64,
		1024 * 1024 * 64,
	} {
		buf := make([]byte, sz)
		rand.Read(buf)
		payloads = append(payloads, buf)
	}

	var doneCount int32 = 0
	dotest := func(index int, payload []byte) {
		defer func() {
			fmt.Printf("[%v] sz=%v test case completed.\n", index, len(payload))
			if atomic.AddInt32(&doneCount, 1) == int32(len(payloads)) {
				fmt.Printf("stop listening\n")
				l.Close()
			}
		}()
		defer wg.Done()

		conn, err := node2.DialIP(0, 80)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()

		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := conn.Write(payload)
			if err != nil {
				t.Error(err)
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
			t.Error("payload not equal")
			return
		}
	}

	for i, payload := range payloads {
		wg.Add(1)
		go dotest(i, payload)
	}

	wg.Wait() // 等待所有stream传输完毕

	node1.Close()
	node2.Close()
	wg2.Wait() // 等待所有node关闭
}
