package node

import (
	"testing"

	"github.com/net-agent/flex/v3/packet"
)

func TestPresenceViewSnapshotAndQuery(t *testing.T) {
	var v PresenceView

	if _, ok := v.get("a"); ok {
		t.Error("expected miss on empty view")
	}

	v.applySnapshot([]packet.PresenceState{
		{Domain: "a", Online: true, IP: 1, Mac: "m1", Version: 1},
		{Domain: "b", Online: false, Version: 2},
	})

	st, ok := v.get("a")
	if !ok || !st.Online || st.IP != 1 || st.Version != 1 {
		t.Errorf("unexpected state for a: %+v ok=%v", st, ok)
	}
	if st, ok := v.get("b"); !ok || st.Online || st.Version != 2 {
		t.Errorf("unexpected state for b: %+v ok=%v", st, ok)
	}
	if got := len(v.list()); got != 2 {
		t.Errorf("expected 2 states, got %v", got)
	}

	// 快照覆盖已有域名
	v.applySnapshot([]packet.PresenceState{{Domain: "a", Online: false, Version: 3}})
	if st, _ := v.get("a"); st.Online || st.Version != 3 {
		t.Errorf("snapshot overwrite failed: %+v", st)
	}

	// 更旧的快照不回退本地视图（快照与事件由不同 goroutine 应用，后到的旧快照必须跳过）
	v.applySnapshot([]packet.PresenceState{{Domain: "a", Online: true, IP: 9, Version: 2}})
	if st, _ := v.get("a"); st.Online || st.Version != 3 || st.IP != 0 {
		t.Errorf("stale snapshot regressed view: %+v", st)
	}

	// 同版本快照幂等覆盖（内容必然一致）
	v.applySnapshot([]packet.PresenceState{{Domain: "a", Online: false, Version: 3}})
	if st, _ := v.get("a"); st.Version != 3 {
		t.Errorf("equal-version snapshot should apply idempotently: %+v", st)
	}

	v.remove("a")
	if _, ok := v.get("a"); ok {
		t.Error("expected miss after remove")
	}
}

func TestPresenceViewApplyEvent(t *testing.T) {
	var v PresenceView
	v.applySnapshot([]packet.PresenceState{{Domain: "a", Online: true, IP: 1, Version: 1}})

	// 常规迁移 v1 → v2
	applied, gap := v.applyEvent(packet.PresenceEvent{Domain: "a", Online: false, IP: 1, Version: 2})
	if !applied || gap {
		t.Fatalf("expected applied transition, got applied=%v gap=%v", applied, gap)
	}
	if st, _ := v.get("a"); st.Online || st.Version != 2 {
		t.Fatalf("unexpected state: %+v", st)
	}

	// stale 事件（version <= 当前）被丢弃
	applied, gap = v.applyEvent(packet.PresenceEvent{Domain: "a", Online: true, IP: 9, Version: 2})
	if applied || gap {
		t.Fatalf("expected stale drop, got applied=%v gap=%v", applied, gap)
	}
	if st, _ := v.get("a"); st.IP != 1 {
		t.Fatalf("stale event polluted view: %+v", st)
	}

	// 缺口事件（version 跳变）不应用
	applied, gap = v.applyEvent(packet.PresenceEvent{Domain: "a", Online: true, IP: 2, Version: 5})
	if applied || !gap {
		t.Fatalf("expected gap, got applied=%v gap=%v", applied, gap)
	}
	if st, _ := v.get("a"); st.Version != 2 {
		t.Fatalf("gap event polluted view: %+v", st)
	}

	// 未见过的域名：任意 version 直接应用
	applied, gap = v.applyEvent(packet.PresenceEvent{Domain: "b", Online: true, IP: 3, Version: 7})
	if !applied || gap {
		t.Fatalf("expected applied for first sight, got applied=%v gap=%v", applied, gap)
	}
}

func TestPresenceViewResetSnapshot(t *testing.T) {
	var v PresenceView
	v.applySnapshot([]packet.PresenceState{{Domain: "a", Online: false, IP: 1, Version: 6}})

	// 权威重置：version 回退也覆盖（switcher 重启 / 离线条目淘汰后的新纪元快照）
	v.resetSnapshot([]packet.PresenceState{{Domain: "a", Online: true, IP: 2, Version: 1}})
	st, ok := v.get("a")
	if !ok || !st.Online || st.IP != 2 || st.Version != 1 {
		t.Errorf("reset should overwrite regardless of version: %+v ok=%v", st, ok)
	}
}
