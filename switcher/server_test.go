package switcher

import (
	"errors"
	"log"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/link"
	"github.com/net-agent/flex/v3/node"
	"github.com/net-agent/flex/v3/packet"
)

func TestServerRun(t *testing.T) {
	pswd := "testpswd"
	s := NewServer(pswd, nil, nil)
	s.Close() // 提高代码覆盖度

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Errorf("listen failed. err=%v\n", err)
		return
	}
	addr := l.Addr().String()
	go s.Serve(l)
	go s.Serve(l) // 提高代码覆盖度

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Errorf("dial failed. err=%v\n", err)
		return
	}
	pc := packet.NewWithConn(c)
	lnk, _ := testDialLink(t, pc, "test", pswd)
	_ = lnk

	s.Close()
}

func TestServeConn(t *testing.T) {
	pswd := "testpswd"
	s := NewServer(pswd, nil, nil)
	pc1, pc2 := packet.Pipe()

	// 模拟客户端请求
	go func() {
		d := link.NewDialer(link.DialConfig{PSK: pswd, Domain: "test"})
		_, _, _ = d.Dial(linkTransport(pc1))
		pc1.Close()
	}()

	s.ServeConn(pc2)
}

// 模拟domain重复的场景
func TestHandlePCErr_DomainExist(t *testing.T) {
	pswd := "testpswd"
	s := NewServer(pswd, nil, nil)
	s.OnContextStart = func(ctx *Context) {
		log.Printf("pbuf loop start, domain='%v' ip='%v'", ctx.Domain, ctx.IP)
	}
	s.OnContextStop = func(ctx *Context, duration time.Duration) {
		log.Printf("pbuf loop stopped, dur=%v\n", duration)
	}

	pc1, pc2 := packet.Pipe()
	go s.ServeConn(pc2)

	// 先正常接入一个 domain=test 的连接。
	lnk1, ip := testDialLink(t, pc1, "test", pswd)
	n := node.NewFromLink(lnk1)
	n.SetIP(ip)
	n.SetDomain("test")
	go n.Serve()
	defer n.Close()

	// 重复接入同一个 domain，attach 的存活探测会成功，随后服务端关闭连接。
	pc3, pc4 := packet.Pipe()
	go s.ServeConn(pc4)
	dialer := link.NewDialer(link.DialConfig{PSK: pswd, Domain: "test"})
	if _, _, err := dialer.Dial(linkTransport(pc3)); err == nil {
		t.Error("unexpected nil err for duplicate domain")
	}
}

// 模拟密码错误的场景
func TestHandlePCErr_Password(t *testing.T) {
	pswd := "testpswd"
	s := NewServer(pswd, nil, nil)
	pc1, pc2 := packet.Pipe()

	serverErr := make(chan error, 1)
	go func() { serverErr <- s.ServeConn(pc2) }()

	dialer := link.NewDialer(link.DialConfig{PSK: pswd + "_badpswd", Domain: "test"})
	if _, _, err := dialer.Dial(linkTransport(pc1)); err == nil {
		t.Error("expected dial error for bad password")
	}
	if err := <-serverErr; err == nil {
		t.Error("expected server handshake error")
	}
}

// 模拟服务端在应答之前连接断开的情况
func TestHandlePCErr_WriteResponse(t *testing.T) {
	pswd := "testpswd"
	s := NewServer(pswd, nil, nil)
	pc1, pc2 := packet.Pipe()

	serverErr := make(chan error, 1)
	go func() { serverErr <- s.ServeConn(pc2) }()

	dialer := link.NewDialer(link.DialConfig{PSK: pswd, Domain: "test"})
	tr := &closeAfterWriteTransport{Transport: linkTransport(pc1)}
	_, _, _ = dialer.Dial(tr)

	if err := <-serverErr; !errors.Is(err, errHandlePCWriteFailed) {
		t.Fatalf("unexpected err=%v", err)
	}
}

type closeAfterWriteTransport struct {
	link.Transport
	once sync.Once
}

func (c *closeAfterWriteTransport) WriteFrames(frames []link.FrameBytes) error {
	err := c.Transport.WriteFrames(frames)
	c.once.Do(func() { _ = c.Transport.Close() })
	return err
}

func TestServerInfo(t *testing.T) {
	server := NewServer("test-pwd", nil, nil)

	stats := server.GetStats()
	if stats == nil {
		t.Fatal("expected stats to be not nil")
	}
	if stats.UptimeSeconds < 0 {
		t.Errorf("expected non-negative uptime, got %d", stats.UptimeSeconds)
	}

	// 模拟已运行一段时间，uptime 应随之增长
	server.startedAt = time.Now().Add(-2 * time.Second)
	if stats := server.GetStats(); stats.UptimeSeconds < 2 {
		t.Errorf("expected uptime >= 2, got %d", stats.UptimeSeconds)
	}

	clients := server.GetClients()
	if len(clients) != 0 {
		t.Errorf("expected 0 clients, got %d", len(clients))
	}
}

func TestGetClientsWithData(t *testing.T) {
	s, _, _ := initTestEnv(t, "test1", "test2")

	clients := s.GetClients()
	if len(clients) != 2 {
		t.Fatalf("expected 2 clients, got %d", len(clients))
	}

	stats := s.GetStats()
	if stats.ActiveConnections != 2 {
		t.Errorf("expected 2 active connections, got %d", stats.ActiveConnections)
	}
	if stats.TotalContexts < 2 {
		t.Errorf("expected TotalContexts >= 2, got %d", stats.TotalContexts)
	}

	for _, c := range clients {
		if c.Domain == "" {
			t.Error("expected non-empty domain")
		}
		if c.IP == 0 {
			t.Error("expected non-zero IP")
		}
	}
}

// Server.Close 应释放所有活跃连接，registry 随各 ServeConn 退出而清空
func TestServerCloseReleasesClients(t *testing.T) {
	s, node1, node2 := initTestEnv(t, "test1", "test2")
	defer node1.Close()
	defer node2.Close()

	if got := len(s.registry.activeContexts()); got != 2 {
		t.Fatalf("expected 2 active contexts, got %v", got)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(s.registry.activeContexts()) == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("expected registry emptied after Close, got %v", len(s.registry.activeContexts()))
}

// 回归：NewContext 会启动 runForwardLoop，attach 失败/应答写失败路径
// 若直接 return 而不 release，每次失败都会永久泄漏一个 goroutine 与 Context。
func TestServeConn_NoGoroutineLeakOnFailure(t *testing.T) {
	pswd := "testpswd"
	s := NewServer(pswd, nil, nil)
	defer s.Close()

	dialer := link.NewDialer(link.DialConfig{PSK: pswd, Domain: "leak-probe"})
	runOnce := func() {
		pc1, pc2 := packet.Pipe()
		done := make(chan struct{})
		go func() {
			defer close(done)
			tr := &closeAfterWriteTransport{Transport: linkTransport(pc1)}
			_, _, _ = dialer.Dial(tr)
		}()
		_ = s.ServeConn(pc2)
		<-done
	}

	// 预热一轮，排除运行时内部 goroutine 的首次分配
	runOnce()
	time.Sleep(50 * time.Millisecond)
	base := runtime.NumGoroutine()

	const rounds = 30
	for i := 0; i < rounds; i++ {
		runOnce()
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= base+3 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("goroutine leak: base=%d now=%d, failed rounds=%d", base, runtime.NumGoroutine(), rounds)
}
