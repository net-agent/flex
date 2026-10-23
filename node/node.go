package node

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v3/internal/idpool"
	"github.com/net-agent/flex/v3/internal/sched"
	"github.com/net-agent/flex/v3/link"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/stream"
	"github.com/net-agent/flex/v3/wire"
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
	// pconn is retained for compatibility/tests (the public constructors still
	// accept packet.Conn). All I/O goes through lnk.
	pconn packet.Conn
	lnk   *link.Link

	dispatcher Dispatcher
	heartbeat  Heartbeat
	listenHub  ListenHub
	dialer     Dialer
	pinger     Pinger
	watcher    Watcher
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
	return newWithConfig(conn, portMin, portMax, heartbeatInterval, sched.DefaultSchedulerConfig())
}

// newWithConfig is the internal constructor used by Session, which carries a
// scheduler configuration across reconnects.
func newWithConfig(conn packet.Conn, portMin, portMax uint16, heartbeatInterval time.Duration, cfg sched.SchedulerConfig) (*Node, error) {
	portm, err := idpool.New(portMin, portMax)
	if err != nil {
		return nil, err
	}
	var lnk *link.Link
	if conn != nil {
		var t link.Transport
		if lt, ok := conn.(link.Transport); ok {
			t = lt
		} else {
			t = link.NewPacketTransport(conn)
		}
		lnk = link.NewDirectWithConfig(t, link.Config{Scheduler: cfg, LegacyWire: true})
	}
	node := &Node{
		pconn:     conn,
		lnk:       lnk,
		done:      make(chan struct{}),
		logger:    slog.Default(),
		startedAt: time.Now(),
	}

	node.listenHub.init(node, portm)
	node.dialer.init(node, portm)
	node.pinger.init(node)
	node.watcher.init(node)
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

// effectiveWindowSize 返回本端实际使用的流控窗口：未配置时取默认值，
// 并钳制到协议硬上限。Dial 请求必须携带该确定值，而不是 0——
// 否则双方对"0 的含义"理解不一致（本地默认 vs 对端默认）会导致窗口不对称，
// 接收端缓冲上限小于发送端窗口时会误触发溢出保护。
func (node *Node) effectiveWindowSize() int32 {
	w := node.getWindowSize()
	if w <= 0 {
		w = stream.DefaultWindowSize
	}
	if w > stream.MaxWindowSize {
		w = stream.MaxWindowSize
	}
	return w
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

// SetDetachRetention 设置流完全关闭后的滞留时长：滞留期内流仍可按 SID 寻址，
// 用于消化晚到的包；设为 0 表示立即清理。仅应在 Serve 之前调用。
// 滞留期只影响资源清理与 closedStates 历史写入时机，
// 不影响 Trace.StreamClosed 的触发时机（detach 时即触发）。
func (node *Node) SetDetachRetention(d time.Duration) {
	node.streamHub.detachRetention = d
}

// PingDomain 对指定节点进行连通性测试并返回 RTT。domain 为空时返回到中转节点的 RTT。
func (node *Node) PingDomain(domain string, timeout time.Duration) (time.Duration, error) {
	return node.pinger.PingDomain(domain, timeout)
}

// Watch 订阅一组 named node 的上下线状态，返回这些节点的当前状态快照。
// 订阅生效后，后续状态迁移通过 SetPresenceHandler 注册的回调实时推送；
// 快照与推送在 switcher 侧原子衔接，不存在中间空洞。
// 通知仅为信息告知，协议栈不会对 offline 节点做任何自动动作（如关闭 stream、
// 取消 Dial），需要 fail-fast 语义的应用可在回调中自行组合。
func (node *Node) Watch(timeout time.Duration, domains ...string) ([]packet.PresenceState, error) {
	return node.watcher.Watch(timeout, domains...)
}

// Unwatch 取消对一组 named node 的状态订阅。
func (node *Node) Unwatch(timeout time.Duration, domains ...string) error {
	return node.watcher.Unwatch(timeout, domains...)
}

// SetPresenceHandler 设置 presence 事件回调，可在运行期间替换。
// 回调在 dispatcher 的 cmd goroutine 中同步执行，不得阻塞。
func (node *Node) SetPresenceHandler(fn func(packet.PresenceEvent)) {
	node.watcher.SetHandler(fn)
}

// GetPresence 返回指定域名的本地 presence 状态副本。
// 副本由 Watch 快照与后续事件增量维护；未订阅或已 Unwatch 的域名返回 ok=false。
func (node *Node) GetPresence(domain string) (packet.PresenceState, bool) {
	return node.watcher.view.get(domain)
}

// ListPresence 返回本地 presence 状态副本的全集。
func (node *Node) ListPresence() []packet.PresenceState {
	return node.watcher.view.list()
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

// CloseStream 按 State.Index 关闭一条活跃流（手动关闭，CloseReason 记为 "manual"）。
// 找不到返回 false。关闭通过 CloseRead+CloseWrite 完成，走正常协议关闭流程。
func (node *Node) CloseStream(index int32) bool {
	return node.streamHub.closeStreamByIndex(index)
}

// Serve 启动节点的读循环、分发与心跳，阻塞直到连接断开或 Close 被调用。
//
// 读循环直接处理来自网络的不可信字节，必须对输入驱动的 panic 兜底，
// 否则一条畸形连接即可打崩进程。recover 只覆盖 Serve 所在的 goroutine
// （读循环与调用栈上的 handler）；dispatcher/heartbeat 等派生 goroutine
// 中的 panic 不在覆盖范围内，仍应作为 bug 暴露。
func (node *Node) Serve() (err error) {
	if err = node.dispatcher.start(); err != nil {
		return err
	}
	defer node.Close()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("node: recovered from panic: %v", r)
			node.logger.Error("panic recovered in Node.Serve", "panic", r, "stack", string(debug.Stack()))
		}
	}()

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
		if node.lnk != nil {
			_ = node.lnk.Close()
		} else if node.pconn != nil {
			_ = node.pconn.Close()
		}
	})
	return nil
}

// WriteBuffer implements packet.Writer for compatibility with existing
// callers. New code should use SendFrame. It copies the payload, so the caller
// may PutBuffer immediately after return.
func (node *Node) WriteBuffer(pbuf *packet.Buffer) error {
	if pbuf == nil {
		return nil
	}
	cmd := wire.Cmd(pbuf.Cmd() &^ 0x80)
	class, ok := wire.ClassOf(cmd)
	if !ok {
		return fmt.Errorf("node: unknown command 0x%02x", byte(cmd))
	}
	var payload []byte
	var ackSize uint16
	if cmd == wire.Cmd(wire.AckPushStreamData) {
		ackSize = pbuf.DataACKSize()
	} else {
		payload = append([]byte(nil), pbuf.Payload...)
	}
	f, err := wire.NewFrame(
		class, cmd,
		wire.Addr{IP: pbuf.SrcIP(), Port: pbuf.SrcPort()},
		wire.Addr{IP: pbuf.DistIP(), Port: pbuf.DistPort()},
		payload, ackSize,
	)
	if err != nil {
		return err
	}
	return node.sendFrameOwned(f)
}

// SendFrame consumes f and routes it: frames addressed to this node go to the
// local dispatcher, everything else goes to the link. The payload is copied so
// callers may reuse their buffer as soon as SendFrame returns. It is the
// stream.Endpoint implementation.
func (node *Node) SendFrame(f *wire.Frame) error {
	if f == nil {
		return nil
	}
	payload := append([]byte(nil), f.Payload()...)
	nf, err := wire.NewFrame(f.Class(), f.Cmd(), f.Src(), f.Dst(), payload, f.AckSize())
	f.Release()
	if err != nil {
		return err
	}
	return node.sendFrameOwned(nf)
}

// sendFrameOwned takes ownership of f and does not copy its payload.
func (node *Node) sendFrameOwned(f *wire.Frame) error {
	if f.Dst().IP == 0 || f.Dst().IP == node.ip {
		// Local dispatch keeps the historical fire-and-forget semantics: a
		// stopped node drops the frame; callers rely on their own timeouts.
		_ = node.dispatcher.dispatchFrame(f)
		return nil
	}
	if node.lnk == nil {
		f.Release()
		return ErrWriterIsNil
	}
	node.heartbeat.Touch()
	node.writtenDataSize.Add(int64(f.PayloadSize()))
	if f.Class() == wire.ClassControl || f.Class() == wire.ClassHandshake {
		// Close/AckClose share the per-stream queue with data; sending them
		// synchronously would let the close overtake queued data.
		switch f.Cmd() {
		case wire.Cmd(wire.CmdCloseStream), wire.Cmd(wire.AckCloseStream):
			return node.lnk.SendFrame(f)
		default:
			return node.lnk.SendFrameSync(f)
		}
	}
	return node.lnk.SendFrame(f)
}

func (node *Node) readLoop() error {
	if node.lnk == nil {
		return ErrWriterIsNil
	}
	for {
		_ = node.lnk.SetReadTimeout(time.Minute * 15)
		f, err := node.lnk.ReadFrame()
		if err != nil {
			return err
		}

		node.readDataSize.Add(int64(f.PayloadSize()))

		if err := node.dispatcher.dispatchFrame(f); err != nil {
			return err
		}
	}
}
