package node

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v3/internal/admit"
	"github.com/net-agent/flex/v3/internal/sched"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/stream"
)

var (
	ErrSessionClosed       = errors.New("session closed")
	ErrSessionDisconnected = errors.New("session disconnected")
)

// SessionState 表示 Session 的连接状态
type SessionState int32

const (
	SessionReady      SessionState = iota // 已创建，Serve 尚未启动
	SessionIdle                           // 已进入 Serve loop，等待首次 Listen/Dial 触发
	SessionConnecting                     // 连接中 / 重连中
	SessionOnline                         // 已连接
	SessionClosed                         // 已关闭
)

func (s SessionState) String() string {
	switch s {
	case SessionReady:
		return "ready"
	case SessionIdle:
		return "idle"
	case SessionConnecting:
		return "connecting"
	case SessionOnline:
		return "online"
	case SessionClosed:
		return "closed"
	default:
		return "unknown"
	}
}

type ConnectFunc func() (packet.Conn, error)

type SessionConfig struct {
	Domain   string
	Password string
	Mac      string
}

// Session 是一个带有断线重连能力的 Node 代理。
// 对外提供与 Node 一致的 Listen/Dial 语义，内部管理 Node 的生命周期和自动重连。
type Session struct {
	connector ConnectFunc
	config    SessionConfig

	enableFairConn atomic.Bool

	mu        sync.RWMutex
	node      *Node
	listeners map[uint16]*SessionListener
	ready     chan struct{} // closed when node is ready

	// presence 订阅意图跨重连存活；view 由快照与事件增量维护，
	// 重连后通过 resubscribePresence 用新快照 reconcile
	presenceMu sync.RWMutex
	watched    map[string]struct{}
	handler    func(packet.PresenceEvent)
	view       PresenceView

	trigger   chan struct{} // closed on first Listen/Dial to start connecting
	onceStart sync.Once

	done      chan struct{}
	onceClose sync.Once
	logger    *slog.Logger
	trace     *Trace

	// 状态管理
	state         atomic.Int32 // SessionState，lock-free 读取
	lastErr       atomic.Value // string，最近一次错误
	reconnects    atomic.Int64 // 累计断线重连次数（Online→Connecting）
	onStateChange func(oldState, newState SessionState)
}

func NewSession(connector ConnectFunc, cfg SessionConfig) *Session {
	s := &Session{
		connector: connector,
		config:    cfg,
		listeners: make(map[uint16]*SessionListener),
		watched:   make(map[string]struct{}),
		ready:     make(chan struct{}),
		trigger:   make(chan struct{}),
		done:      make(chan struct{}),
		logger:    slog.Default(),
	}
	s.enableFairConn.Store(true)
	return s
}

// SetEnableFairConn controls whether the Session wraps its connection with
// sched.FairConn for fair stream scheduling. Default is true (enabled).
// Must be called before Serve.
func (s *Session) SetEnableFairConn(enable bool) {
	s.enableFairConn.Store(enable)
}

// SetLogger 设置 Session 日志器，每次重连新建 Node 时会自动注入。仅应在 Serve 之前调用。
func (s *Session) SetLogger(l *slog.Logger) {
	if l != nil {
		s.logger = l
	}
}

// SetTrace 设置节点级事件钩子，每次重连新建 Node 时会自动注入。仅应在 Serve 之前调用。
func (s *Session) SetTrace(t *Trace) {
	s.trace = t
}

// wireNode 将 Session 级配置集中注入新建的 Node。
// 每次重连都会重建 Node，所有注入配置必须在此统一接线，新增配置项时同步补充。
func (s *Session) wireNode(n *Node, ip uint16) {
	n.SetIP(ip)
	n.SetDomain(s.config.Domain)
	n.SetLogger(s.logger)
	n.SetTrace(s.trace)
	n.SetPresenceHandler(s.dispatchPresence)
}

// GetState 返回当前 Session 状态（lock-free）
func (s *Session) GetState() SessionState {
	return SessionState(s.state.Load())
}

// GetLastErr 返回最近一次错误信息
func (s *Session) GetLastErr() string {
	v := s.lastErr.Load()
	if v == nil {
		return ""
	}
	return v.(string)
}

// GetReconnectCount 返回累计断线次数（Online 后因断线回到 Connecting 的次数）。
// 首次建连失败不计入。
func (s *Session) GetReconnectCount() int64 {
	return s.reconnects.Load()
}

// OnStateChange 注册状态变更回调。必须在 Serve 之前调用。
func (s *Session) OnStateChange(fn func(oldState, newState SessionState)) {
	s.onStateChange = fn
}

// setState 更新状态，仅在状态实际变化时触发回调。
// 如果 err 非 nil，更新 lastErr；如果 err 为 nil，清空 lastErr。
// 必须在 s.mu 锁外调用，避免回调中调用 GetNode() 死锁。
func (s *Session) setState(newState SessionState, err error) {
	if err != nil {
		s.lastErr.Store(err.Error())
	} else {
		s.lastErr.Store("")
	}

	old := SessionState(s.state.Swap(int32(newState)))
	if old != newState && s.onStateChange != nil {
		s.onStateChange(old, newState)
	}
}

// Listen 注册一个端口监听。该监听跨重连存活，Node 重建后自动重新注册。
// 首次调用会触发 Serve 开始连接。
func (s *Session) Listen(port uint16) (net.Listener, error) {
	s.ensureServing()

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.listeners[port]; exists {
		return nil, ErrListenPortIsUsed
	}

	sl := &SessionListener{
		port:    port,
		session: s,
		streams: make(chan *stream.Stream, 32),
		done:    make(chan struct{}),
	}
	s.listeners[port] = sl

	// 如果 Node 已经在运行，立即注册并启动桥接
	if s.node != nil {
		nl, err := s.node.Listen(port)
		if err != nil {
			delete(s.listeners, port)
			return nil, err
		}
		go s.bridge(sl, nl)
	}

	return sl, nil
}

// Dial 通过当前 Node 发起连接。如果当前处于断线状态，立即返回错误。
// 首次调用会触发 Serve 开始连接。
func (s *Session) Dial(addr string) (*stream.Stream, error) {
	s.ensureServing()

	s.mu.RLock()
	n := s.node
	s.mu.RUnlock()
	if n == nil {
		return nil, ErrSessionDisconnected
	}
	return n.Dial(addr)
}

// WaitReady 阻塞等待 Node 就绪（已连接）。可用于在 Dial 前等待重连完成。
func (s *Session) WaitReady(timeout time.Duration) error {
	s.mu.RLock()
	if s.node != nil {
		s.mu.RUnlock()
		return nil
	}
	ready := s.ready
	s.mu.RUnlock()

	select {
	case <-ready:
		return nil
	case <-time.After(timeout):
		return ErrSessionDisconnected
	case <-s.done:
		return ErrSessionClosed
	}
}

// GetNode 返回当前活跃的 Node，可能为 nil。
func (s *Session) GetNode() *Node {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.node
}

// --- presence ---

// Watch 订阅一组域名的上下线事件，返回当前状态快照。
// 订阅意图跨重连存活：断线重连后 Session 自动重订阅并用新快照 reconcile 本地视图。
// 首次调用会触发 Serve 开始连接。
// 离线时调用返回 ErrSessionDisconnected，但意图已记录，重连后仍会生效。
func (s *Session) Watch(timeout time.Duration, domains ...string) ([]packet.PresenceState, error) {
	s.ensureServing()

	if len(domains) == 0 {
		return nil, ErrEmptyWatchDomains
	}
	s.presenceMu.Lock()
	for _, d := range domains {
		s.watched[d] = struct{}{}
	}
	s.presenceMu.Unlock()

	s.mu.RLock()
	n := s.node
	s.mu.RUnlock()
	if n == nil {
		return nil, ErrSessionDisconnected
	}
	states, err := n.Watch(timeout, domains...)
	if err != nil {
		return nil, err
	}
	s.view.applySnapshot(states)
	return states, nil
}

// Unwatch 取消对一组域名的订阅，并将其从本地视图中移除。
// 离线时调用仅删除订阅意图，返回 ErrSessionDisconnected。
func (s *Session) Unwatch(timeout time.Duration, domains ...string) error {
	s.presenceMu.Lock()
	for _, d := range domains {
		delete(s.watched, d)
	}
	s.presenceMu.Unlock()

	s.mu.RLock()
	n := s.node
	s.mu.RUnlock()
	if n == nil {
		return ErrSessionDisconnected
	}
	if err := n.Unwatch(timeout, domains...); err != nil {
		return err
	}
	s.view.remove(domains...)
	return nil
}

// SetPresenceHandler 设置 presence 事件回调，可在运行期间替换。
// 回调在 dispatcher 的 cmd goroutine 中同步执行，不得阻塞。
func (s *Session) SetPresenceHandler(fn func(packet.PresenceEvent)) {
	s.presenceMu.Lock()
	s.handler = fn
	s.presenceMu.Unlock()
}

// GetPresence 返回指定域名的本地 presence 状态副本。
// 副本由 Watch 快照与后续事件增量维护，跨重连存活（重连后用新快照 reconcile）。
func (s *Session) GetPresence(domain string) (packet.PresenceState, bool) {
	return s.view.get(domain)
}

// ListPresence 返回本地 presence 状态副本的全集。
func (s *Session) ListPresence() []packet.PresenceState {
	return s.view.list()
}

// dispatchPresence 接收当前 Node 推送的 presence 事件：
// 先维护 Session 级视图（stale 丢弃、缺口触发重同步），再投递给用户 handler。
func (s *Session) dispatchPresence(ev packet.PresenceEvent) {
	applied, gap := s.view.applyEvent(ev)
	if gap {
		s.logger.Warn("presence event gap detected, resync", "domain", ev.Domain, "version", ev.Version)
		go s.resyncPresence(ev.Domain)
		return
	}
	if !applied {
		return
	}
	s.presenceMu.RLock()
	fn := s.handler
	s.presenceMu.RUnlock()
	if fn != nil {
		fn(ev)
	}
}

// resyncPresence 通过当前 Node 对指定域名做一次 Watch 重同步（自愈兜底）。
func (s *Session) resyncPresence(domain string) {
	s.mu.RLock()
	n := s.node
	s.mu.RUnlock()
	if n == nil {
		return
	}
	states, err := n.Watch(time.Second*5, domain)
	if err != nil {
		s.logger.Warn("presence resync failed", "domain", domain, "error", err)
		return
	}
	s.view.applySnapshot(states)
}

// resubscribePresence 在重连成功后恢复全部订阅意图，
// 并用返回的快照 reconcile Session 级视图。失败仅记日志，不影响重连流程。
// 快照以权威重置语义应用：version 回退只可能来自 switcher 重启或离线条目淘汰，
// 此时新快照就是当前真相，必须覆盖旧视图而不是按版本丢弃。
func (s *Session) resubscribePresence(n *Node) {
	s.presenceMu.RLock()
	domains := make([]string, 0, len(s.watched))
	for d := range s.watched {
		domains = append(domains, d)
	}
	s.presenceMu.RUnlock()
	if len(domains) == 0 {
		return
	}
	states, err := n.Watch(time.Second*5, domains...)
	if err != nil {
		s.logger.Warn("presence resubscribe failed", "domains", domains, "error", err)
		return
	}
	s.view.resetSnapshot(states)
}

func (s *Session) ensureServing() {
	s.onceStart.Do(func() { close(s.trigger) })
}

// Serve 启动重连循环。阻塞直到 Close 被调用。
// 实际连接在首次 Listen 或 Dial 调用时才开始（懒连接）。
//
// 状态流转：
//
//	               ┌─────────────────────────────────────────┐
//	               │                  Close()                │
//	               ▼                                         │
//	NewSession → Ready → [Serve()] → Idle → Connecting → Online
//	                                            ▲   │
//	                                            └───┘  (断线自动重连)
//	                                            │
//	                                          Closed  (Close() 可在任意状态触发)
//
// 各状态说明：
//
//	Ready      — Session 已创建，Serve 尚未启动
//	Idle       — 已进入 Serve loop，等待首次 Listen/Dial 触发（懒连接）
//	Connecting — 连接中或断线重连中
//	Online     — 与服务器成功建立连接，Node 可用
//	Closed     — Session 已永久关闭，不可复用
func (s *Session) Serve() error {
	s.setState(SessionIdle, nil) // ★ 已进入 Serve loop，等待首次触发

	// 等待首次使用触发
	select {
	case <-s.trigger:
	case <-s.done:
		return nil
	}

	s.setState(SessionConnecting, nil) // ★ 触发连接：Idle → Connecting

	backoff := time.Second

	for {
		select {
		case <-s.done:
			return nil
		default:
		}

		conn, err := s.connector()
		if err != nil {
			s.logger.Warn("connect failed", "error", err, "retry_in", backoff)
			s.setState(SessionConnecting, err) // 状态不变，仅更新 lastErr
			select {
			case <-s.done:
				return nil
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}

		ip, err := admit.Handshake(conn, s.config.Domain, s.config.Mac, s.config.Password)
		if err != nil {
			conn.Close()
			s.logger.Warn("handshake failed", "error", err, "retry_in", backoff)
			s.setState(SessionConnecting, err) // 状态不变，仅更新 lastErr
			select {
			case <-s.done:
				return nil
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}

		if s.enableFairConn.Load() {
			s.logger.Info("enabling fair connection scheduling")
			conn = sched.NewFairConn(conn)
		}

		node := New(conn)
		s.wireNode(node, ip)

		backoff = time.Second // 连接成功，重置退避

		s.mu.Lock()
		s.node = node
		for port, sl := range s.listeners {
			nl, err := node.Listen(port)
			if err != nil {
				s.logger.Warn("register listener failed", "port", port, "error", err)
				continue
			}
			go s.bridge(sl, nl)
		}
		ready := s.ready
		s.mu.Unlock()

		s.setState(SessionOnline, nil) // ★ 触发回调：Connecting → Online
		close(ready)                   // 状态就绪后再唤醒等待者，保证 WaitReady 返回时已 Online

		go s.resubscribePresence(node)

		serveErr := node.Serve() // 阻塞直到断线

		s.mu.Lock()
		s.node = nil
		s.ready = make(chan struct{}) // 为下一轮重连准备新的 ready channel
		s.mu.Unlock()

		s.reconnects.Add(1)
		s.setState(SessionConnecting, serveErr) // ★ 触发回调：Online → Connecting

		s.logger.Info("node disconnected, reconnecting...")
	}
}

func (s *Session) Close() error {
	s.onceClose.Do(func() {
		close(s.done)
		s.mu.Lock()
		if s.node != nil {
			s.node.Close()
			s.node = nil
		}
		s.mu.Unlock()
		s.setState(SessionClosed, nil) // ★ 触发回调
	})
	return nil
}

// bridge 将 node.Listener 的 Accept 结果转发到 SessionListener 的 streams channel。
// 当 Node 死亡时，nl.Accept() 返回错误，bridge 自然退出。
func (s *Session) bridge(sl *SessionListener, nl net.Listener) {
	for {
		conn, err := nl.Accept()
		if err != nil {
			return
		}
		st := conn.(*stream.Stream)
		select {
		case sl.streams <- st:
		case <-sl.done:
			st.Close()
			return
		case <-s.done:
			st.Close()
			return
		}
	}
}

func (s *Session) removeListener(port uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.listeners, port)
	if s.node != nil {
		if nl, err := s.node.listenHub.getListenerByPort(port); err == nil {
			nl.Close()
		}
	}
}

// SessionListener 实现 net.Listener，跨 Node 重连存活。
type SessionListener struct {
	port    uint16
	session *Session
	streams chan *stream.Stream
	done    chan struct{}
	once    sync.Once
}

func (sl *SessionListener) Accept() (net.Conn, error) {
	select {
	case s, ok := <-sl.streams:
		if !ok {
			return nil, ErrListenerClosed
		}
		return s, nil
	case <-sl.done:
		return nil, ErrListenerClosed
	case <-sl.session.done:
		return nil, ErrSessionClosed
	}
}

func (sl *SessionListener) Close() error {
	sl.once.Do(func() {
		close(sl.done)
		sl.session.removeListener(sl.port)
	})
	return nil
}

func (sl *SessionListener) Addr() net.Addr  { return sl }
func (sl *SessionListener) Network() string { return "flex" }
func (sl *SessionListener) String() string  { return fmt.Sprintf("session:%d", sl.port) }
