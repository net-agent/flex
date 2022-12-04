package node

import (
	"github.com/net-agent/flex/v2/packet"
)

// pbufRouteLoop 处理流数据传输的逻辑（open-ack -> push -> push-ack -> close -> close-ack）
func (node *Node) pbufRouteLoop(ch chan *packet.Buffer) {
	for pbuf := range ch {
		switch pbuf.CmdType() {

		case packet.CmdPushStreamData:
			if pbuf.IsACK() {
				node.HandleCmdPushStreamDataAck(pbuf)
			} else {
				node.HandleCmdPushStreamData(pbuf)
			}

		case packet.CmdOpenStream:
			if pbuf.IsACK() {
				node.HandleCmdOpenStreamAck(pbuf)
			} else {
				go node.HandleCmdOpenStream(pbuf)
			}

		case packet.CmdCloseStream:
			if pbuf.IsACK() {
				go node.HandleCmdCloseStreamAck(pbuf)
			} else {
				go node.HandleCmdCloseStream(pbuf)
			}

		case packet.CmdPingDomain:
			if pbuf.IsACK() {
				go node.HandleCmdPingDomainAck(pbuf)
			} else {
				go node.HandleCmdPingDomain(pbuf)
			}
		}
	}
}
