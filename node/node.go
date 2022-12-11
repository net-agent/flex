package node

import (
	"errors"
	"log"
	"sync"
	"time"

	"github.com/net-agent/flex/v2/numsrc"
	"github.com/net-agent/flex/v2/packet"
)

var (
	ErrSidIsAttached = errors.New("sid is attached")
)

const (
	DefaultHeartbeatInterval = time.Second * 15
	DefaultWriteLocalTimeout = time.Second * 15
)

type Node struct {
	packet.Conn
	writeMut      sync.Mutex
	lastWriteTime time.Time

	ListenHub // 提供Listen实现
	Dialer    // 提供Dial、DialDomain、DialIP实现
	Pinger    // 提供PingDomain实现
	DataHub   // 处理Data、DataAck、Close、CloseAck

	domain string
	ip     uint16

	portm     *numsrc.Manager // 用于管理和分配端口资源
	running   bool
	onceClose sync.Once
}

func New(conn packet.Conn) *Node {
	portm, _ := numsrc.NewManager(1, 1000, 0xFFFF)

	node := &Node{
		Conn:          conn,
		portm:         portm,
		lastWriteTime: time.Now(),
		running:       false,
	}

	node.ListenHub.Init(node)
	node.Dialer.Init(node)
	node.Pinger.Init(node)
	node.DataHub.Init(node)

	return node
}

func (node *Node) SetDomain(domain string) { node.domain = domain }
func (node *Node) GetDomain() string       { return node.domain }
func (node *Node) SetIP(ip uint16)         { node.ip = ip }
func (node *Node) GetIP() uint16           { return node.ip }

func (node *Node) Run() {
	if node.running {
		return
	}
	node.running = true
	// 创建一个1024长度的pbuf channel用于缓存读取到的pbuf
	// 解耦读数据和处理数据
	pbufCh := make(chan *packet.Buffer, 1024)
	defer close(pbufCh)
	go node.readBufferUntilFailed(pbufCh)

	// 创建一个计时器，用于定时探活
	ticker := time.NewTicker(DefaultHeartbeatInterval)
	defer ticker.Stop()
	go node.keepalive(ticker)

	// 从pbuf channel中消费数据
	// 不断路由收到的pbuf
	node.pbufRouteLoop(pbufCh)
	node.running = false
}

func (node *Node) Close() error {
	node.onceClose.Do(func() {
		node.Conn.Close()
	})
	return nil
}

// WriteBuffer goroutine safe writer
func (node *Node) WriteBuffer(pbuf *packet.Buffer) error {
	if node.Conn == nil {
		return errors.New("packet conn is nil")
	}
	node.writeMut.Lock()
	defer node.writeMut.Unlock()

	node.lastWriteTime = time.Now()
	return node.Conn.WriteBuffer(pbuf)
}

func (node *Node) readBufferUntilFailed(ch chan *packet.Buffer) {
	for {
		pbuf, err := node.ReadBuffer()
		if err != nil {
			log.Printf("readBufferUntilFailed err=%v\n", err)
			return
		}

		select {
		case ch <- pbuf:
		case <-time.After(DefaultWriteLocalTimeout):
			log.Printf("readBufferUntilFailed packet discarded. %v\n", pbuf.HeaderString())
		}
	}
}

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

func (node *Node) keepalive(ticker *time.Ticker) {
	for range ticker.C {
		if time.Since(node.lastWriteTime) > (DefaultHeartbeatInterval - time.Second) {
			_, err := node.PingDomain("", time.Second*3)
			if err != nil {
				log.Printf("keepaliveWithPeer failed: %v\n", err)
				node.Close()
				ticker.Stop()
				return
			}
		}
	}
}
