package node

import (
	"errors"
	"sync"
	"time"

	"github.com/net-agent/flex/v2/packet"
)

const (
	DefaultHeartbeatInterval = time.Second * 15
	DefaultWriteLocalTimeout = time.Second * 15
)

type Node struct {
	domain string
	ip     uint16
	packet.Conn
	readBufChan  chan *packet.Buffer // 从Conn中读到的数据包队列
	localBufPipe chan *packet.Buffer // 本机回环请求产生的数据包队列

	freePorts   chan uint16
	listenPorts sync.Map
	usedPorts   sync.Map
	streams     sync.Map

	writeMut      sync.Mutex
	lastWriteTime time.Time

	onceClose sync.Once
}

func New(conn packet.Conn) *Node {
	freePorts := make(chan uint16, 65536)
	for i := 1024; i < 65536; i++ {
		freePorts <- uint16(i)
	}

	return &Node{
		Conn:          conn,
		readBufChan:   make(chan *packet.Buffer, 1024),
		localBufPipe:  make(chan *packet.Buffer, 1024),
		freePorts:     freePorts,
		lastWriteTime: time.Now(),
	}
}

func (node *Node) SetDomain(domain string) {
	node.domain = domain
}
func (node *Node) SetIP(ip uint16) {
	node.ip = ip
}

func (node *Node) Run() {
	ticker := time.NewTicker(DefaultHeartbeatInterval)
	go node.heartbeatLoop(ticker)
	go node.pbufRouteLoop(node.readBufChan)
	go node.pbufRouteLoop(node.localBufPipe)

	var wg sync.WaitGroup
	wg.Add(1)
	go node.readLoop(&wg)

	wg.Wait()
}

func (node *Node) Close() error {
	node.onceClose.Do(func() {
		node.Conn.Close()
		close(node.localBufPipe)
	})
	return nil
}

const (
	DefaultGetFreePortTimeout = time.Second * 2
)

func (node *Node) GetIP() uint16 {
	return node.ip
}

func (node *Node) GetFreePort() (uint16, error) {
	for {
		select {
		case port := <-node.freePorts:
			_, loaded := node.listenPorts.Load(port)
			if loaded {
				// 此端口被listener占用
				continue
			}
			return port, nil
		case <-time.After(DefaultGetFreePortTimeout):
			return 0, errors.New("free port dry")
		}
	}
}
