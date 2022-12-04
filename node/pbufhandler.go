package node

import (
	"errors"
	"log"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/stream"
)

func (node *Node) GetStreamBySID(sid uint64, getAndDelete bool) (*stream.Conn, error) {
	// 收到close，代表对端不会再发送数据，可以解除streams绑定
	var it interface{}
	var found bool

	if getAndDelete {
		it, found = node.streams.LoadAndDelete(sid)
	} else {
		it, found = node.streams.Load(sid)
	}
	if !found {
		return nil, errors.New("stream not found")
	}

	c, ok := it.(*stream.Conn)
	if !ok {
		log.Printf("internal error (close)\n")
		return nil, errors.New("stream convert failed")
	}

	return c, nil
}

// 处理远端开启OpenStream的请求
func (node *Node) HandleCmdOpenStream(pbuf *packet.Buffer) {

	it, found := node.listenPorts.Load(pbuf.DistPort())
	if !found {
		node.WriteBuffer(pbuf.SetOpenACK("open on port refused"))
		return
	}
	listener, ok := it.(*Listener)
	if !ok {
		node.WriteBuffer(pbuf.SetOpenACK("open internal error"))
		return
	}

	// 收到对端发过来的open请求，开始准备创建连接
	s := stream.New(false)
	s.SetLocal(pbuf.DistIP(), pbuf.DistPort())
	s.SetRemote(pbuf.SrcIP(), pbuf.SrcPort())
	s.SetDialer(string(pbuf.Payload))
	s.InitWriter(node)

	// 直接进行绑定，做好读数据准备
	_, loaded := node.streams.LoadOrStore(pbuf.SID(), s)
	if loaded {
		log.Printf("stream exist\n")
		node.WriteBuffer(pbuf.SetOpenACK("stream exist"))
		return
	}

	// 告诉对端，连接创建成功了
	// 因为通道能够保证包是顺序的，所以不需要对端的ACK，即可开始发送数据
	node.WriteBuffer(pbuf.SetOpenACK(""))
	listener.AppendConn(s)
}

// 远端已经做好读写准备，可以通知stream，完成open操作
func (node *Node) HandleCmdOpenStreamAck(pbuf *packet.Buffer) {
	it, loaded := node.usedPorts.LoadAndDelete(pbuf.DistPort())
	if !loaded {
		log.Printf("local not found (open-ack). distport=%v\n", pbuf.DistPort())
		return
	}

	s, ok := it.(*stream.Conn)
	if !ok {
		log.Printf("internal error (open-ack)\n")
		return
	}

	// bind streams
	_, loaded = node.streams.LoadOrStore(pbuf.SID(), s)
	if loaded {
		log.Printf("stream exists (open-ack)\n")
		return
	}

	s.Opened(pbuf)
}

// 处理远端的Ping请求
func (node *Node) HandleCmdPingDomain(pbuf *packet.Buffer) {
	if string(pbuf.Payload) != node.domain {
		return
	}

	pbuf.SetCmd(pbuf.Cmd() | packet.CmdACKFlag)
	pbuf.SwapSrcDist()
	pbuf.SetSrc(node.GetIP(), 0)
	node.WriteBuffer(pbuf)
}

func (node *Node) HandleCmdPingDomainAck(pbuf *packet.Buffer) {
	it, found := node.pingRequests.Load(pbuf.DistPort())
	if !found {
		return
	}
	ch, ok := it.(chan *packet.Buffer)
	if !ok {
		return
	}

	ch <- pbuf // 注意：pbuf的控制权转移至channel里
}

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
	node.ReleaseUsedPort(port)
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
	node.ReleaseUsedPort(port)
}
