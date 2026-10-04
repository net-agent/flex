package switcher

import (
	"log/slog"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/internal/admit"
	"github.com/net-agent/flex/v3/node"
	"github.com/net-agent/flex/v3/packet"
)

func watchEventCollector(n *node.Node) chan packet.PresenceEvent {
	ch := make(chan packet.PresenceEvent, 16)
	n.SetPresenceHandler(func(ev packet.PresenceEvent) { ch <- ev })
	return ch
}

func waitPresenceEvent(t *testing.T, ch chan packet.PresenceEvent) packet.PresenceEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting presence event")
		return packet.PresenceEvent{}
	}
}

func waitNoPresenceEvent(t *testing.T, ch chan packet.PresenceEvent, d time.Duration) {
	t.Helper()
	select {
	case ev := <-ch:
		t.Fatalf("unexpected presence event: %+v", ev)
	case <-time.After(d):
	}
}

// connectAndServe 接入一个完整运行的 node
func connectAndServe(t *testing.T, s *Server, domain string) *node.Node {
	t.Helper()
	pc1, pc2 := packet.Pipe()
	go s.ServeConn(pc2)
	ip, err := admit.Handshake(pc1, domain, "", "testpswd")
	if err != nil {
		t.Fatalf("handshake failed: %v", err)
	}
	n := node.New(pc1)
	n.SetIP(ip)
	n.SetDomain(domain)
	go n.Serve()
	t.Cleanup(func() { n.Close() })
	return n
}

// handshakeOnly 只完成握手接入，后台持续读包但从不应答（模拟 ping 不通的节点）。
// packet.Pipe 是同步管道，必须有 goroutine 持续排空，否则 switcher 的 ping 写入会阻塞。
func handshakeOnly(t *testing.T, s *Server, domain string) (packet.Conn, uint16) {
	t.Helper()
	pc1, pc2 := packet.Pipe()
	go s.ServeConn(pc2)
	ip, err := admit.Handshake(pc1, domain, "", "testpswd")
	if err != nil {
		t.Fatalf("handshake failed: %v", err)
	}
	go func() {
		for {
			pbuf, err := pc1.ReadBuffer()
			if err != nil {
				return
			}
			packet.PutBuffer(pbuf)
		}
	}()
	t.Cleanup(func() { pc1.Close() })
	return pc1, ip
}

func TestPresenceWatchSnapshot(t *testing.T) {
	s, node1, node2 := initTestEnv("test1", "test2")
	defer s.Close()
	defer node1.Close()
	defer node2.Close()

	states, err := node1.Watch(time.Second*3, "test2", "ghost")
	if err != nil {
		t.Fatalf("watch failed: %v", err)
	}
	if len(states) != 2 {
		t.Fatalf("expected 2 states, got %v", len(states))
	}
	if st := states[0]; st.Domain != "test2" || !st.Online || st.IP != node2.GetIP() {
		t.Errorf("unexpected state for test2: %+v (node2 ip=%v)", st, node2.GetIP())
	}
	if st := states[0]; st.Version != 1 {
		t.Errorf("expected version=1 for first attach, got %+v", st)
	}
	if st := states[1]; st.Domain != "ghost" || st.Online || st.IP != 0 || st.Version != 0 {
		t.Errorf("unexpected state for ghost: %+v", st)
	}
}

func TestPresenceNotifyOnlineAndOffline(t *testing.T) {
	s, node1, node2 := initTestEnv("test1", "test2")
	defer s.Close()
	defer node1.Close()
	defer node2.Close()

	ch := watchEventCollector(node1)

	states, err := node1.Watch(time.Second*3, "test3")
	if err != nil {
		t.Fatalf("watch failed: %v", err)
	}
	if len(states) != 1 || states[0].Online {
		t.Fatalf("expected test3 offline in snapshot, got %+v", states)
	}

	// 上线
	n3 := connectAndServe(t, s, "test3")
	ev := waitPresenceEvent(t, ch)
	if !ev.Online || ev.Domain != "test3" || ev.IP != n3.GetIP() {
		t.Errorf("unexpected online event: %+v (n3 ip=%v)", ev, n3.GetIP())
	}

	// 下线
	n3.Close()
	ev = waitPresenceEvent(t, ch)
	if ev.Online || ev.Domain != "test3" || ev.IP != n3.GetIP() {
		t.Errorf("unexpected offline event: %+v", ev)
	}
}

func TestPresenceUnwatch(t *testing.T) {
	s, node1, node2 := initTestEnv("test1", "test2")
	defer s.Close()
	defer node1.Close()

	ch := watchEventCollector(node1)

	if _, err := node1.Watch(time.Second*3, "test2"); err != nil {
		t.Fatalf("watch failed: %v", err)
	}
	if err := node1.Unwatch(time.Second*3, "test2"); err != nil {
		t.Fatalf("unwatch failed: %v", err)
	}

	node2.Close()
	waitNoPresenceEvent(t, ch, time.Millisecond*500)
}

func TestPresenceDomainReplace(t *testing.T) {
	s, node1, node2 := initTestEnv("test1", "test2")
	defer s.Close()
	defer node1.Close()
	defer node2.Close()

	// 先接入一个永不响应的节点（不启动 Serve，无法应答 ping）
	_, oldIP := handshakeOnly(t, s, "test3")

	ch := watchEventCollector(node1)
	states, err := node1.Watch(time.Second*3, "test3")
	if err != nil {
		t.Fatalf("watch failed: %v", err)
	}
	if len(states) != 1 || !states[0].Online || states[0].IP != oldIP {
		t.Fatalf("expected test3 online with ip=%v, got %+v", oldIP, states)
	}

	// 同名节点再次接入：旧节点 ping 不通（约 3s），被替换
	_, newIP := handshakeOnly(t, s, "test3")

	// 应依次收到 offline(old) + online(new)
	ev1 := waitPresenceEvent(t, ch)
	if ev1.Online || ev1.Domain != "test3" || ev1.IP != oldIP {
		t.Errorf("unexpected first event: %+v (old ip=%v)", ev1, oldIP)
	}
	ev2 := waitPresenceEvent(t, ch)
	if !ev2.Online || ev2.Domain != "test3" || ev2.IP != newIP {
		t.Errorf("unexpected second event: %+v (new ip=%v)", ev2, newIP)
	}
	if ev2.Seq <= ev1.Seq {
		t.Errorf("expected increasing seq, got %v then %v", ev1.Seq, ev2.Seq)
	}
	// 域名历经 attach(v1) → offline(v2) → online(v3)
	if ev1.Version != 2 || ev2.Version != 3 {
		t.Errorf("expected versions 2 then 3, got %v then %v", ev1.Version, ev2.Version)
	}
}

// TestPresenceVersionContinuity 验证同一域名多次上下线时 version 连续递增
func TestPresenceVersionContinuity(t *testing.T) {
	s, node1, node2 := initTestEnv("test1", "test2")
	defer s.Close()
	defer node1.Close()
	defer node2.Close()

	ch := watchEventCollector(node1)
	if _, err := node1.Watch(time.Second*3, "test3"); err != nil {
		t.Fatalf("watch failed: %v", err)
	}

	// 上线 v1 → 下线 v2 → 再上线 v3
	n3 := connectAndServe(t, s, "test3")
	if ev := waitPresenceEvent(t, ch); !ev.Online || ev.Version != 1 {
		t.Errorf("expected online v1, got %+v", ev)
	}
	n3.Close()
	if ev := waitPresenceEvent(t, ch); ev.Online || ev.Version != 2 {
		t.Errorf("expected offline v2, got %+v", ev)
	}
	connectAndServe(t, s, "test3")
	if ev := waitPresenceEvent(t, ch); !ev.Online || ev.Version != 3 {
		t.Errorf("expected online v3, got %+v", ev)
	}
}

// TestPresenceCenterVersionRules 直接驱动 presenceCenter，验证 stale 事件不影响 version
func TestPresenceCenterVersionRules(t *testing.T) {
	pc := &presenceCenter{
		events: make(chan presenceMsg, 8),
		done:   make(chan struct{}),
		logger: slog.Default(),
		state:  make(map[string]presenceEntry),
		subs:   make(map[uint16]*presenceSub),
	}

	online := func(domain string, ip uint16) { pc.handleOnline(presenceMsg{domain: domain, ip: ip}) }
	offline := func(domain string, ip uint16) { pc.handleOffline(presenceMsg{domain: domain, ip: ip}) }
	assertState := func(wantVersion uint64, wantOnline bool) {
		t.Helper()
		e := pc.state["a"]
		if e.version != wantVersion || e.online != wantOnline {
			t.Fatalf("expected version=%v online=%v, got %+v", wantVersion, wantOnline, e)
		}
	}

	online("a", 1) // v1 online
	assertState(1, true)

	offline("a", 2) // stale ip：不生效
	assertState(1, true)

	online("a", 1) // 重复 online：不生效
	assertState(1, true)

	offline("a", 1) // v2 offline
	assertState(2, false)

	offline("a", 1) // 已 offline：不生效
	assertState(2, false)

	online("a", 3) // v3 online（新 ip）
	assertState(3, true)

	offline("a", 1) // 旧 ip 的迟到 offline：不生效（replace 防护）
	assertState(3, true)
}

func TestPresenceSubscriberPurge(t *testing.T) {
	s, node1, node2 := initTestEnv("test1", "test2")
	defer s.Close()
	defer node2.Close()

	if _, err := node1.Watch(time.Second*3, "test2"); err != nil {
		t.Fatalf("watch failed: %v", err)
	}
	node1IP := node1.GetIP()

	// node2 先订阅 test1，用于观察 node1 的下线事件
	if _, err := node2.Watch(time.Second*3, "test1"); err != nil {
		t.Fatalf("watch failed: %v", err)
	}
	ch := watchEventCollector(node2)

	// 订阅者断连（switcher 侧 detach 是异步的）
	node1.Close()

	// 第一步同步：收到 offline(test1) 说明 detach 的两个事件
	// [offline(test1), purge(node1IP)] 均已入队
	ev := waitPresenceEvent(t, ch)
	if ev.Online || ev.Domain != "test1" {
		t.Fatalf("unexpected event: %+v", ev)
	}

	// 第二步同步：队列 FIFO，此次 subscribe 排在 purge 之后处理，
	// 收到应答即说明 purge 已完成
	if _, err := node2.Watch(time.Second*3, "test1"); err != nil {
		t.Fatalf("watch failed: %v", err)
	}

	if _, ok := s.registry.presence.subs[node1IP]; ok {
		t.Errorf("expected subscriber ip=%v purged", node1IP)
	}
}

func TestPresenceWatchInvalidDomain(t *testing.T) {
	s, node1, node2 := initTestEnv("test1", "test2")
	defer s.Close()
	defer node1.Close()
	defer node2.Close()

	if _, err := node1.Watch(time.Second*3, "localhost"); err == nil {
		t.Error("expected error for invalid domain, got nil")
	}
}

// TestPresenceCenterSweep 验证离线条目按 TTL 淘汰的边界条件
func TestPresenceCenterSweep(t *testing.T) {
	pc := &presenceCenter{
		events:     make(chan presenceMsg, 8),
		done:       make(chan struct{}),
		logger:     slog.Default(),
		offlineTTL: time.Hour,
		state:      make(map[string]presenceEntry),
		subs:       make(map[uint16]*presenceSub),
		watchers:   make(map[string]map[uint16]struct{}),
	}

	pc.handleOnline(presenceMsg{domain: "a", ip: 1})
	pc.handleOnline(presenceMsg{domain: "b", ip: 2})
	pc.handleOnline(presenceMsg{domain: "c", ip: 3})
	pc.handleOffline(presenceMsg{domain: "a", ip: 1}) // 离线且无订阅者
	pc.handleOffline(presenceMsg{domain: "b", ip: 2}) // 离线但有订阅者
	pc.watchers["b"] = map[uint16]struct{}{9: {}}

	// 未超 TTL：全部保留
	pc.sweep(time.Now())
	if _, ok := pc.state["a"]; !ok {
		t.Fatal("entry a should survive before TTL")
	}

	// a 离线超过 TTL：被淘汰；b（有订阅者）、c（在线）保留
	e := pc.state["a"]
	e.offlineAt = time.Now().Add(-2 * time.Hour)
	pc.state["a"] = e
	pc.sweep(time.Now())

	if _, ok := pc.state["a"]; ok {
		t.Error("expected a evicted after TTL")
	}
	if _, ok := pc.state["b"]; !ok {
		t.Error("watched offline entry b should survive")
	}
	if _, ok := pc.state["c"]; !ok {
		t.Error("online entry c should survive")
	}
}

// TestPresenceCenterPurge 验证订阅者清理同时维护正排与倒排索引
func TestPresenceCenterPurge(t *testing.T) {
	pc := &presenceCenter{
		events:   make(chan presenceMsg, 8),
		done:     make(chan struct{}),
		logger:   slog.Default(),
		state:    make(map[string]presenceEntry),
		subs:     make(map[uint16]*presenceSub),
		watchers: make(map[string]map[uint16]struct{}),
	}
	pc.subs[7] = &presenceSub{domains: map[string]struct{}{"a": {}, "b": {}}}
	pc.watchers["a"] = map[uint16]struct{}{7: {}}
	pc.watchers["b"] = map[uint16]struct{}{7: {}, 8: {}}

	pc.handlePurge(7)

	if _, ok := pc.subs[7]; ok {
		t.Error("expected sub purged")
	}
	if _, ok := pc.watchers["a"]; ok {
		t.Error("expected empty watcher set removed")
	}
	if _, ok := pc.watchers["b"][8]; !ok {
		t.Error("other watchers should survive")
	}

	// 幂等：重复 purge 不 panic
	pc.handlePurge(7)
}
