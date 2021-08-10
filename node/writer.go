package node

import (
	"errors"
	"time"

	"github.com/net-agent/flex/v2/packet"
)

// WriteBuffer goroutine safe writer
func (node *Node) WriteBuffer(pbuf *packet.Buffer) error {
	dist := pbuf.DistIP()
	if dist == node.ip || dist == LocalIP {
		select {
		case node.localBufPipe <- pbuf:
			return nil
		case <-time.After(DefaultWriteLocalTimeout):
			return errors.New("write local timeout")
		}
	}

	node.writeMut.Lock()
	defer node.writeMut.Unlock()

	node.lastWriteTime = time.Now()
	return node.Conn.WriteBuffer(pbuf)
}
