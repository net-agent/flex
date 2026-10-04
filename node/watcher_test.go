package node

import (
	"strings"
	"testing"
	"time"

	"github.com/net-agent/flex/v3/packet"
)

// initWatcherTestNode 启动一个 node，返回充当 fake switcher 的对端连接
func initWatcherTestNode(t *testing.T, domain string) (*Node, packet.Conn) {
	t.Helper()
	pc1, pc2 := packet.Pipe()
	n := New(pc1)
	n.SetIP(1)
	n.SetDomain(domain)
	go n.Serve()
	t.Cleanup(func() { n.Close() })
	return n, pc2
}

// readSubscribeRequest 从 fake switcher 侧读出一个订阅请求并校验
func readSubscribeRequest(t *testing.T, sw packet.Conn) (*packet.Buffer, packet.SubscribeRequest) {
	t.Helper()
	pbuf, err := sw.ReadBuffer()
	if err != nil {
		t.Fatalf("read subscribe request failed: %v", err)
	}
	if pbuf.Cmd() != packet.CmdSubscribePresence {
		t.Fatalf("expected cmd=CmdSubscribePresence, got %v", pbuf.CmdName())
	}
	return pbuf, packet.DecodeSubscribeRequest(pbuf.Payload)
}

func writeSubscribeACK(t *testing.T, sw packet.Conn, req *packet.Buffer, ack packet.SubscribeACK) {
	t.Helper()
	resp := packet.NewBufferWithCmd(packet.AckSubscribePresence)
	resp.SetSrc(packet.SwitcherIP, 0)
	resp.SetDist(req.SrcIP(), req.SrcPort())
	if err := resp.SetPayload(ack.Encode()); err != nil {
		t.Fatalf("set ack payload failed: %v", err)
	}
	if err := sw.WriteBuffer(resp); err != nil {
		t.Fatalf("write ack failed: %v", err)
	}
}

func TestWatcherWatch(t *testing.T) {
	n, sw := initWatcherTestNode(t, "watcher-node")
	defer sw.Close()

	go func() {
		pbuf, req := readSubscribeRequest(t, sw)
		if req.Op != packet.SubscribeAdd {
			t.Errorf("expected op=SubscribeAdd, got %v", req.Op)
		}
		if len(req.Domains) != 1 || req.Domains[0] != "target" {
			t.Errorf("unexpected domains: %v", req.Domains)
		}
		writeSubscribeACK(t, sw, pbuf, packet.SubscribeACK{OK: true, States: []packet.PresenceState{
			{Domain: "target", Online: true, IP: 7, Mac: "m1"},
		}})
	}()

	states, err := n.Watch(time.Second*3, "target")
	if err != nil {
		t.Fatalf("watch failed: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("expected 1 state, got %v", len(states))
	}
	st := states[0]
	if st.Domain != "target" || !st.Online || st.IP != 7 || st.Mac != "m1" {
		t.Errorf("unexpected state: %+v", st)
	}
}

func TestWatcherWatchRejected(t *testing.T) {
	n, sw := initWatcherTestNode(t, "watcher-node")
	defer sw.Close()

	go func() {
		pbuf, _ := readSubscribeRequest(t, sw)
		writeSubscribeACK(t, sw, pbuf, packet.SubscribeACK{OK: false, Error: "invalid domain"})
	}()

	_, err := n.Watch(time.Second*3, "bad-domain")
	if err == nil || !strings.Contains(err.Error(), "invalid domain") {
		t.Errorf("expected 'invalid domain' error, got %v", err)
	}
}

func TestWatcherWatchTimeout(t *testing.T) {
	n, sw := initWatcherTestNode(t, "watcher-node")
	defer sw.Close()

	go func() {
		pbuf, _ := readSubscribeRequest(t, sw)
		packet.PutBuffer(pbuf) // 不应答，触发超时
	}()

	_, err := n.Watch(time.Millisecond*100, "target")
	if err != ErrWatchTimeout {
		t.Errorf("expected ErrWatchTimeout, got %v", err)
	}
}

func TestWatcherWatchEmptyDomains(t *testing.T) {
	n, sw := initWatcherTestNode(t, "watcher-node")
	defer sw.Close()

	if _, err := n.Watch(time.Second); err != ErrEmptyWatchDomains {
		t.Errorf("expected ErrEmptyWatchDomains, got %v", err)
	}
	if err := n.Unwatch(time.Second); err != ErrEmptyWatchDomains {
		t.Errorf("expected ErrEmptyWatchDomains, got %v", err)
	}
}

func TestWatcherUnwatch(t *testing.T) {
	n, sw := initWatcherTestNode(t, "watcher-node")
	defer sw.Close()

	go func() {
		pbuf, req := readSubscribeRequest(t, sw)
		if req.Op != packet.SubscribeRemove {
			t.Errorf("expected op=SubscribeRemove, got %v", req.Op)
		}
		writeSubscribeACK(t, sw, pbuf, packet.SubscribeACK{OK: true})
	}()

	if err := n.Unwatch(time.Second*3, "target"); err != nil {
		t.Errorf("unwatch failed: %v", err)
	}
}

func writePresenceNotify(t *testing.T, sw packet.Conn, distIP uint16, ev packet.PresenceEvent) {
	t.Helper()
	pbuf := packet.NewBufferWithCmd(packet.CmdNotifyPresence)
	pbuf.SetSrc(packet.SwitcherIP, 0)
	pbuf.SetDist(distIP, 0)
	_ = pbuf.SetPayload(ev.Encode())
	if err := sw.WriteBuffer(pbuf); err != nil {
		t.Fatalf("write notify failed: %v", err)
	}
}

func waitViewVersion(t *testing.T, n *Node, domain string, version uint64) packet.PresenceState {
	t.Helper()
	deadline := time.Now().Add(time.Second * 3)
	for time.Now().Before(deadline) {
		if st, ok := n.GetPresence(domain); ok && st.Version == version {
			return st
		}
		time.Sleep(time.Millisecond * 10)
	}
	t.Fatalf("timeout waiting view %v version=%v", domain, version)
	return packet.PresenceState{}
}

func TestWatcherNotify(t *testing.T) {
	n, sw := initWatcherTestNode(t, "watcher-node")
	defer sw.Close()

	// 未设置 handler 时收到通知：不应 panic
	writePresenceNotify(t, sw, 1, packet.PresenceEvent{Seq: 1, Version: 1, Domain: "target", Online: true, IP: 7})

	// 用一次 Watch 往返作为同步点：notify 与 ack 均经 cmdChan 有序处理，
	// Watch 返回时上面的 notify 一定已被分发完毕（无 handler，静默丢弃）
	go func() {
		pbuf, _ := readSubscribeRequest(t, sw)
		writeSubscribeACK(t, sw, pbuf, packet.SubscribeACK{OK: true})
	}()
	if _, err := n.Watch(time.Second*3, "target"); err != nil {
		t.Fatalf("watch failed: %v", err)
	}

	ch := make(chan packet.PresenceEvent, 2)
	n.SetPresenceHandler(func(ev packet.PresenceEvent) { ch <- ev })

	writePresenceNotify(t, sw, 1, packet.PresenceEvent{Seq: 2, Version: 2, Domain: "target", Online: false, IP: 7, Mac: "m1"})

	select {
	case ev := <-ch:
		if ev.Domain != "target" || ev.Online || ev.IP != 7 || ev.Mac != "m1" || ev.Seq != 2 || ev.Version != 2 {
			t.Errorf("unexpected event: %+v", ev)
		}
	case <-time.After(time.Second * 3):
		t.Fatal("timeout waiting presence event")
	}
}

func TestWatcherViewIntegration(t *testing.T) {
	n, sw := initWatcherTestNode(t, "watcher-node")
	defer sw.Close()

	go func() {
		pbuf, _ := readSubscribeRequest(t, sw)
		writeSubscribeACK(t, sw, pbuf, packet.SubscribeACK{OK: true, States: []packet.PresenceState{
			{Domain: "target", Online: true, IP: 7, Mac: "m1", Version: 1},
		}})
		// Unwatch 请求：回空 ACK
		pbuf, _ = readSubscribeRequest(t, sw)
		writeSubscribeACK(t, sw, pbuf, packet.SubscribeACK{OK: true})
	}()

	// Watch 后快照进入视图
	if _, err := n.Watch(time.Second*3, "target"); err != nil {
		t.Fatalf("watch failed: %v", err)
	}
	st, ok := n.GetPresence("target")
	if !ok || !st.Online || st.IP != 7 || st.Version != 1 {
		t.Fatalf("unexpected view state: %+v ok=%v", st, ok)
	}

	// 事件增量更新视图
	writePresenceNotify(t, sw, 1, packet.PresenceEvent{Version: 2, Domain: "target", Online: false, IP: 7})
	st = waitViewVersion(t, n, "target", 2)
	if st.Online {
		t.Errorf("expected offline in view, got %+v", st)
	}

	// Unwatch 后从视图移除
	if err := n.Unwatch(time.Second*3, "target"); err != nil {
		t.Fatalf("unwatch failed: %v", err)
	}
	if _, ok := n.GetPresence("target"); ok {
		t.Error("expected view miss after unwatch")
	}
}

func TestWatcherStaleEventDropped(t *testing.T) {
	n, sw := initWatcherTestNode(t, "watcher-node")
	defer sw.Close()

	ch := make(chan packet.PresenceEvent, 2)
	n.SetPresenceHandler(func(ev packet.PresenceEvent) { ch <- ev })

	go func() {
		pbuf, _ := readSubscribeRequest(t, sw)
		writeSubscribeACK(t, sw, pbuf, packet.SubscribeACK{OK: true, States: []packet.PresenceState{
			{Domain: "target", Online: true, IP: 7, Version: 2},
		}})
	}()
	if _, err := n.Watch(time.Second*3, "target"); err != nil {
		t.Fatalf("watch failed: %v", err)
	}

	// version 不增的事件是 stale：不投递、不污染视图
	writePresenceNotify(t, sw, 1, packet.PresenceEvent{Version: 2, Domain: "target", Online: false, IP: 9})
	writePresenceNotify(t, sw, 1, packet.PresenceEvent{Version: 1, Domain: "target", Online: false, IP: 9})

	select {
	case ev := <-ch:
		t.Fatalf("stale event should not be delivered: %+v", ev)
	case <-time.After(time.Millisecond * 300):
	}
	if st, _ := n.GetPresence("target"); !st.Online || st.IP != 7 || st.Version != 2 {
		t.Errorf("stale event polluted view: %+v", st)
	}
}

func TestWatcherGapResync(t *testing.T) {
	n, sw := initWatcherTestNode(t, "watcher-node")
	defer sw.Close()

	ch := make(chan packet.PresenceEvent, 4)
	n.SetPresenceHandler(func(ev packet.PresenceEvent) { ch <- ev })

	go func() {
		// 首次订阅：快照 v1
		pbuf, _ := readSubscribeRequest(t, sw)
		writeSubscribeACK(t, sw, pbuf, packet.SubscribeACK{OK: true, States: []packet.PresenceState{
			{Domain: "target", Online: true, IP: 7, Version: 1},
		}})
		// gap 触发的自动重订阅：返回 v3 快照
		pbuf, _ = readSubscribeRequest(t, sw)
		writeSubscribeACK(t, sw, pbuf, packet.SubscribeACK{OK: true, States: []packet.PresenceState{
			{Domain: "target", Online: false, IP: 7, Version: 3},
		}})
	}()

	if _, err := n.Watch(time.Second*3, "target"); err != nil {
		t.Fatalf("watch failed: %v", err)
	}

	// 注入缺口事件 v3（视图当前 v1）：不投递，触发自动重同步
	writePresenceNotify(t, sw, 1, packet.PresenceEvent{Version: 3, Domain: "target", Online: false, IP: 7})

	st := waitViewVersion(t, n, "target", 3)
	if st.Online {
		t.Errorf("expected offline after resync, got %+v", st)
	}

	// 缺口事件不应投递给 handler
	select {
	case ev := <-ch:
		t.Fatalf("gap event should not be delivered: %+v", ev)
	case <-time.After(time.Millisecond * 300):
	}
}
