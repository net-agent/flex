// Session 单元测试方案
//
// 测试策略：
//   - 使用 newTestConnector 创建可控的 connector，每次调用返回一对通过 packet.Pipe 互联的 packet.Conn
//   - server 端在 goroutine 中完成 admit.Accept 握手并创建 Node
//   - 使用 fakeListener 直接测试 bridge 内部的 select 分支（sl.done / s.done）
//   - 通过关闭 server 端 Node 触发断线，验证自动重连和 Listener 重新注册
//
// 覆盖目标（按函数）：
//   NewSession       — 基本创建
//   SetLogger        — nil 不变 / 非 nil 替换
//   GetNode          — nil / 非 nil
//   Listen           — Serve 前注册 | 端口重复 | 运行中注册+桥接 | 运行中 node.Listen 失败
//   Dial             — node 为 nil 返回错误 | 正常委托
//   WaitReady        — 已就绪 | 超时 | Session 已关闭 | 等待后就绪
//   Serve            — 启动前已关闭 | connector 失败+backoff 中 Close | 断线重连
//   Close            — 有活跃 Node | 无 Node | 幂等
//   bridge           — nl.Accept 错误退出 | 正常转发 | sl.done 退出 | s.done 退出
//   removeListener   — node 为 nil 仅删 map | 有 Node 时同时关闭底层 Listener
//   SessionListener  — Accept 正常 | Listener 关闭唤醒 | Session 关闭唤醒 | channel 关闭
//                      Close 正常+幂等 | 有 Node 时关闭底层 | Addr/Network/String
//
// 未覆盖：Serve 中 backoff 翻倍逻辑（需等待 ≥1s，性价比低）

package node

import (
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"net"

	"github.com/net-agent/flex/v3/internal/admit"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/stream"
	"github.com/stretchr/testify/assert"
)

const testPassword = "testpass"

func testSessionConfig() SessionConfig {
	return SessionConfig{
		Domain:   "testclient",
		Password: testPassword,
		Mac:      "testmac",
	}
}

// newTestConnector 创建一个测试用的 connector，每次调用返回 packet.Conn。
// server 端在 goroutine 中完成握手并创建 Node。
// getServers 会等待所有 pending 的 server 创建完成后返回。
func newTestConnector() (connector func() (packet.Conn, error), getServers func() []*Node) {
	var mu sync.Mutex
	var servers []*Node
	var wg sync.WaitGroup

	connector = func() (packet.Conn, error) {
		c1, c2 := packet.Pipe()
		wg.Add(1)
		go func() {
			defer wg.Done()
			req, err := admit.Accept(c2, testPassword)
			if err != nil {
				c2.Close()
				return
			}
			resp := admit.NewOKResponse(1)
			if err := resp.WriteTo(c2, testPassword); err != nil {
				c2.Close()
				return
			}

			server := New(c2)
			server.SetIP(2)
			server.SetDomain(req.Domain)
			go server.Serve()

			mu.Lock()
			servers = append(servers, server)
			mu.Unlock()
		}()
		return c1, nil
	}

	getServers = func() []*Node {
		wg.Wait()
		mu.Lock()
		defer mu.Unlock()
		cp := make([]*Node, len(servers))
		copy(cp, servers)
		return cp
	}

	return
}

func TestNewSession(t *testing.T) {
	s := NewSession(nil, SessionConfig{})
	assert.NotNil(t, s)
	assert.Nil(t, s.node)
	assert.NotNil(t, s.listeners)
	assert.NotNil(t, s.ready)
	assert.NotNil(t, s.done)
}

func TestSessionSetLogger(t *testing.T) {
	s := NewSession(nil, SessionConfig{})
	original := s.logger

	// nil 不应改变 logger
	s.SetLogger(nil)
	assert.Equal(t, original, s.logger)

	// 非 nil 应替换
	l := slog.Default()
	s.SetLogger(l)
	assert.Equal(t, l, s.logger)
}

func TestSessionGetNode(t *testing.T) {
	s := NewSession(nil, SessionConfig{})
	assert.Nil(t, s.GetNode())

	n := New(nil)
	s.node = n
	assert.Equal(t, n, s.GetNode())
}

// --- Listen ---

func TestSessionListenBeforeServe(t *testing.T) {
	s := NewSession(nil, SessionConfig{})

	sl, err := s.Listen(80)
	assert.Nil(t, err)
	assert.NotNil(t, sl)
	assert.Len(t, s.listeners, 1)
}

func TestSessionListenDuplicatePort(t *testing.T) {
	s := NewSession(nil, SessionConfig{})

	_, err := s.Listen(80)
	assert.Nil(t, err)

	_, err = s.Listen(80)
	assert.Equal(t, ErrListenPortIsUsed, err)
}

func TestSessionListenWhileRunning(t *testing.T) {
	connector, getServers := newTestConnector()
	s := NewSession(connector, testSessionConfig())

	s.ensureServing()
	go s.Serve()
	defer s.Close()

	assert.Nil(t, s.WaitReady(time.Second))

	// 在运行中注册 Listener
	sl, err := s.Listen(80)
	assert.Nil(t, err)

	// 从 server 端 dial 到 client 的 80 端口，验证桥接生效
	servers := getServers()
	st, err := servers[0].DialIP(1, 80)
	assert.Nil(t, err)

	conn, err := sl.Accept()
	assert.Nil(t, err)
	assert.NotNil(t, conn)

	conn.Close()
	st.Close()
}

// --- Dial ---

func TestSessionDialDisconnected(t *testing.T) {
	s := NewSession(nil, SessionConfig{})
	_, err := s.Dial("1:80")
	assert.Equal(t, ErrSessionDisconnected, err)
}

func TestSessionDialConnected(t *testing.T) {
	connector, getServers := newTestConnector()
	s := NewSession(connector, testSessionConfig())

	s.ensureServing()
	go s.Serve()
	defer s.Close()

	assert.Nil(t, s.WaitReady(time.Second))

	servers := getServers()
	_, err := servers[0].Listen(80)
	assert.Nil(t, err)

	st, err := s.Dial("2:80")
	assert.Nil(t, err)
	assert.NotNil(t, st)
	st.Close()
}

// --- WaitReady ---

func TestSessionWaitReadyAlreadyReady(t *testing.T) {
	s := NewSession(nil, SessionConfig{})
	s.node = New(nil) // 模拟已连接
	assert.Nil(t, s.WaitReady(time.Millisecond))
}

func TestSessionWaitReadyTimeout(t *testing.T) {
	s := NewSession(nil, SessionConfig{})
	err := s.WaitReady(50 * time.Millisecond)
	assert.Equal(t, ErrSessionDisconnected, err)
}

func TestSessionWaitReadySessionClosed(t *testing.T) {
	s := NewSession(nil, SessionConfig{})
	s.Close()
	err := s.WaitReady(time.Second)
	assert.Equal(t, ErrSessionClosed, err)
}

func TestSessionWaitReadyBecomesReady(t *testing.T) {
	connector, _ := newTestConnector()
	s := NewSession(connector, testSessionConfig())

	s.ensureServing()
	go s.Serve()
	defer s.Close()

	err := s.WaitReady(time.Second)
	assert.Nil(t, err)
	assert.NotNil(t, s.GetNode())
}

// --- Serve ---

func TestSessionServeAlreadyClosed(t *testing.T) {
	s := NewSession(nil, SessionConfig{})
	s.Close()
	err := s.Serve()
	assert.Nil(t, err)
}

func TestSessionServeConnectorFails(t *testing.T) {
	connector := func() (packet.Conn, error) {
		return nil, errors.New("connect failed")
	}
	s := NewSession(connector, testSessionConfig())

	done := make(chan error, 1)
	go func() { done <- s.Serve() }()

	// 触发连接并等待进入 backoff
	s.ensureServing()
	time.Sleep(50 * time.Millisecond)

	// Close 应中断 backoff 等待
	s.Close()

	select {
	case err := <-done:
		assert.Nil(t, err)
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not exit after Close")
	}
}

// --- Reconnect ---

func TestSessionReconnect(t *testing.T) {
	connector, getServers := newTestConnector()
	s := NewSession(connector, testSessionConfig())

	// 先注册 Listener
	sl, err := s.Listen(80)
	assert.Nil(t, err)

	go s.Serve()
	defer s.Close()

	// 等待首次连接
	assert.Nil(t, s.WaitReady(time.Second))
	assert.Equal(t, int64(0), s.GetReconnectCount(), "no reconnect before first disconnect")

	servers := getServers()
	server1 := servers[0]

	// 通过 server1 dial 到 client，验证 Listener 工作
	st1, err := server1.DialIP(1, 80)
	assert.Nil(t, err)
	conn1, err := sl.Accept()
	assert.Nil(t, err)
	assert.NotNil(t, conn1)
	conn1.Close()
	st1.Close()

	// 关闭 server1 触发断线
	server1.Close()

	// 等待重连
	time.Sleep(100 * time.Millisecond)
	assert.Nil(t, s.WaitReady(5*time.Second))

	// 验证重连发生
	servers = getServers()
	assert.GreaterOrEqual(t, len(servers), 2)
	assert.Equal(t, int64(1), s.GetReconnectCount(), "disconnect should be counted")
	server2 := servers[1]

	// 通过 server2 dial，验证 Listener 被重新注册
	st2, err := server2.DialIP(1, 80)
	assert.Nil(t, err)
	conn2, err := sl.Accept()
	assert.Nil(t, err)
	assert.NotNil(t, conn2)
	conn2.Close()
	st2.Close()
}

// wireNode：Session 级 logger/trace 应注入每个新建的 Node，重连后依然生效
func TestSessionWiresLoggerAndTrace(t *testing.T) {
	connector, getServers := newTestConnector()
	s := NewSession(connector, testSessionConfig())

	h := &captureHandler{}
	s.SetLogger(slog.New(h))

	opens := make(chan *stream.State, 8)
	s.SetTrace(&Trace{
		StreamOpen: func(st *stream.State) { opens <- st },
	})
	waitOpen := func(msg string) {
		t.Helper()
		select {
		case <-opens:
		case <-time.After(5 * time.Second):
			t.Fatal(msg)
		}
	}

	sl, err := s.Listen(80)
	assert.Nil(t, err)
	defer sl.Close()

	go s.Serve()
	defer s.Close()

	assert.Nil(t, s.WaitReady(time.Second))

	// 首个 Node：logger/trace 已接线
	n1 := s.GetNode()
	assert.NotNil(t, n1)
	assert.Same(t, s.logger, n1.logger, "session logger should be injected into node")
	assert.NotNil(t, n1.trace, "session trace should be injected into node")

	servers := getServers()
	st1, err := servers[0].DialIP(1, 80)
	assert.Nil(t, err)
	c1, err := sl.Accept()
	assert.Nil(t, err)
	waitOpen("StreamOpen should fire on session-wired node")
	c1.Close()
	st1.Close()

	// 断线重连后：重建的 Node 同样完成接线
	servers[0].Close()
	time.Sleep(100 * time.Millisecond)
	assert.Nil(t, s.WaitReady(5*time.Second))

	n2 := s.GetNode()
	assert.NotNil(t, n2)
	assert.NotSame(t, n1, n2, "reconnect should rebuild node")
	assert.Same(t, s.logger, n2.logger, "session logger should be re-injected after reconnect")
	assert.NotNil(t, n2.trace, "session trace should be re-injected after reconnect")

	servers = getServers()
	assert.GreaterOrEqual(t, len(servers), 2)
	st2, err := servers[1].DialIP(1, 80)
	assert.Nil(t, err)
	c2, err := sl.Accept()
	assert.Nil(t, err)
	waitOpen("StreamOpen should still fire after reconnect")
	c2.Close()
	st2.Close()
}

// --- Close ---

func TestSessionCloseWithActiveNode(t *testing.T) {
	connector, _ := newTestConnector()
	s := NewSession(connector, testSessionConfig())

	done := make(chan error, 1)
	s.ensureServing()
	go func() { done <- s.Serve() }()

	assert.Nil(t, s.WaitReady(time.Second))
	assert.NotNil(t, s.GetNode())

	s.Close()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not exit after Close")
	}

	assert.Nil(t, s.GetNode())
}

func TestSessionCloseNilNode(t *testing.T) {
	s := NewSession(nil, SessionConfig{})
	assert.Nil(t, s.Close())
}

func TestSessionDoubleClose(t *testing.T) {
	s := NewSession(nil, SessionConfig{})
	assert.Nil(t, s.Close())
	assert.Nil(t, s.Close())
}

// --- SessionListener ---

func TestSessionListenerAccept(t *testing.T) {
	connector, getServers := newTestConnector()
	s := NewSession(connector, testSessionConfig())

	sl, _ := s.Listen(80)
	go s.Serve()
	defer s.Close()

	assert.Nil(t, s.WaitReady(time.Second))

	servers := getServers()
	st, err := servers[0].DialIP(1, 80)
	assert.Nil(t, err)

	conn, err := sl.Accept()
	assert.Nil(t, err)
	assert.NotNil(t, conn)
	conn.Close()
	st.Close()
}

func TestSessionListenerAcceptOnListenerClose(t *testing.T) {
	s := NewSession(nil, SessionConfig{})
	sl, _ := s.Listen(80)

	done := make(chan error, 1)
	go func() {
		_, err := sl.Accept()
		done <- err
	}()

	time.Sleep(20 * time.Millisecond)
	sl.Close()

	select {
	case err := <-done:
		assert.Equal(t, ErrListenerClosed, err)
	case <-time.After(time.Second):
		t.Fatal("Accept did not return after listener Close")
	}
}

func TestSessionListenerAcceptOnSessionClose(t *testing.T) {
	s := NewSession(nil, SessionConfig{})
	sl, _ := s.Listen(80)

	done := make(chan error, 1)
	go func() {
		_, err := sl.Accept()
		done <- err
	}()

	time.Sleep(20 * time.Millisecond)
	s.Close()

	select {
	case err := <-done:
		assert.Equal(t, ErrSessionClosed, err)
	case <-time.After(time.Second):
		t.Fatal("Accept did not return after session Close")
	}
}

func TestSessionListenerAcceptChannelClosed(t *testing.T) {
	s := NewSession(nil, SessionConfig{})
	sl := &SessionListener{
		port:    80,
		session: s,
		streams: make(chan *stream.Stream, 1),
		done:    make(chan struct{}),
	}
	close(sl.streams)
	_, err := sl.Accept()
	assert.Equal(t, ErrListenerClosed, err)
}

func TestSessionListenerClose(t *testing.T) {
	s := NewSession(nil, SessionConfig{})
	sl, _ := s.Listen(80)
	assert.Len(t, s.listeners, 1)

	err := sl.Close()
	assert.Nil(t, err)
	assert.Empty(t, s.listeners)
}

func TestSessionListenerDoubleClose(t *testing.T) {
	s := NewSession(nil, SessionConfig{})
	sl, _ := s.Listen(80)

	assert.Nil(t, sl.Close())
	assert.Nil(t, sl.Close())
}

func TestSessionListenerCloseWithActiveNode(t *testing.T) {
	connector, _ := newTestConnector()
	s := NewSession(connector, testSessionConfig())

	sl, _ := s.Listen(80)
	go s.Serve()
	defer s.Close()

	assert.Nil(t, s.WaitReady(time.Second))

	// 关闭 SessionListener 应同时关闭底层 node.Listener
	sl.Close()

	s.mu.RLock()
	n := s.node
	s.mu.RUnlock()

	_, err := n.listenHub.getListenerByPort(80)
	assert.Equal(t, ErrListenerNotFound, err)
}

func TestSessionListenerAddr(t *testing.T) {
	s := NewSession(nil, SessionConfig{})
	sl, _ := s.Listen(80)

	assert.Equal(t, "flex", sl.(*SessionListener).Network())
	assert.Equal(t, "session:80", sl.(*SessionListener).String())
	assert.Equal(t, sl, sl.(*SessionListener).Addr())
}

func TestSessionListenWhileRunningNodeListenFails(t *testing.T) {
	connector, _ := newTestConnector()
	s := NewSession(connector, testSessionConfig())

	s.ensureServing()
	go s.Serve()
	defer s.Close()

	assert.Nil(t, s.WaitReady(time.Second))

	// 先在底层 Node 上占用 port 80
	s.mu.RLock()
	n := s.node
	s.mu.RUnlock()
	_, err := n.Listen(80)
	assert.Nil(t, err)

	// Session.Listen(80) 应失败并清理
	_, err = s.Listen(80)
	assert.Equal(t, ErrListenPortIsUsed, err)
	assert.Empty(t, s.listeners)
}

// --- bridge ---

// fakeListener 用于直接测试 bridge 的各个 select 分支
type fakeListener struct {
	ch   chan net.Conn
	done chan struct{}
}

func (f *fakeListener) Accept() (net.Conn, error) {
	select {
	case c := <-f.ch:
		return c, nil
	case <-f.done:
		return nil, errors.New("closed")
	}
}
func (f *fakeListener) Close() error   { close(f.done); return nil }
func (f *fakeListener) Addr() net.Addr { return nil }

func TestSessionBridgeListenerDone(t *testing.T) {
	s := NewSession(nil, SessionConfig{})
	sl := &SessionListener{
		port:    80,
		session: s,
		streams: make(chan *stream.Stream), // 0 容量，发送必阻塞
		done:    make(chan struct{}),
	}

	fl := &fakeListener{ch: make(chan net.Conn, 1), done: make(chan struct{})}

	bridgeDone := make(chan struct{})
	go func() {
		s.bridge(sl, fl)
		close(bridgeDone)
	}()

	// 先关闭 sl.done，再送入 stream
	close(sl.done)
	st1, st2 := stream.Pipe()
	// st2 在后台响应 CmdClose，避免 st1.Close() 等待 CloseAck 超时
	go func() {
		buf := make([]byte, 1)
		st2.Read(buf)
		st2.CloseWrite()
	}()
	fl.ch <- st1

	select {
	case <-bridgeDone:
	case <-time.After(time.Second):
		t.Fatal("bridge did not exit on sl.done")
	}
}

func TestSessionBridgeSessionDone(t *testing.T) {
	s := NewSession(nil, SessionConfig{})
	sl := &SessionListener{
		port:    80,
		session: s,
		streams: make(chan *stream.Stream), // 0 容量
		done:    make(chan struct{}),
	}

	fl := &fakeListener{ch: make(chan net.Conn, 1), done: make(chan struct{})}

	bridgeDone := make(chan struct{})
	go func() {
		s.bridge(sl, fl)
		close(bridgeDone)
	}()

	// 先关闭 session.done，再送入 stream
	s.Close()
	st1, st2 := stream.Pipe()
	// st2 在后台响应 CmdClose，避免 st1.Close() 等待 CloseAck 超时
	go func() {
		buf := make([]byte, 1)
		st2.Read(buf)
		st2.CloseWrite()
	}()
	fl.ch <- st1

	select {
	case <-bridgeDone:
	case <-time.After(time.Second):
		t.Fatal("bridge did not exit on s.done")
	}
}

// --- removeListener ---

func TestSessionRemoveListenerNilNode(t *testing.T) {
	s := NewSession(nil, SessionConfig{})
	s.Listen(80)
	assert.Len(t, s.listeners, 1)

	s.removeListener(80)
	assert.Empty(t, s.listeners)
}

// --- SessionState ---

func TestSessionStateString(t *testing.T) {
	assert.Equal(t, "ready", SessionReady.String())
	assert.Equal(t, "idle", SessionIdle.String())
	assert.Equal(t, "connecting", SessionConnecting.String())
	assert.Equal(t, "online", SessionOnline.String())
	assert.Equal(t, "closed", SessionClosed.String())
	assert.Equal(t, "unknown", SessionState(99).String())
}

func TestSessionInitialState(t *testing.T) {
	s := NewSession(nil, SessionConfig{})
	assert.Equal(t, SessionReady, s.GetState())
	assert.Equal(t, "", s.GetLastErr())
}

func TestSessionStateConnectingToOnline(t *testing.T) {
	connector, _ := newTestConnector()
	s := NewSession(connector, testSessionConfig())

	var mu sync.Mutex
	var transitions []struct{ old, new_ SessionState }

	s.OnStateChange(func(old, new_ SessionState) {
		mu.Lock()
		transitions = append(transitions, struct{ old, new_ SessionState }{old, new_})
		mu.Unlock()
	})

	s.ensureServing()
	go s.Serve()

	assert.Nil(t, s.WaitReady(time.Second))
	assert.Equal(t, SessionOnline, s.GetState())
	assert.Equal(t, "", s.GetLastErr())

	mu.Lock()
	// Ready→Idle, Idle→Connecting, Connecting→Online
	assert.Len(t, transitions, 3)
	assert.Equal(t, SessionIdle, transitions[1].old)
	assert.Equal(t, SessionOnline, transitions[2].new_)
	mu.Unlock()

	s.Close()
}

func TestSessionStateOnlineToConnectingOnDisconnect(t *testing.T) {
	connector, getServers := newTestConnector()
	s := NewSession(connector, testSessionConfig())

	var mu sync.Mutex
	var transitions []struct{ old, new_ SessionState }

	s.OnStateChange(func(old, new_ SessionState) {
		mu.Lock()
		transitions = append(transitions, struct{ old, new_ SessionState }{old, new_})
		mu.Unlock()
	})

	s.ensureServing()
	go s.Serve()
	defer s.Close()

	assert.Nil(t, s.WaitReady(time.Second))

	// 关闭 server 端触发断线
	servers := getServers()
	servers[0].Close()

	// 等待重连成功
	time.Sleep(100 * time.Millisecond)
	assert.Nil(t, s.WaitReady(5*time.Second))

	mu.Lock()
	// 应至少有 5 次状态转换: ready→idle, idle→connecting, connecting→online, online→connecting, connecting→online
	assert.GreaterOrEqual(t, len(transitions), 5)
	assert.Equal(t, SessionOnline, transitions[2].new_)     // 首次上线
	assert.Equal(t, SessionConnecting, transitions[3].new_) // 断线
	assert.Equal(t, SessionOnline, transitions[4].new_)     // 重连上线
	mu.Unlock()
}

func TestSessionStateCloseCallback(t *testing.T) {
	s := NewSession(nil, SessionConfig{})

	var called bool
	var oldState, newState SessionState

	s.OnStateChange(func(old, new_ SessionState) {
		called = true
		oldState = old
		newState = new_
	})

	s.Close()

	assert.True(t, called)
	assert.Equal(t, SessionReady, oldState)
	assert.Equal(t, SessionClosed, newState)
	assert.Equal(t, SessionClosed, s.GetState())
}

func TestSessionLastErrOnConnectFailure(t *testing.T) {
	connector := func() (packet.Conn, error) {
		return nil, errors.New("dial tcp: connection refused")
	}
	s := NewSession(connector, testSessionConfig())

	s.ensureServing()
	go s.Serve()

	// 等待 connector 被调用
	time.Sleep(100 * time.Millisecond)

	assert.Equal(t, SessionConnecting, s.GetState())
	assert.Equal(t, "dial tcp: connection refused", s.GetLastErr())

	s.Close()
}

func TestSessionLastErrClearedOnOnline(t *testing.T) {
	callCount := 0
	connector, _ := newTestConnector()
	wrappedConnector := func() (packet.Conn, error) {
		callCount++
		if callCount == 1 {
			return nil, errors.New("first attempt fails")
		}
		return connector()
	}
	s := NewSession(wrappedConnector, testSessionConfig())

	s.ensureServing()
	go s.Serve()
	defer s.Close()

	// 等待重连成功
	assert.Nil(t, s.WaitReady(5*time.Second))
	assert.Equal(t, SessionOnline, s.GetState())
	assert.Equal(t, "", s.GetLastErr())
}

func TestSessionNoCallbackWithoutRegistration(t *testing.T) {
	// 没有注册 OnStateChange 不应 panic
	s := NewSession(nil, SessionConfig{})
	s.Close()
	assert.Equal(t, SessionClosed, s.GetState())
}

// --- presence ---

// newPresenceConnector 创建一个 connector，server 侧完成握手后把连接交给测试驱动（fake switcher）
func newPresenceConnector(swCh chan packet.Conn) func() (packet.Conn, error) {
	return func() (packet.Conn, error) {
		c1, c2 := packet.Pipe()
		go func() {
			_, err := admit.Accept(c2, testPassword)
			if err != nil {
				c2.Close()
				return
			}
			resp := admit.NewOKResponse(1)
			if err := resp.WriteTo(c2, testPassword); err != nil {
				c2.Close()
				return
			}
			swCh <- c2
		}()
		return c1, nil
	}
}

// recvSwitcher 等待一条完成握手的新连接
func recvSwitcher(t *testing.T, swCh chan packet.Conn) packet.Conn {
	t.Helper()
	select {
	case sw := <-swCh:
		return sw
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting switcher conn")
		return nil
	}
}

// fakePresenceSwitcher 模拟 switcher 的 presence 行为：
//   - serve 循环应答订阅请求（Add 回当前状态快照，Remove 回空 ACK），
//     容忍建连时刻 resubscribe 与显式 Watch 并发产生的重复订阅（真实 switcher 侧订阅幂等）
//   - 快照始终取当前状态，保证与已推送事件的 version 单调性一致
type fakePresenceSwitcher struct {
	t  *testing.T
	sw packet.Conn

	mu sync.Mutex
	st packet.PresenceState
}

func newFakePresenceSwitcher(t *testing.T, sw packet.Conn, initial packet.PresenceState) *fakePresenceSwitcher {
	f := &fakePresenceSwitcher{t: t, sw: sw, st: initial}
	go f.serve()
	return f
}

func (f *fakePresenceSwitcher) serve() {
	for {
		pbuf, err := f.sw.ReadBuffer()
		if err != nil {
			return
		}
		if pbuf.Cmd() != packet.CmdSubscribePresence {
			packet.PutBuffer(pbuf)
			continue
		}
		req := packet.DecodeSubscribeRequest(pbuf.Payload)
		if req.Op == packet.SubscribeAdd {
			f.mu.Lock()
			states := []packet.PresenceState{f.st}
			f.mu.Unlock()
			writeSubscribeACK(f.t, f.sw, pbuf, packet.SubscribeACK{OK: true, States: states})
		} else {
			writeSubscribeACK(f.t, f.sw, pbuf, packet.SubscribeACK{OK: true})
		}
	}
}

// notify 更新 fake 当前状态并向对端推送事件（之后的订阅快照与事件保持一致）
func (f *fakePresenceSwitcher) notify(distIP uint16, ev packet.PresenceEvent) {
	f.mu.Lock()
	f.st = packet.PresenceState{Domain: ev.Domain, Online: ev.Online, IP: ev.IP, Mac: ev.Mac, Version: ev.Version}
	f.mu.Unlock()
	writePresenceNotify(f.t, f.sw, distIP, ev)
}

func waitSessionView(t *testing.T, s *Session, domain string, version uint64) packet.PresenceState {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if st, ok := s.GetPresence(domain); ok && st.Version == version {
			return st
		}
		time.Sleep(10 * time.Millisecond)
	}
	st, _ := s.GetPresence(domain)
	t.Fatalf("timeout waiting session view %v version=%v, last=%+v", domain, version, st)
	return packet.PresenceState{}
}

func TestSessionWatchWhileDisconnected(t *testing.T) {
	s := NewSession(nil, SessionConfig{})

	// 空域名直接报错，不记录意图
	_, err := s.Watch(time.Second)
	assert.Equal(t, ErrEmptyWatchDomains, err)

	// 离线 Watch：返回错误但意图已记录
	_, err = s.Watch(time.Second, "target")
	assert.Equal(t, ErrSessionDisconnected, err)
	s.presenceMu.RLock()
	_, ok := s.watched["target"]
	s.presenceMu.RUnlock()
	assert.True(t, ok, "watch intent should be recorded while disconnected")

	// 离线 Unwatch：返回错误且意图被移除
	err = s.Unwatch(time.Second, "target")
	assert.Equal(t, ErrSessionDisconnected, err)
	s.presenceMu.RLock()
	_, ok = s.watched["target"]
	s.presenceMu.RUnlock()
	assert.False(t, ok, "watch intent should be removed")
}

func TestSessionWatchAndNotify(t *testing.T) {
	swCh := make(chan packet.Conn, 1)
	s := NewSession(newPresenceConnector(swCh), testSessionConfig())

	s.ensureServing()
	go s.Serve()
	defer s.Close()
	assert.Nil(t, s.WaitReady(time.Second))

	sw := recvSwitcher(t, swCh)
	defer sw.Close()
	fsw := newFakePresenceSwitcher(t, sw, packet.PresenceState{Domain: "target", Online: true, IP: 7, Mac: "m1", Version: 1})

	// Watch：快照进入 Session 级视图
	states, err := s.Watch(time.Second*3, "target")
	assert.Nil(t, err)
	assert.Len(t, states, 1)
	st, ok := s.GetPresence("target")
	assert.True(t, ok)
	assert.True(t, st.Online)
	assert.Equal(t, uint64(1), st.Version)
	assert.Len(t, s.ListPresence(), 1)

	// 事件经 node dispatcher 投递到 Session handler，并增量更新视图
	ch := make(chan packet.PresenceEvent, 2)
	s.SetPresenceHandler(func(ev packet.PresenceEvent) { ch <- ev })
	fsw.notify(1, packet.PresenceEvent{Version: 2, Domain: "target", Online: false, IP: 7, Mac: "m1"})

	select {
	case ev := <-ch:
		assert.Equal(t, "target", ev.Domain)
		assert.False(t, ev.Online)
		assert.Equal(t, uint64(2), ev.Version)
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting session presence event")
	}
	st = waitSessionView(t, s, "target", 2)
	assert.False(t, st.Online)

	// stale 事件（node 级视图丢弃）：Session handler 不应收到（不更新 fake 状态，直接注入）
	writePresenceNotify(t, sw, 1, packet.PresenceEvent{Version: 2, Domain: "target", Online: true, IP: 9})
	select {
	case ev := <-ch:
		t.Fatalf("stale event should not be delivered: %+v", ev)
	case <-time.After(300 * time.Millisecond):
	}

	// Unwatch：视图与意图同步移除
	assert.Nil(t, s.Unwatch(time.Second*3, "target"))
	_, ok = s.GetPresence("target")
	assert.False(t, ok)
	s.presenceMu.RLock()
	assert.Empty(t, s.watched)
	s.presenceMu.RUnlock()
}

// 断线重连后 Session 应自动重订阅，并用新快照 reconcile 视图
func TestSessionPresenceAutoResubscribe(t *testing.T) {
	swCh := make(chan packet.Conn, 4)
	s := NewSession(newPresenceConnector(swCh), testSessionConfig())

	s.ensureServing()
	go s.Serve()
	defer s.Close()
	assert.Nil(t, s.WaitReady(time.Second))

	// 首个连接：Watch 快照 v1 online
	sw1 := recvSwitcher(t, swCh)
	newFakePresenceSwitcher(t, sw1, packet.PresenceState{Domain: "target", Online: true, IP: 7, Mac: "m1", Version: 1})
	_, err := s.Watch(time.Second*3, "target")
	assert.Nil(t, err)
	assert.Equal(t, uint64(1), waitSessionView(t, s, "target", 1).Version)

	// 断开底层连接触发重连
	sw1.Close()

	// 重连后自动重订阅：fake switcher 回快照 v2 offline，视图被 reconcile
	sw2 := recvSwitcher(t, swCh)
	defer sw2.Close()
	newFakePresenceSwitcher(t, sw2, packet.PresenceState{Domain: "target", Online: false, IP: 7, Mac: "m1", Version: 2})

	st := waitSessionView(t, s, "target", 2)
	assert.False(t, st.Online, "view should be reconciled by resubscribe snapshot")
	assert.Equal(t, int64(1), s.GetReconnectCount())
}

// 直接驱动 dispatchPresence：未见过的事件投递，stale 丢弃，gap 不投递且触发重同步（node 为 nil 时静默返回）
func TestSessionDispatchPresence(t *testing.T) {
	s := NewSession(nil, SessionConfig{})
	ch := make(chan packet.PresenceEvent, 4)
	s.SetPresenceHandler(func(ev packet.PresenceEvent) { ch <- ev })

	// 未见过的域名直接应用并投递
	s.dispatchPresence(packet.PresenceEvent{Domain: "a", Version: 3, Online: true, IP: 7})
	select {
	case ev := <-ch:
		assert.Equal(t, uint64(3), ev.Version)
	case <-time.After(time.Second):
		t.Fatal("event not delivered")
	}

	// stale：不投递、不污染视图
	s.dispatchPresence(packet.PresenceEvent{Domain: "a", Version: 2, Online: false})
	// gap：不投递，触发重同步（node 为 nil，静默返回）
	s.dispatchPresence(packet.PresenceEvent{Domain: "a", Version: 5, Online: false})

	select {
	case ev := <-ch:
		t.Fatalf("stale/gap event should not be delivered: %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}
	st, ok := s.GetPresence("a")
	assert.True(t, ok)
	assert.Equal(t, uint64(3), st.Version)
	assert.True(t, st.Online)
}
