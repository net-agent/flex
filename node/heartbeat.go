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

// PingDomain 对指定的节点进行连通性测试并返回RTT。domain为空时，返回到中转节点的RTT
func (node *Node) PingDomain(domain string) (time.Duration, error) {
	port, err := node.GetFreePort()
	defer node.ReleaseUsedPort(port)

	if err != nil {
		return 0, err
	}
	pbuf := packet.NewBuffer(nil)
	pbuf.SetCmd(packet.CmdPingDomain)
	pbuf.SetSrc(node.ip, port)
	pbuf.SetDist(0, 0) // 忽略
	pbuf.SetPayload([]byte(domain))

	ch := make(chan *packet.Buffer) // for response
	node.pingRequests.Store(port, ch)
	defer func() {
		node.pingRequests.Delete(port)
		close(ch)
	}()

	pingStart := time.Now()
	node.WriteBuffer(pbuf)
	<-ch

	return time.Since(pingStart), nil
}
