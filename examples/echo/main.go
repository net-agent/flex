// echo 演示 flex 最基本的用法：
// 1. 启动 switcher 中转服务
// 2. 两个 node 通过 TCP 连接到 switcher 并完成握手
// 3. node-server 在端口 80 上监听，提供 echo 服务
// 4. node-client 通过域名 dial 到 node-server，发送数据并读取回显
package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"time"

	"github.com/net-agent/flex/v3/internal/admit"
	"github.com/net-agent/flex/v3/node"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/switcher"
)

func main() {
	// ---- 1. 启动 switcher ----
	password := "demo-password"
	srv := switcher.NewServer(password, nil, nil)
	defer srv.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	addr := ln.Addr().String()
	go srv.Serve(ln)
	log.Printf("switcher listening on %s", addr)

	// ---- 2. 连接两个 node ----
	nodeServer := connectNode(addr, "server-node", password)
	nodeClient := connectNode(addr, "client-node", password)
	defer nodeServer.Close()
	defer nodeClient.Close()

	// ---- 3. node-server 监听端口 80，运行 echo 服务 ----
	flexLn, err := nodeServer.Listen(80)
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		for {
			conn, err := flexLn.Accept()
			if err != nil {
				return
			}
			go io.Copy(conn, conn) // echo
		}
	}()

	// ---- 4. node-client 通过域名 dial，发送数据 ----
	stream, err := nodeClient.Dial("server-node:80")
	if err != nil {
		log.Fatal("dial failed:", err)
	}
	defer stream.Close()

	msg := "hello flex!"
	stream.Write([]byte(msg))

	buf := make([]byte, len(msg))
	io.ReadFull(stream, buf)
	fmt.Printf("sent: %s\nrecv: %s\n", msg, string(buf))
}

// connectNode 连接到 switcher 并返回就绪的 Node
func connectNode(addr, domain, password string) *node.Node {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	pc := packet.NewWithConn(conn)

	ip, obfKey, err := admit.Handshake(pc, domain, "", password)
	if err != nil {
		log.Fatal(err)
	}

	n := node.New(packet.NewObfuscatedConn(pc, obfKey))
	n.SetDomain(domain)
	n.SetIP(ip)
	go n.Serve()

	time.Sleep(50 * time.Millisecond) // 等待 serve 就绪
	return n
}
