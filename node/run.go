package node

import (
	"errors"
	"log"
	"sync"
	"time"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/stream"
)

const (
	DefaultHeartbeatInterval = time.Second * 15
	DefaultWriteLocalTimeout = time.Second * 15
)

type Node struct {
	ip uint16
	packet.Conn
	pushChan        chan *packet.Buffer
	enableLocalLoop bool
	localPushChan   chan *packet.Buffer

	freePorts   chan uint16
	listenPorts sync.Map
	usedPorts   sync.Map
	streams     sync.Map

	writeMut      sync.Mutex
	lastWriteTime time.Time
}

func New(conn packet.Conn) *Node {
	freePorts := make(chan uint16, 65536)
	for i := 1024; i < 65536; i++ {
		freePorts <- uint16(i)
	}

	return &Node{
		Conn:          conn,
		pushChan:      make(chan *packet.Buffer, 1024),
		localPushChan: make(chan *packet.Buffer, 1024*4),
		freePorts:     freePorts,
		lastWriteTime: time.Now(),
	}
}

func (node *Node) SetIP(ip uint16) {
	node.ip = ip
}

// WriteBuffer goroutine safe writer
func (node *Node) WriteBuffer(pbuf *packet.Buffer) error {
	if node.enableLocalLoop && (pbuf.DistIP() == node.ip) {
		return node.routeLocalBuffer(pbuf)
	}

	node.writeMut.Lock()
	defer node.writeMut.Unlock()

	node.lastWriteTime = time.Now()
	return node.Conn.WriteBuffer(pbuf)
}

func (node *Node) EnableLocalLoop(enable bool) {
	node.enableLocalLoop = enable
}

//
func (node *Node) routeBuffer(pbuf *packet.Buffer) error {
	if pbuf.Cmd() == packet.CmdOpenStream {
		go node.OnOpen(pbuf)
		return nil
	}
	select {
	case node.pushChan <- pbuf:
		return nil
	case <-time.After(DefaultWriteLocalTimeout):
		return errors.New("write localLoop timeout")
	}
}

// todo: bucket的逻辑导致此处效率不高（读写变成串行）
func (node *Node) routeLocalBuffer(pbuf *packet.Buffer) error {
	if pbuf.Cmd() == packet.CmdOpenStream {
		go node.OnOpen(pbuf)
		return nil
	}
	node.localPushChan <- pbuf
	return nil
}

func (node *Node) Run() {
	ticker := time.NewTicker(DefaultHeartbeatInterval)
	go node.heartbeatLoop(ticker)
	go node.pushBufLoop(node.pushChan)
	go node.pushBufLoop(node.localPushChan)

	// quick read loop
	// 不在此循环中做太多逻辑，确保读取效率
	for {
		pbuf, err := node.ReadBuffer()
		if err != nil {
			return
		}

		err = node.routeBuffer(pbuf)
		if err != nil {
			return
		}
	}
}

// OnOpen 响应创建链接的需求
func (node *Node) OnOpen(pbuf *packet.Buffer) {

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

// OnOpenACK
// 对端已经做好读写准备，可以通知stream，完成open操作
func (node *Node) OnOpenACK(pbuf *packet.Buffer) {
	it, loaded := node.usedPorts.LoadAndDelete(pbuf.DistPort())
	if !loaded {
		log.Printf("local not found\n")
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

// pushBufLoop 处理流数据传输的逻辑（open-ack -> push -> push-ack -> close -> close-ack）
func (node *Node) pushBufLoop(ch chan *packet.Buffer) {
	for pbuf := range ch {

		switch pbuf.Cmd() & 0xFE {

		case packet.CmdOpenStream:
			// 外部的筛选逻辑确保此处全是open-ack
			// 必须顺序处理ACK，才能确保push不会先于open-ack被执行（会有丢包发生）
			node.OnOpenACK(pbuf)

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
				// push-ack
				// 收到确认包，此时应该根据ack信息，恢复stream的数据桶容量
				c.IncreaseBucket(pbuf.ACKInfo())
			} else {
				// push
				// 收到新的数据，追加到strema中
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
				log.Printf("internal error (close)\n")
				continue
			}

			go func(conn *stream.Conn, isACK bool) {
				usedPort, err := conn.GetUsedPort()
				if err == nil {
					node.freePorts <- usedPort
				}

				if isACK {
					// close-ack
					// 当前连接已经处于“半关闭”状态，写状态已提前关闭，因此只需要把读状态关闭即可
					conn.AppendEOF()
				} else {
					// close
					// 收到对端发过来的close消息，可以确定对端已经不会再有数据过来，所以EOF
					// 同时，应该马上关闭写状态（暂时不允许半开状态长时间保留）
					conn.AppendEOF()
					conn.CloseWrite(true)
				}
			}(c, pbuf.IsACK())
		}

	}
}

func (node *Node) heartbeatLoop(ticker *time.Ticker) {
	pbuf := packet.NewBuffer(nil)
	pbuf.SetCmd(packet.CmdAlive)
	pbuf.SetSrc(node.ip, 0)
	pbuf.SetDist(0xffff, 0)
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
