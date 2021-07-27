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
)

func HelpTest2Node(t *testing.T, node1, node2 *Node, concurrent int) {
	var wg sync.WaitGroup
	var wg2 sync.WaitGroup
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
	for i, sz := range []int{
		10,
		1024 * 1024 * 64,
		1024 * 1024 * 64,
		1024 * 1024 * 64,
		1024 * 1024 * 64,
		0,
		1,
		10,
		1024,
		1024 * 1024,
	} {
		if concurrent == 0 || i < concurrent {
			buf := make([]byte, sz)
			if i == 0 {
				copy(buf, []byte("helloworld"))
			} else {
				rand.Read(buf)
			}
			payloads = append(payloads, buf)
		}
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

		conn, err := node2.DialIP(node1.GetIP(), 80)
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
			fmt.Printf("[%v] write success, sz=%v\n", index, len(payload))
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
