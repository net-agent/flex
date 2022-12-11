package node

import (
	"errors"
	"log"
	"sync"
	"time"

	"github.com/net-agent/flex/v2/numsrc"
	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/stream"
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

	ListenerHub          // 提供Listen实现
	Dialer               // 提供Dial、DialDomain、DialIP实现
	Pinger               // 提供PingDomain实现
	streams     sync.Map // map[sid]*stream

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

	node.ListenerHub.Init(node)
	node.Dialer.Init(node)
	node.Pinger.Init(node)

	return node
}

func (node *Node) SetDomain(domain string) { node.domain = domain }
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

func (node *Node) AttachStream(s *stream.Conn, sid uint64) error {
	_, loaded := node.streams.LoadOrStore(sid, s)
	if loaded {
		// 已经存在还未释放的stream
		return ErrSidIsAttached
	}
	return nil
}

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
