package node

import (
	"errors"

	"github.com/net-agent/flex/v2/packet"
)

var (
	errStreamNotFound      = errors.New("stream not found")
	errConvertStreamFailed = errors.New("convert stream failed")
	ErrInvalidListener     = errors.New("invalid listener")
)

// 处理数据包
func (node *Node) HandleCmdPushStreamData(pbuf *packet.Buffer) {
	c, err := node.GetStreamBySID(pbuf.SID(), false)
	if err != nil {
		return
	}

	c.AppendData(pbuf.Payload)
}

// 处理数据包已送达的消息
func (node *Node) HandleCmdPushStreamDataAck(pbuf *packet.Buffer) {
	c, err := node.GetStreamBySID(pbuf.SID(), false)
	if err != nil {
		return
	}

	c.IncreaseBucket(pbuf.ACKInfo())
}

func (node *Node) HandleCmdCloseStream(pbuf *packet.Buffer) {
	conn, err := node.GetStreamBySID(pbuf.SID(), true)
	if err != nil {
		return
	}

	conn.AppendEOF()
	conn.CloseWrite(true)

	port, err := conn.GetUsedPort()
	if err != nil {
		return
	}
	node.portm.ReleaseNumberSrc(port)
}

func (node *Node) HandleCmdCloseStreamAck(pbuf *packet.Buffer) {
	conn, err := node.GetStreamBySID(pbuf.SID(), true)
	if err != nil {
		return
	}

	conn.AppendEOF()

	port, err := conn.GetUsedPort()
	if err != nil {
		return
	}
	node.portm.ReleaseNumberSrc(port)
}
