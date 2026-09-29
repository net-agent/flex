package node

import (
	"github.com/net-agent/flex/v3/internal/admit"
	"github.com/net-agent/flex/v3/packet"
)

// Connect 在已建立的物理连接上与 Switcher 完成认证握手，
// 返回一个已设置好 Domain/IP、可直接调用 Serve 的 Node。
//
// 用法：
//
//	pc := packet.NewWithConn(conn)
//	n, err := node.Connect(pc, "my-domain", "", password)
//	if err != nil {
//		log.Fatal(err)
//	}
//	go n.Serve()
//
// 握手失败时连接的所有权仍属于调用方，由调用方决定是否关闭。
func Connect(conn packet.Conn, domain, mac, password string) (*Node, error) {
	ip, err := admit.Handshake(conn, domain, mac, password)
	if err != nil {
		return nil, err
	}

	n := New(conn)
	n.SetDomain(domain)
	n.SetIP(ip)
	return n, nil
}
