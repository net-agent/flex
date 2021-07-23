package node

import (
	"fmt"
	"log"
	"sync"

	"github.com/net-agent/flex/packet"
	"github.com/net-agent/flex/stream"
)

type Node struct {
	packet.Conn
	openChan chan *packet.Buffer
	pushChan chan *packet.Buffer

	freePorts   chan uint16
	listenPorts sync.Map
	usedPorts   sync.Map
	streams     sync.Map
}

func (node *Node) Run() {
	go node.openBufLoop()
	go node.pushBufLoop()

	// do read
	for {
		pbuf, err := node.ReadBuffer()
		if err != nil {
			return
		}

		switch pbuf.Cmd() {
		case packet.CmdOpenStream:
			node.openChan <- pbuf

		// 为了保证时序，close、close-ack必须放在push序列中
		case packet.CmdCloseStream:
			node.pushChan <- pbuf
		case packet.CmdPushStreamData:
			node.pushChan <- pbuf
		}
	}
}

// ackLoop 处理应答包的逻辑
func (node *Node) ackLoop() {
	for pbuf := range node.ackChan {
		switch pbuf.Cmd() {

		// open-ack
		// 收到open应答包，代表对端已经做好接收数据的准备
		// 调用stream.Opend回调，解除stream.open阻塞
		// 此时此刻，stream可以安全进行双向通信
		case packet.CmdOpenStream:
			it, found := node.usedPorts.LoadAndDelete(pbuf.DistPort())
			if !found {
				log.Printf("localports not found (open-ack)\n")
				continue
			}
			s := it.(*stream.Conn)
			s.Opened(pbuf.Payload)

		// close-ack
		// 收到close应答包，代表对端已经不会再发送数据
		// 此刻可以安全解除stream与node的绑定
		// 此时此刻，stream完全关闭双向通信
		case packet.CmdCloseStream:
			// 收到close-ack，代表EOF，可以放心删除stream
			it, loaded := node.streams.LoadAndDelete(pbuf.SID())
			if !loaded {
				log.Printf("detach failed (close-ack)\n")
				return
			}
			s, ok := it.(*stream.Conn)
			if !ok {
				log.Printf("internal error (close-ack)\n")
				continue
			}
			node.freePorts <- s.LocalPort()
			s.Closed()

		case packet.CmdPushStreamData:
		}
	}
}

// openBufLoop 处理open请求的逻辑
func (node *Node) openBufLoop() {
	for pbuf := range node.openChan {
		it, found := node.listenPorts.Load(pbuf.DistPort())
		if !found {
			log.Printf("port not listened\n")
			continue
		}

		listener, ok := it.(*Listener)
		if !ok {
			log.Printf("open internal error\n")
			continue
		}

		s := stream.New()
		_, loaded := node.streams.LoadOrStore(pbuf.SID(), s)
		if loaded {
			log.Printf("stream exist\n")
			continue
		}

		go func(buf *packet.Buffer) {
			// 告诉对端，连接创建成功了
			pbuf.SetOpenACK(nil)
			node.WriteBuffer(pbuf)

			// 告诉caller，可以开始发送数据了
			listener.openedStream <- s
		}(pbuf)
	}
}

// pushBufLoop 处理流数据传输的逻辑
func (node *Node) pushBufLoop() {
	for pbuf := range node.pushChan {

		switch pbuf.Cmd() {

		case packet.CmdPushStreamData:

			it, found := node.streams.Load(pbuf.SID())
			if !found {
				log.Printf("sid not found (push)\n")
				continue
			}

			c, ok := it.(*stream.Conn)
			if !ok {
				log.Printf("internal error (push)\n")
				continue
			}

			if pbuf.IsACK() {
				c.IncreaseBucket(pbuf.ACKInfo())
			} else {
				c.AppendData(pbuf.Payload)
			}

		case packet.CmdCloseStream:

			// 收到close，代表对端不会再发送数据，可以解除streams绑定
			it, found := node.streams.LoadAndDelete(pbuf.SID())
			if !found {
				continue
			}
			c, ok := it.(*stream.Conn)
			if !ok {
				fmt.Printf("internal error (close)")
				continue
			}

			go func(conn *stream.Conn, isACK bool) {
				if isACK {
					conn.AppendEOF()
				} else {
					conn.Close() // stop read and write
				}
			}(c, pbuf.IsACK())
		}

	}
}
