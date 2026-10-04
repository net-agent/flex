// presence 演示如何使用 Watch 订阅 named node 的上下线状态，
// 替代基于 PingDomain 的轮询。
package main

import (
	"fmt"
	"log"
	"net"
	"time"

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

	// watcher 节点订阅 node-2 的上下线状态
	watcher := connectNode(ln.Addr().String(), "watcher", password)
	defer watcher.Close()

	watcher.SetPresenceHandler(func(ev packet.PresenceEvent) {
		fmt.Printf("event:   domain=%v online=%v ip=%v version=%v seq=%v\n",
			ev.Domain, ev.Online, ev.IP, ev.Version, ev.Seq)
	})

	// 订阅成功即返回当前状态快照（此刻 node-2 尚未上线）
	states, err := watcher.Watch(3*time.Second, "node-2")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("snapshot: %+v\n", states)

	// node-2 上线，watcher 收到 online 事件
	n2 := connectNode(ln.Addr().String(), "node-2", password)
	time.Sleep(time.Second)

	// 本地视图随事件增量更新
	st, ok := watcher.GetPresence("node-2")
	fmt.Printf("view:    %+v ok=%v\n", st, ok)

	// node-2 下线，watcher 收到 offline 事件
	n2.Close()
	time.Sleep(time.Second)
}

func connectNode(addr, domain, password string) *node.Node {
	conn, _ := net.Dial("tcp", addr)
	pc := packet.NewWithConn(conn)
	n, err := node.Connect(pc, domain, "", password)
	if err != nil {
		log.Fatal(err)
	}
	go n.Serve()
	time.Sleep(50 * time.Millisecond)
	return n
}
