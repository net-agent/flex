package node

import (
	"errors"
	"log"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/stream"
)

var (
	errStreamNotFound      = errors.New("stream not found")
	errConvertStreamFailed = errors.New("convert stream failed")
	ErrListenerNotFound    = errors.New("listener not found")
	ErrInvalidListener     = errors.New("invalid listener")
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
		return nil, errStreamNotFound
	}

	c, ok := it.(*stream.Conn)
	if !ok {
		return nil, errConvertStreamFailed
	}

	return c, nil
}

func (node *Node) GetListenerByPort(port uint16) (*Listener, error) {
	it, found := node.listenPorts.Load(port)
	if !found {
		return nil, ErrListenerNotFound
	}
	l, ok := it.(*Listener)
	if !ok {
		return nil, ErrInvalidListener
	}
	return l, nil
}

// 处理远端开启OpenStream的请求
func (node *Node) HandleCmdOpenStream(pbuf *packet.Buffer) {
	ackMessage := ""
	defer func() {
		err := node.WriteBuffer(pbuf.SetOpenACK(ackMessage))
		if err != nil {
			log.Printf("HandleCmdOpenStream write ackMessage failed, err=%v\n", err)
		}
	}()

	l, err := node.GetListenerByPort(pbuf.DistPort())
	if err != nil {
		ackMessage = err.Error()
		return
	}

	err = l.HandleCmdOpenStream(pbuf)
	if err != nil {
		ackMessage = err.Error()
		return
	}

	ackMessage = ""
}

// 远端已经做好读写准备，可以通知stream，完成open操作
func (node *Node) HandleCmdOpenStreamAck(pbuf *packet.Buffer) {
	it, loaded := node.ackstreams.LoadAndDelete(pbuf.DistPort())
	if !loaded {
		log.Printf("local not found (open-ack). distport=%v\n", pbuf.DistPort())
		return
	}

	ch, ok := it.(chan *stream.Conn)
	if !ok {
		log.Printf("internal error (open-ack)\n")
		return
	}

	info := string(pbuf.Payload)
	if info != "" {
		log.Printf("open stream failed: %v\n", info)
		ch <- nil
		return
	}

	//
	// create and bind stream
	//
	s := stream.New(true)
	s.SetLocal(pbuf.DistIP(), pbuf.DistPort())
	s.SetRemote(pbuf.SrcIP(), pbuf.SrcPort())
	s.InitWriter(node)

	_, loaded = node.streams.LoadOrStore(pbuf.SID(), s)
	if loaded {
		log.Printf("stream exists (open-ack)\n")
		s.Close()
		return
	}

	ch <- s
	s = nil
}

// 处理远端的Ping请求
func (node *Node) HandleCmdPingDomain(pbuf *packet.Buffer) {
	if string(pbuf.Payload) != node.domain {
		pbuf.SetPayload([]byte("domain not match")) // 应答时payload为空表示成功，非空则记录错误原因
	} else {
		pbuf.SetPayload(nil)
	}

	pbuf.SetCmd(pbuf.Cmd() | packet.CmdACKFlag)
	pbuf.SwapSrcDist()
	pbuf.SetSrc(node.GetIP(), 0)
	node.WriteBuffer(pbuf)
}

func (node *Node) HandleCmdPingDomainAck(pbuf *packet.Buffer) {
	it, found := node.pingRequests.Load(pbuf.DistPort())
	if !found {
		log.Printf("HandleCmdPingDomainAck failed, port='%v' not found\n", pbuf.DistPort())
		return
	}
	ch, ok := it.(chan *packet.Buffer)
	if !ok {
		log.Printf("HandleCmdPingDomainAck failed, convert pbuf chan failed\n")
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
