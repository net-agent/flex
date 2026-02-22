package node

import "github.com/net-agent/flex/v3/packet"

func Pipe(domain1, domain2 string) (*Node, *Node) {
	c1, c2 := packet.Pipe()
	node1 := New(c1)
	node2 := New(c2)

	node1.SetDomain(domain1)
	node1.SetIP(1)
	go node1.Serve()

	node2.SetDomain(domain2)
	node2.SetIP(2)
	go node2.Serve()

	return node1, node2
}
