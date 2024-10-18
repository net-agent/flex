package node

import (
	"errors"
	"sync"
	"time"

	"github.com/net-agent/flex/v2/numsrc"
	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/warning"
)

var (
	ErrSidIsAttached = errors.New("sid is attached")
	ErrWriterIsNil   = errors.New("writer is nil")
	ErrNodeIsStopped = errors.New("node is stopped")
)

var (
	DefaultHeartbeatInterval = time.Second * 15
	DefaultWriteLocalTimeout = time.Second * 15
)

type Node struct {
	packet.Conn
	writeMut             sync.Mutex
	lastWriteTime        time.Time
	heartbeatInterval    time.Duration // 探活的时间间隔
	writePbufChanTimeout time.Duration // 读取数据包的超时时间

	cmdChan  chan *packet.Buffer
	dataChan chan *packet.Buffer
	running  bool
	chanMut  sync.RWMutex

	ListenHub     // 提供Listen实现
	Dialer        // 提供Dial、DialDomain、DialIP实现
	Pinger        // 提供PingDomain实现
	DataHub       // 处理Data、DataAck、Close、CloseAck
	warning.Guard // 处理后台任务的异常信息

	network string
	domain  string
	ip      uint16

	onceClose    sync.Once
	aliveChecker func() error
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
	node.aliveChecker = func() error {
		_, err := node.PingDomain("", time.Second*2)
		return err
	}

	return node
}

func (node *Node) SetNetwork(n string)     { node.network = n }
func (node *Node) GetNetwork() string      { return node.network }
func (node *Node) SetDomain(domain string) { node.domain = domain }
func (node *Node) GetDomain() string       { return node.domain }
func (node *Node) SetIP(ip uint16)         { node.ip = ip }
func (node *Node) GetIP() uint16           { return node.ip }

func (node *Node) Run() error {
	err := node.initRun()
	if err != nil {
		return err
	}
	defer node.Close()

	// 从pbuf channel中消费数据
	// 不断路由收到的pbuf
	go node.routeCmdPbufChan(node.cmdChan)
	go node.routeDataPbufChan(node.dataChan)

	// 创建一个计时器，用于定时探活
	ticker := time.NewTicker(node.heartbeatInterval)
	defer ticker.Stop()
	go node.keepalive(ticker)

	// 不断读取pbuf直到底层网络通信失败为止
	// 正常情况下阻塞于此
	return node.readBufferUntilFailed()
}

// Run之前的资源创建工作
func (node *Node) initRun() error {
	node.chanMut.Lock()
	defer node.chanMut.Unlock()

	if node.running {
		return errors.New("repeat run detected")
	}
	node.running = true

	// 创建不同的缓存队列，避免不同功能的数据包相互影响
	// 根据是否需要有序分为：Cmd和Data两种类型
	node.cmdChan = make(chan *packet.Buffer, 1024)  // 用于缓存OpenStream、PushDataAck的队列，这两个无需保证顺序
	node.dataChan = make(chan *packet.Buffer, 1024) // 与数据传输有关的包，OpenStreamAck、PushData、Close、CloseAck，需要保证顺序

	return nil
}

func (node *Node) Close() error {
	node.onceClose.Do(func() {
		// 安全清理相关资源
		node.chanMut.Lock()
		if node.running {
			node.running = false
			close(node.cmdChan)
			close(node.dataChan)
		}
		node.chanMut.Unlock()

		node.Conn.Close()
	})
	return nil
}

// WriteBuffer goroutine safe writer
func (node *Node) WriteBuffer(pbuf *packet.Buffer) error {
	if pbuf.DistIP() == 0 || pbuf.DistIP() == node.ip {
		// 跳过网络传输直接送入数据缓存队列中
		node.handlePbuf(pbuf)
		return nil
	}
	if node.Conn == nil {
		return ErrWriterIsNil
	}
	node.writeMut.Lock()
	defer node.writeMut.Unlock()

	node.lastWriteTime = time.Now()
	return node.Conn.WriteBuffer(pbuf)
}

func (node *Node) readBufferUntilFailed() error {
	for {
		node.SetReadTimeout(time.Minute * 15)
		pbuf, err := node.ReadBuffer()
		if err != nil {
			return err
		}

		err = node.handlePbuf(pbuf)
		if err != nil {
			return err
		}
	}

}

func (node *Node) handlePbuf(pbuf *packet.Buffer) error {
	node.chanMut.RLock()
	defer node.chanMut.RUnlock()

	if !node.running {
		return ErrNodeIsStopped
	}

	switch pbuf.Cmd() {
	case packet.CmdPushStreamData:
		node.dataChan <- pbuf // PushData是最常见的，设置最短比较路径
	case packet.CmdOpenStream,
		packet.AckPushStreamData,
		packet.CmdPingDomain,
		packet.AckPingDomain:
		node.cmdChan <- pbuf
	default:
		node.dataChan <- pbuf
	}

	return nil
}

// pbufRouteLoop 处理流数据传输的逻辑（open-ack -> push -> push-ack -> close -> close-ack）
func (node *Node) routeCmdPbufChan(ch chan *packet.Buffer) {
	for pbuf := range ch {
		switch pbuf.Cmd() {
		case packet.AckPushStreamData:
			node.HandleAckPushStreamData(pbuf)

		case packet.CmdOpenStream:
			node.HandleCmdOpenStream(pbuf)

		case packet.CmdPingDomain:
			node.HandleCmdPingDomain(pbuf)

		case packet.AckPingDomain:
			node.HandleAckPingDomain(pbuf)

		default:
			node.PopupWarning("unknown cmd", pbuf.HeaderString())
		}
	}
}
func (node *Node) routeDataPbufChan(ch chan *packet.Buffer) {
	for pbuf := range ch {
		switch pbuf.Cmd() {
		case packet.CmdPushStreamData:
			node.HandleCmdPushStreamData(pbuf)

		case packet.AckOpenStream:
			node.HandleAckOpenStream(pbuf)

		case packet.CmdCloseStream:
			node.HandleCmdCloseStream(pbuf)

		case packet.AckCloseStream:
			node.HandleAckCloseStream(pbuf)

		default:
			node.PopupWarning("unknown cmd", pbuf.HeaderString())
		}
	}
}

func (node *Node) keepalive(ticker *time.Ticker) {
	for range ticker.C {
		if time.Since(node.lastWriteTime) < node.heartbeatInterval {
			continue
		}

		if node.aliveChecker == nil {
			node.PopupWarning("aliveChecker is nil", "")
			return
		}

		err := node.aliveChecker()
		if err != nil {
			node.PopupWarning("check alive failed", err.Error())
			node.Close()
			ticker.Stop()
			return
		}
	}
}
