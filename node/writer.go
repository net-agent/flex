package node

import (
	"time"

	"github.com/net-agent/flex/v2/packet"
)

// WriteBuffer goroutine safe writer
func (node *Node) WriteBuffer(pbuf *packet.Buffer) error {

	node.writeMut.Lock()
	defer node.writeMut.Unlock()

	node.lastWriteTime = time.Now()
	return node.Conn.WriteBuffer(pbuf)
}
