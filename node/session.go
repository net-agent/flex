package node

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/net-agent/flex/v3/internal/admit"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/stream"
)

var (
	ErrSessionClosed       = errors.New("session closed")
	ErrSessionDisconnected = errors.New("session disconnected")
)

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

	mu        sync.RWMutex
	node      *Node
	listeners map[uint16]*SessionListener
	ready     chan struct{} // closed when node is ready

	trigger   chan struct{} // closed on first Listen/Dial to start connecting
	onceStart sync.Once

	done      chan struct{}
	onceClose sync.Once
	logger    *slog.Logger
}

func NewSession(connector ConnectFunc, cfg SessionConfig) *Session {
	return &Session{
		connector: connector,
		config:    cfg,
		listeners: make(map[uint16]*SessionListener),
		ready:     make(chan struct{}),
		trigger:   make(chan struct{}),
		done:      make(chan struct{}),
		logger:    slog.Default(),
	}
}

func (s *Session) SetLogger(l *slog.Logger) {
	if l != nil {
		s.logger = l
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

func (s *Session) ensureServing() {
	s.onceStart.Do(func() { close(s.trigger) })
}

// Serve 启动重连循环。阻塞直到 Close 被调用。
// 实际连接在首次 Listen 或 Dial 调用时才开始（懒连接）。
func (s *Session) Serve() error {
	// 等待首次使用触发
	select {
	case <-s.trigger:
	case <-s.done:
		return nil
	}

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
			select {
			case <-s.done:
				return nil
			case <-time.After(backoff):
			}
			backoff = min(backoff*2, 30*time.Second)
			continue
		}

		node := New(conn)
		node.SetIP(ip)
		node.SetDomain(s.config.Domain)

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
		close(s.ready) // 唤醒所有等待者
		s.mu.Unlock()

		node.Serve() // 阻塞直到断线

		s.mu.Lock()
		s.node = nil
		s.ready = make(chan struct{}) // 为下一轮重连准备新的 ready channel
		s.mu.Unlock()

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
		if nl, err := s.node.getListenerByPort(port); err == nil {
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
