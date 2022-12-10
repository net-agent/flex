package node

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func TestNetPipe(t *testing.T) {
	c1, c2 := net.Pipe()
	go io.Copy(c2, c2)

	size := 1024 * 1024 * 64
	payload := make([]byte, size)
	go c1.Write(payload)

	resp := make([]byte, size)
	io.ReadFull(c1, resp)
	if !bytes.Equal(payload, resp) {
		t.Error("not equal")
		return
	}
}

func TestNodePipe(t *testing.T) {
	n1, n2 := Pipe("test1", "test2")
	var err error

	_, err = n1.PingDomain("test2", time.Second)
	if err != nil {
		t.Error(err)
		return
	}

	_, err = n2.PingDomain("test1", time.Second)
	if err != nil {
		t.Error(err)
		return
	}

	// 并发调用
	// var wg sync.WaitGroup
	// for i := 0; i < 100; i++ {
	// 	wg.Add(1)
	// 	go func() {
	// 		defer wg.Done()
	// 		n2.PingDomain("test1", time.Second)
	// 		if err != nil {
	// 			t.Error(err)
	// 			return
	// 		}
	// 	}()
	// }
	// wg.Wait()
}
