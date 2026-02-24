// ping 演示如何使用 PingDomain 测试节点间的连通性和延迟
package main

import (
	"fmt"
	"log"
	"net"
	"time"

	"github.com/net-agent/flex/v3/internal/admit"
	"github.com/net-agent/flex/v3/node"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/switcher"
)

func main() {
	password := "demo"

	// 启动 switcher
	srv := switcher.NewServer(password, nil, nil)
	defer srv.Close()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go srv.Serve(ln)

	// 连接两个 node
	n1 := connectNode(ln.Addr().String(), "node-1", password)
	n2 := connectNode(ln.Addr().String(), "node-2", password)
	defer n1.Close()
	defer n2.Close()

	// ping switcher（空域名 = ping 中转节点）
	rtt, err := n1.PingDomain("", time.Second)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("ping switcher: %v\n", rtt)

	// ping 远端节点
	for i := range 5 {
		rtt, err := n1.PingDomain("node-2", time.Second)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("ping node-2 [%d]: %v\n", i+1, rtt)
	}
}

func connectNode(addr, domain, password string) *node.Node {
	conn, _ := net.Dial("tcp", addr)
	pc := packet.NewWithConn(conn)
	ip, obfKey, err := admit.Handshake(pc, domain, "", password)
	if err != nil {
		log.Fatal(err)
	}
	n := node.New(packet.NewObfuscatedConn(pc, obfKey))
	n.SetDomain(domain)
	n.SetIP(ip)
	go n.Serve()
	time.Sleep(50 * time.Millisecond)
	return n
}
