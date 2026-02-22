package node

import (
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v3/internal/idpool"
	"github.com/net-agent/flex/v3/packet"
)

var (
	ErrSidIsAttached = errors.New("sid is attached")
	ErrWriterIsNil   = errors.New("writer is nil")
	ErrNodeIsStopped = errors.New("node is stopped")
	ErrRepeatRun     = errors.New("repeat run detected")
)

var (
	DefaultHeartbeatInterval = time.Second * 15
)

type FlowConfig struct {
	Bandwidth     int64 // bytes per second
	RTT           time.Duration
	MaxWindowSize int32
}

type Node struct {
	packet.Conn

	Dispatcher
	Heartbeat

	ListenHub // 提供Listen实现
	Dialer    // 提供Dial、DialDomain、DialIP实现
	Pinger    // 提供PingDomain实现
	StreamHub // 处理Data、DataAck、Close、CloseAck
	logger    *slog.Logger

	network string
	domain  string
	ip      uint16

	done      chan struct{}
	onceClose sync.Once

	writtenDataSize int64
	readDataSize    int64

	flowConfig FlowConfig
}

type NodeInfo struct {
	Domain       string `json:"domain"`
	IP           uint16 `json:"ip"`
	Network      string `json:"network"`
	Uptime       int64  `json:"uptime_seconds"`
	BytesRead    int64  `json:"bytes_read"`
	BytesWritten int64  `json:"bytes_written"`
}

type ListenerInfo struct {
	Port uint16 `json:"port"`
	Addr string `json:"addr"`
}

func New(conn packet.Conn) *Node {
	portm, _ := idpool.New(1000, 0xFFFF)
	return NewWithOptions(conn, portm, DefaultHeartbeatInterval)
}

func NewWithOptions(conn packet.Conn, portm *idpool.Pool, heartbeatInterval time.Duration) *Node {
	node := &Node{
		Conn:   conn,
		done:   make(chan struct{}),
		logger: slog.Default(),
	}

	node.ListenHub.init(node, portm)
	node.Dialer.init(node, portm)
	node.Pinger.init(node)
	node.StreamHub.init(node, portm)
	node.Heartbeat.init(node, heartbeatInterval)
	node.Dispatcher.init(node)

	node.Heartbeat.SetChecker(func() error {
		_, err := node.PingDomain("", time.Second*2)
		return err
	})

	return node
}

func (node *Node) SetNetwork(n string)     { node.network = n }
func (node *Node) GetNetwork() string      { return node.network }
func (node *Node) SetDomain(domain string) { node.domain = domain }
func (node *Node) GetDomain() string       { return node.domain }
func (node *Node) SetIP(ip uint16)         { node.ip = ip }
func (node *Node) GetIP() uint16           { return node.ip }
func (node *Node) SetLogger(l *slog.Logger) {
	if l != nil {
		node.logger = l
	}
}
func (node *Node) GetReadWriteSize() (read, written int64) {
	return node.readDataSize, node.writtenDataSize
}

func (node *Node) GetInfo() *NodeInfo {
	read, written := node.GetReadWriteSize()
	return &NodeInfo{
		Domain:       node.GetDomain(),
		IP:           node.GetIP(),
		Network:      node.GetNetwork(),
		BytesRead:    read,
		BytesWritten: written,
	}
}

func (node *Node) GetListeners() []ListenerInfo {
	listeners := node.getActiveListeners()
	infos := make([]ListenerInfo, 0, len(listeners))
	for _, l := range listeners {
		infos = append(infos, ListenerInfo{
			Port: l.port,
			Addr: l.str,
		})
	}
	return infos
}

func (node *Node) SetFlowConfig(cfg FlowConfig) {
	node.flowConfig = cfg
}

func (node *Node) GetWindowSize() int32 {
	if node.flowConfig.MaxWindowSize > 0 {
		return node.flowConfig.MaxWindowSize
	}
	if node.flowConfig.Bandwidth > 0 && node.flowConfig.RTT > 0 {
		bdp := node.flowConfig.Bandwidth * int64(node.flowConfig.RTT) / int64(time.Second)
		return int32(bdp)
	}
	return 0 // uses default in stream package
}

func (node *Node) Serve() error {
	err := node.Dispatcher.start()
	if err != nil {
		return err
	}
	defer node.Close()

	go node.Dispatcher.processCmdChan()
	go node.Dispatcher.processDataChan()

	ticker := time.NewTicker(node.Heartbeat.interval)
	defer ticker.Stop()
	go node.Heartbeat.run(ticker, node.done, func() { node.Close() })

	return node.readLoop()
}

func (node *Node) Close() error {
	node.onceClose.Do(func() {
		// 1. 广播关闭信号，通知 Heartbeat 等 goroutine 退出
		close(node.done)

		// 2. 停止 Dispatcher，不再接受新的 dispatch
		node.Dispatcher.stop()

		// 3. 关闭所有 Listener，解除阻塞在 Accept() 上的 goroutine
		node.ListenHub.closeAllListeners()

		// 4. 关闭所有活跃 Stream，释放本地资源（不走网络协商）
		node.StreamHub.closeAllStreams()

		// 5. 关闭底层连接，使 readLoop 退出
		node.Conn.Close()
	})
	return nil
}

// WriteBuffer goroutine safe writer
func (node *Node) WriteBuffer(pbuf *packet.Buffer) error {
	if pbuf.DistIP() == 0 || pbuf.DistIP() == node.ip {
		node.Dispatcher.dispatch(pbuf)
		return nil
	}
	if node.Conn == nil {
		return ErrWriterIsNil
	}

	node.Heartbeat.Touch()
	atomic.AddInt64(&node.writtenDataSize, int64(pbuf.PayloadSize()))

	return node.Conn.WriteBuffer(pbuf)
}

func (node *Node) readLoop() error {
	for {
		node.SetReadTimeout(time.Minute * 15)
		pbuf, err := node.ReadBuffer()
		if err != nil {
			return err
		}

		atomic.AddInt64(&node.readDataSize, int64(pbuf.PayloadSize()))

		err = node.Dispatcher.dispatch(pbuf)
		if err != nil {
			return err
		}
	}
}
