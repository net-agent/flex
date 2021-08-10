package node

import (
	"errors"
	"time"

	"github.com/net-agent/flex/v2/packet"
)

// WriteBuffer goroutine safe writer
func (node *Node) WriteBuffer(pbuf *packet.Buffer) error {
	if EnableLocalLoop {
		dist := pbuf.DistIP()
		if dist == node.ip || dist == LocalIP {
			return node.writeLocal(pbuf)
		}
	}

	node.writeMut.Lock()
	defer node.writeMut.Unlock()

	node.lastWriteTime = time.Now()
	return node.Conn.WriteBuffer(pbuf)
}

func (node *Node) writeLocal(pbuf *packet.Buffer) error {
	if pbuf.Cmd() == packet.CmdOpenStream {
		// 此处模拟switcher中ResolveOpenCmd的逻辑，为请求包附带dialer信息
		pbuf.SetPayload([]byte("local"))
	}
	ch := node.localBufPipe
	if pbuf.Cmd() == (packet.CmdACKFlag | packet.CmdPushStreamData) {
		ch = node.localAckBufPipe
	}
	select {
	case ch <- pbuf:
		return nil
	case <-time.After(DefaultWriteLocalTimeout):
		return errors.New("write local timeout")
	}
}
