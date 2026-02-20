package switcher

import (
	"time"

	"github.com/net-agent/flex/v2/handshake"
	"github.com/net-agent/flex/v2/node"
	"github.com/net-agent/flex/v2/packet"
)

func initTestEnv(domain1, domain2 string) (*Server, *node.Node, *node.Node) {
	pswd := "testpswd"
	s := NewServer(pswd, nil, nil)
	pc1, pc2 := packet.Pipe()
	pc3, pc4 := packet.Pipe()

	go s.HandlePacketConn(pc2)
	go s.HandlePacketConn(pc4)

	ip1, _ := handshake.UpgradeRequest(pc1, domain1, "", pswd)
	node1 := node.New(pc1)
	node1.SetIP(ip1)
	node1.SetDomain(domain1)
	go node1.Run()

	ip2, _ := handshake.UpgradeRequest(pc3, domain2, "", pswd)
	node2 := node.New(pc3)
	node2.SetIP(ip2)
	node2.SetDomain(domain2)
	go node2.Run()

	<-time.After(time.Millisecond * 50)

	return s, node1, node2
}
