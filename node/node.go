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

var (
	DefaultHeartbeatInterval = time.Second * 15
	DefaultWriteLocalTimeout = time.Second * 15
)

type Node struct {
	packet.Conn
	writeMut             sync.Mutex
	lastWriteTime        time.Time
	pbufChan             chan *packet.Buffer
	heartbeatInterval    time.Duration // 探活的时间间隔
	writePbufChanTimeout time.Duration // 读取数据包的超时时间

	ListenHub // 提供Listen实现
	Dialer    // 提供Dial、DialDomain、DialIP实现
	Pinger    // 提供PingDomain实现
	DataHub   // 处理Data、DataAck、Close、CloseAck

	domain string
	ip     uint16

	running   bool
	onceClose sync.Once
}

func New(conn packet.Conn) *Node {
	portm, _ := numsrc.NewManager(1, 1000, 0xFFFF)
	return NewWithOptions(conn, portm, DefaultHeartbeatInterval, DefaultWriteLocalTimeout)
}

func NewWithOptions(conn packet.Conn, portm *numsrc.Manager, heartbeatInterval, writePbufChanTimeout time.Duration) *Node {
	node := &Node{
		Conn:                 conn,
		lastWriteTime:        time.Now(),
		running:              false,
		heartbeatInterval:    heartbeatInterval,
		writePbufChanTimeout: writePbufChanTimeout,
	}

	node.ListenHub.Init(node, portm)
	node.Dialer.Init(node, portm)
	node.Pinger.Init(node)
	node.DataHub.Init(portm)

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
	node.pbufChan = pbufCh

	go func() {
		node.readBufferUntilFailed(pbufCh)
		close(pbufCh)
	}()

	// 创建一个计时器，用于定时探活
	ticker := time.NewTicker(DefaultHeartbeatInterval)
	defer ticker.Stop()
	go node.keepalive(ticker)

	// 从pbuf channel中消费数据
	// 不断路由收到的pbuf
	node.pbufRouteLoop(pbufCh)
	node.pbufChan = nil
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
	if pbuf.DistIP() == 0 || pbuf.DistIP() == node.ip {
		if node.pbufChan != nil {
			node.pbufChan <- pbuf
			return nil
		}
	}
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
		// // 丢弃掉distIP不匹配的无效数据包
		// // 疑问：是否应当相信对端发送过来的数据以提升效率？
		// if pbuf.DistIP() != node.ip {
		// 	log.Printf("pbuf[cmd=%v] has been discarded. distIP='%v' localIP='%v'\n", pbuf.CmdName(), pbuf.DistIP(), node.ip)
		// 	continue
		// }

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
		if time.Since(node.lastWriteTime) < DefaultHeartbeatInterval {
			continue
		}

		_, err := node.PingDomain("", time.Second*3)
		if err == nil {
			continue
		}

		log.Printf("keepalive error: %v\n", err)
		node.Close()
		ticker.Stop()
		return
	}
}
