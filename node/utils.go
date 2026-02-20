package node

import "github.com/net-agent/flex/v2/packet"

func appendWindowSize(buf []byte, size int32) []byte {
	return append(buf,
		byte(size>>24),
		byte(size>>16),
		byte(size>>8),
		byte(size),
	)
}

func readUint32(b []byte) uint32 {
	return uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])
}

func writeUint32(b []byte, v uint32) {
	b[0] = byte(v >> 24)
	b[1] = byte(v >> 16)
	b[2] = byte(v >> 8)
	b[3] = byte(v)
}

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
