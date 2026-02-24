// tcp-proxy 演示如何用 flex 做 TCP 端口转发：
// - node-a 将本地 TCP 服务暴露到 flex 网络的端口 80
// - node-b 在本地监听一个 TCP 端口，将流量转发到 node-a:80
// 效果：访问 node-b 的本地端口 == 访问 node-a 的本地服务
package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/net-agent/flex/v3/internal/admit"
	"github.com/net-agent/flex/v3/node"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/switcher"
)

func main() {
	password := "demo"

	// ---- 1. 启动 switcher ----
	srv := switcher.NewServer(password, nil, nil)
	defer srv.Close()
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	go srv.Serve(ln)
	switcherAddr := ln.Addr().String()

	// ---- 2. 在本地启动一个 HTTP 服务（模拟内网服务） ----
	httpLn, _ := net.Listen("tcp", "127.0.0.1:0")
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "hello from internal service")
	})
	go http.Serve(httpLn, nil)
	log.Printf("internal HTTP service on %s", httpLn.Addr())

	// ---- 3. node-a：将本地 HTTP 服务暴露到 flex 端口 80 ----
	nodeA := connectNode(switcherAddr, "node-a", password)
	defer nodeA.Close()

	flexLn, _ := nodeA.Listen(80)
	go func() {
		for {
			flexConn, err := flexLn.Accept()
			if err != nil {
				return
			}
			go func() {
				local, err := net.Dial("tcp", httpLn.Addr().String())
				if err != nil {
					flexConn.Close()
					return
				}
				go io.Copy(local, flexConn)
				io.Copy(flexConn, local)
				local.Close()
				flexConn.Close()
			}()
		}
	}()

	// ---- 4. node-b：在本地开 TCP 端口，转发到 node-a:80 ----
	nodeB := connectNode(switcherAddr, "node-b", password)
	defer nodeB.Close()

	proxyLn, _ := net.Listen("tcp", "127.0.0.1:0")
	log.Printf("proxy listening on %s -> node-a:80", proxyLn.Addr())
	go func() {
		for {
			conn, err := proxyLn.Accept()
			if err != nil {
				return
			}
			go func() {
				flexConn, err := nodeB.Dial("node-a:80")
				if err != nil {
					conn.Close()
					return
				}
				go io.Copy(flexConn, conn)
				io.Copy(conn, flexConn)
				conn.Close()
				flexConn.Close()
			}()
		}
	}()

	// ---- 5. 通过 proxy 端口访问内网服务 ----
	resp, err := http.Get(fmt.Sprintf("http://%s/", proxyLn.Addr()))
	if err != nil {
		log.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	fmt.Printf("response via proxy: %s", body)
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
