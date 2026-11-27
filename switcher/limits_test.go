package switcher

import (
	"testing"
	"time"

	"github.com/net-agent/flex/v4/link"
	"github.com/net-agent/flex/v4/node"
	"github.com/net-agent/flex/v4/packet"
	"github.com/net-agent/flex/v4/wire"
)

func newLimitedServer(t *testing.T, limits Limits) *Server {
	t.Helper()
	s := NewServer("testpswd", nil, nil)
	s.SetLimits(limits)
	t.Cleanup(func() { s.Close() })
	return s
}

// connectTestNode 通过内存 pipe 完成一次握手并启动 node。
func connectTestNode(t *testing.T, s *Server, domain string) *node.Node {
	t.Helper()
	client, server := packet.Pipe()
	go s.ServeConn(server)

	lnk, ip := testDialLink(t, client, domain, "testpswd")
	n := node.NewFromLink(lnk)
	n.SetDomain(domain)
	n.SetIP(ip)
	go n.Serve()
	t.Cleanup(func() { n.Close() })
	return n
}

func permissiveLimits() Limits {
	return Limits{
		MaxConnections:           100,
		MaxStreamsPerContext:     1000,
		MaxPresenceSubscriptions: 1000,
		HandshakeRatePerIP:       1000,
		HandshakeBurstPerIP:      1000,
	}
}

func TestIPLimiter(t *testing.T) {
	l := newIPLimiter(2, 2) // 桶容量 2，每秒补充 2
	now := time.Unix(1000, 0)
	l.now = func() time.Time { return now }

	first, second := l.allow("10.0.0.1"), l.allow("10.0.0.1")
	if !first || !second {
		t.Fatal("burst of 2 should be allowed")
	}
	if l.allow("10.0.0.1") {
		t.Fatal("third request within the same second must be rejected")
	}
	if !l.allow("10.0.0.2") {
		t.Fatal("limiter must be per-IP")
	}

	now = now.Add(500 * time.Millisecond)
	if !l.allow("10.0.0.1") {
		t.Fatal("one token should be refilled after 0.5s")
	}
	if l.allow("10.0.0.1") {
		t.Fatal("only one token should have been refilled")
	}

	if !l.allow("") {
		t.Fatal("empty address must bypass rate limiting")
	}
}

func TestIPLimiterSweep(t *testing.T) {
	l := newIPLimiter(1, 1)
	now := time.Unix(1000, 0)
	l.now = func() time.Time { return now }
	l.lastSweep = now

	l.allow("10.0.0.1")
	now = now.Add(2 * ipLimiterIdleTTL)
	l.allow("10.0.0.2") // 触发清扫

	l.mu.Lock()
	_, stale := l.buckets["10.0.0.1"]
	n := len(l.buckets)
	l.mu.Unlock()
	if stale || n != 1 {
		t.Fatalf("idle buckets should be swept, buckets=%d stale=%v", n, stale)
	}
}

func TestRouterCheckSource(t *testing.T) {
	s := newLimitedServer(t, permissiveLimits())
	ctx := NewContext(1, nil, "node", "", nil)
	ctx.IP = 7

	for _, tc := range []struct {
		src     uint16
		wantErr bool
	}{
		{7, false},
		{8, true},
		{wire.MaxIP, true},
	} {
		f, err := wire.NewControl(wire.CmdOpenStream, wire.Addr{IP: tc.src, Port: 1000}, wire.Addr{}, nil)
		if err != nil {
			t.Fatal(err)
		}
		err = s.router.checkSource(ctx, f)
		f.Release()
		if (err != nil) != tc.wantErr {
			t.Fatalf("src=%d err=%v wantErr=%v", tc.src, err, tc.wantErr)
		}
	}
}

func TestServerConnectionLimit(t *testing.T) {
	limits := permissiveLimits()
	limits.MaxConnections = 1
	s := newLimitedServer(t, limits)

	client1, server1 := packet.Pipe()
	go s.ServeConn(server1)
	lnk1, _ := testDialLink(t, client1, "first", "testpswd")
	defer lnk1.Close()

	client2, server2 := packet.Pipe()
	go s.ServeConn(server2)
	dialer := link.NewDialer(link.DialConfig{PSK: "testpswd", Domain: "second"})
	if _, _, err := dialer.Dial(linkTransport(client2)); err == nil {
		t.Fatal("second connection must be rejected by connection limit")
	}
}

func TestServerStreamQuota(t *testing.T) {
	limits := permissiveLimits()
	limits.MaxStreamsPerContext = 1
	s := newLimitedServer(t, limits)

	n1 := connectTestNode(t, s, "quota-a")
	n2 := connectTestNode(t, s, "quota-b")

	ln, err := n2.Listen(80)
	if err != nil {
		t.Fatal(err)
	}
	accepted := make(chan interface{ Close() error }, 4)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			// 保持接收端存活，否则流被关闭会让配额计数归零，测试失去意义
			accepted <- c
		}
	}()

	first, err := n1.Dial("quota-b:80")
	if err != nil {
		t.Fatalf("first dial should succeed: %v", err)
	}
	defer first.Close()
	select {
	case c := <-accepted:
		defer c.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("accept timeout")
	}

	if _, err := n1.Dial("quota-b:80"); err == nil {
		t.Fatal("second dial must be rejected by stream quota")
	}
}

func TestServerPresenceQuota(t *testing.T) {
	limits := permissiveLimits()
	limits.MaxPresenceSubscriptions = 2
	s := newLimitedServer(t, limits)

	n1 := connectTestNode(t, s, "quota-watch")

	if _, err := n1.Watch(time.Second, "x1", "x2"); err != nil {
		t.Fatalf("watch within quota should succeed: %v", err)
	}
	if _, err := n1.Watch(time.Second, "x3"); err == nil {
		t.Fatal("watch beyond quota must be rejected")
	}
}
