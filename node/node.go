package node

import (
	"errors"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v3/internal/idpool"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/stream"
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
	pconn packet.Conn

	dispatcher Dispatcher
	heartbeat  Heartbeat
	listenHub  ListenHub
	dialer     Dialer
	pinger     Pinger
	streamHub  StreamHub
	logger     *slog.Logger

	network string
	domain  string
	ip      uint16

	done      chan struct{}
	onceClose sync.Once

	startedAt time.Time
	trace     *Trace
	failures  FailureStats

	writtenDataSize atomic.Int64
	readDataSize    atomic.Int64

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
	// 端口范围是合法常量，error 只会来自非法范围，此处可安全忽略
	n, _ := NewWithOptions(conn, 1000, 0xFFFF, DefaultHeartbeatInterval)
	return n
}

// NewWithOptions 创建 Node，并允许自定义虚拟端口分配范围与心跳间隔。
// 端口范围非法（portMin > portMax）时返回 idpool.ErrInvalidRange。
func NewWithOptions(conn packet.Conn, portMin, portMax uint16, heartbeatInterval time.Duration) (*Node, error) {
	portm, err := idpool.New(portMin, portMax)
	if err != nil {
		return nil, err
	}
	node := &Node{
		pconn:     conn,
		done:      make(chan struct{}),
		logger:    slog.Default(),
		startedAt: time.Now(),
	}

	node.listenHub.init(node, portm)
	node.dialer.init(node, portm)
	node.pinger.init(node)
	node.streamHub.init(node, portm)
	node.heartbeat.init(node, heartbeatInterval)
	node.dispatcher.init(node)

	node.heartbeat.SetChecker(func() error {
		rtt, err := node.PingDomain("", time.Second*2)
		if err == nil {
			node.heartbeat.setLastRTT(rtt)
		}
		return err
	})

	return node, nil
}

// SetNetwork 设置网络类型标识。仅应在 Serve 之前调用。
func (node *Node) SetNetwork(n string) { node.network = n }
func (node *Node) GetNetwork() string  { return node.network }

// SetDomain 设置节点域名。仅应在 Serve 之前调用，运行期间修改会破坏路由身份。
func (node *Node) SetDomain(domain string) { node.domain = domain }
func (node *Node) GetDomain() string       { return node.domain }

// SetIP 设置节点虚拟 IP。仅应在 Serve 之前调用，运行期间修改会破坏路由身份。
func (node *Node) SetIP(ip uint16) { node.ip = ip }
func (node *Node) GetIP() uint16   { return node.ip }

// SetLogger 设置节点日志器。各组件经 host 指针惰性读取，调用后即对全组件生效。
// 仅应在 Serve 之前调用。
func (node *Node) SetLogger(l *slog.Logger) {
	if l != nil {
		node.logger = l
	}
}
func (node *Node) GetReadWriteSize() (read, written int64) {
	return node.readDataSize.Load(), node.writtenDataSize.Load()
}

func (node *Node) GetInfo() *NodeInfo {
	read, written := node.GetReadWriteSize()
	return &NodeInfo{
		Domain:       node.GetDomain(),
		IP:           node.GetIP(),
		Network:      node.GetNetwork(),
		Uptime:       int64(time.Since(node.startedAt).Seconds()),
		BytesRead:    read,
		BytesWritten: written,
	}
}

func (node *Node) GetListeners() []ListenerInfo {
	listeners := node.listenHub.getActiveListeners()
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

func (node *Node) getWindowSize() int32 {
	if node.flowConfig.MaxWindowSize > 0 {
		return node.flowConfig.MaxWindowSize
	}
	if node.flowConfig.Bandwidth > 0 && node.flowConfig.RTT > 0 {
		bdp := node.flowConfig.Bandwidth * int64(node.flowConfig.RTT) / int64(time.Second)
		return int32(bdp)
	}
	return 0 // uses default in stream package
}

//
// 面向用户的转发方法：底层能力由 listenHub/dialer/pinger/streamHub 提供
//

// Listen 在虚拟端口上监听，返回标准 net.Listener。
// Accept 得到的连接是 *stream.Stream，实现了 net.Conn。
func (node *Node) Listen(port uint16) (net.Listener, error) {
	return node.listenHub.Listen(port)
}

// Dial 通过 "domain:port" 或 "ip:port" 地址创建虚拟流。
// "local"/"localhost" 表示本节点。
func (node *Node) Dial(addr string) (*stream.Stream, error) {
	return node.dialer.Dial(addr)
}

// DialDomain 通过域名创建虚拟流。
func (node *Node) DialDomain(domain string, port uint16) (*stream.Stream, error) {
	return node.dialer.DialDomain(domain, port)
}

// DialIP 通过虚拟 IP 创建虚拟流。
func (node *Node) DialIP(ip, port uint16) (*stream.Stream, error) {
	return node.dialer.DialIP(ip, port)
}

// SetDialTimeout 设置 Dial 等待应答的超时时间。
func (node *Node) SetDialTimeout(timeout time.Duration) {
	node.dialer.SetDialTimeout(timeout)
}

// PingDomain 对指定节点进行连通性测试并返回 RTT。domain 为空时返回到中转节点的 RTT。
func (node *Node) PingDomain(domain string, timeout time.Duration) (time.Duration, error) {
	return node.pinger.PingDomain(domain, timeout)
}

// GetStreamStates 返回当前所有活跃流的状态快照。
func (node *Node) GetStreamStates() []*stream.State {
	return node.streamHub.GetStreamStates()
}

// GetClosedStates 增量拉取已关闭流的状态快照（环形缓冲区，最多保留最近 1024 条）。
// pos 为上次拉取返回的 nextPos（首次传 0），返回新增的状态与新的 nextPos。
// 写入速度过快导致旧记录被覆盖时，会从最早可用的记录开始返回，调用方可通过
// nextPos 的跳变感知中间存在缺口。
func (node *Node) GetClosedStates(pos int64) ([]*stream.State, int64) {
	return node.streamHub.GetClosedStates(pos)
}

// Serve 启动节点的读循环、分发与心跳，阻塞直到连接断开或 Close 被调用。
func (node *Node) Serve() error {
	err := node.dispatcher.start()
	if err != nil {
		return err
	}
	defer node.Close()

	go node.dispatcher.processCmdChan()
	go node.dispatcher.processDataChan()

	ticker := time.NewTicker(node.heartbeat.interval)
	defer ticker.Stop()
	go node.heartbeat.run(ticker, node.done, func() { node.Close() })

	return node.readLoop()
}

func (node *Node) Close() error {
	node.onceClose.Do(func() {
		// 1. 广播关闭信号，通知 Heartbeat 等 goroutine 退出
		close(node.done)

		// 2. 停止 Dispatcher，不再接受新的 dispatch
		node.dispatcher.stop()

		// 3. 关闭所有 Listener，解除阻塞在 Accept() 上的 goroutine
		node.listenHub.closeAllListeners()

		// 4. 关闭所有活跃 Stream，释放本地资源（不走网络协商）
		node.streamHub.closeAllStreams()

		// 5. 关闭底层连接，使 readLoop 退出
		if node.pconn != nil {
			node.pconn.Close()
		}
	})
	return nil
}

// WriteBuffer 实现 packet.Writer，供 stream 层及包内组件写入协议包。
// DistIP 为本机或未设置时包会被就地分发，否则写入底层连接。
// 该方法属于协议内部实现，业务代码不应直接调用。
func (node *Node) WriteBuffer(pbuf *packet.Buffer) error {
	if pbuf.DistIP() == 0 || pbuf.DistIP() == node.ip {
		node.dispatcher.dispatch(pbuf)
		return nil
	}
	if node.pconn == nil {
		return ErrWriterIsNil
	}

	node.heartbeat.Touch()
	node.writtenDataSize.Add(int64(pbuf.PayloadSize()))

	return node.pconn.WriteBuffer(pbuf)
}

func (node *Node) readLoop() error {
	for {
		node.pconn.SetReadTimeout(time.Minute * 15)
		pbuf, err := node.pconn.ReadBuffer()
		if err != nil {
			return err
		}

		node.readDataSize.Add(int64(pbuf.PayloadSize()))

		err = node.dispatcher.dispatch(pbuf)
		if err != nil {
			return err
		}
	}
}
