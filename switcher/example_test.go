package switcher_test

import (
	"fmt"

	"github.com/net-agent/flex/v3/node"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/switcher"
)

// 在内存连接上完成一次握手，Switcher 会为节点分配虚拟 IP。
func ExampleServer_ServeConn() {
	srv := switcher.NewServer("secret", nil, nil)
	defer srv.Close()
	srv.SetLimits(switcher.Limits{
		MaxConnections:           10,
		MaxStreamsPerContext:     100,
		MaxPresenceSubscriptions: 10,
		HandshakeRatePerIP:       100,
		HandshakeBurstPerIP:      100,
	})

	client, server := packet.Pipe()
	go func() { _ = srv.ServeConn(server) }()

	n, err := node.Connect(client, "demo", "", "secret")
	if err != nil {
		fmt.Println("connect:", err)
		return
	}
	defer n.Close()

	fmt.Println("domain:", n.GetDomain(), "ip:", n.GetIP())

	// Output:
	// domain: demo ip: 1
}
