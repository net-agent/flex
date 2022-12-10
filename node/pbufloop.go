package node

import (
	"github.com/net-agent/flex/v2/packet"
)

// pbufRouteLoop 处理流数据传输的逻辑（open-ack -> push -> push-ack -> close -> close-ack）
func (node *Node) pbufRouteLoop(ch chan *packet.Buffer) {
	for pbuf := range ch {

		switch pbuf.CmdType() {

		case packet.CmdOpenStream:
			if pbuf.IsACK() {
				node.HandleCmdOpenStreamAck(pbuf)
			} else {
				go node.HandleCmdOpenStream(pbuf)
			}

		case packet.CmdPushStreamData:
			if pbuf.IsACK() {
				go node.HandleCmdPushStreamDataAck(pbuf)
			} else {
				node.HandleCmdPushStreamData(pbuf)
			}

		case packet.CmdCloseStream:
			if pbuf.IsACK() {
				node.HandleCmdCloseStreamAck(pbuf)
			} else {
				node.HandleCmdCloseStream(pbuf)
			}

		case packet.CmdPingDomain:
			if pbuf.IsACK() {
				node.HandleCmdPingDomainAck(pbuf)
			} else {
				node.HandleCmdPingDomain(pbuf)
			}
		}
	}
}
