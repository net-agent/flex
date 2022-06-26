package node

import (
	"log"
	"time"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/vars"
)

func (node *Node) heartbeatLoop(ticker *time.Ticker) {
	pbuf := packet.NewBuffer(nil)
	pbuf.SetCmd(packet.CmdAlive)
	pbuf.SetSrc(node.ip, 0)
	pbuf.SetDist(vars.SwitcherIP, 0)
	pbuf.SetPayload(nil)

	for range ticker.C {
		if time.Since(node.lastWriteTime) > (DefaultHeartbeatInterval - time.Second) {
			err := node.WriteBuffer(pbuf)
			if err != nil {
				log.Printf("write heartbeat-data failed: %v\n", err)
				node.Close()
				ticker.Stop()
				return
			}
		}
	}
}
