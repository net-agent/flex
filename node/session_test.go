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

			obfKey := admit.DeriveObfuscateKey(testPassword, req.Nonce, resp.Nonce)
			server := New(packet.NewObfuscatedConn(c2, obfKey))
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

	_, err := n.getListenerByPort(80)
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
	defer st2.Close()
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
	defer st2.Close()
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
