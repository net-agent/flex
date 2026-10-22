package node_test

import (
	"fmt"
	"io"

	"github.com/net-agent/flex/v3/node"
	"github.com/net-agent/flex/v3/packet"
)

// 两个 Node 直连：一端 Listen，另一端 DialIP 后写入数据。
func ExampleNode_DialIP() {
	c1, c2 := packet.Pipe()
	server := node.New(c2)
	server.SetDomain("server")
	server.SetIP(1)
	go server.Serve()
	defer server.Close()

	client := node.New(c1)
	client.SetDomain("client")
	client.SetIP(2)
	go client.Serve()
	defer client.Close()

	ln, err := server.Listen(80)
	if err != nil {
		fmt.Println("listen:", err)
		return
	}

	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 5)
		n, _ := io.ReadFull(conn, buf)
		got <- string(buf[:n])
		conn.Close()
	}()

	s, err := client.DialIP(1, 80)
	if err != nil {
		fmt.Println("dial:", err)
		return
	}
	_, _ = s.Write([]byte("hello"))

	fmt.Println("server got:", <-got)
	s.Close()

	// Output:
	// server got: hello
}
