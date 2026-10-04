package node

import (
	"sync"

	"github.com/net-agent/flex/v3/packet"
)

// PresenceView 是 presence 状态的本地副本，由订阅快照与后续事件增量维护。
// 一致性语义由 per-domain version 承载：stale 事件被丢弃，事件缺口被识别。
// Watcher（随连接生死）与 Session（跨重连 reconcile）各持有一份。
type PresenceView struct {
	mu     sync.RWMutex
	states map[string]packet.PresenceState
}

// applySnapshot 用订阅应答的快照覆盖涉及的域名。
// 快照与事件在本地由不同 goroutine 应用（ACK 唤醒 Watch 调用方，事件走 dispatcher），
// 线上的序无法传导到本地应用顺序，因此比本地视图更旧的快照条目直接跳过，不回退。
func (v *PresenceView) applySnapshot(states []packet.PresenceState) {
	v.apply(states, false)
}

// resetSnapshot 以权威重置语义应用快照：无条件覆盖涉及的域名。
// 仅用于重连后的重订阅——此时旧连接上累积的版本已不可比
// （switcher 重启或离线条目被淘汰都会使 version 重新计数），
// 新连接上的快照就是当前真相。同连接内的常规订阅必须用 applySnapshot。
func (v *PresenceView) resetSnapshot(states []packet.PresenceState) {
	v.apply(states, true)
}

func (v *PresenceView) apply(states []packet.PresenceState, force bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.states == nil {
		v.states = make(map[string]packet.PresenceState, len(states))
	}
	for _, st := range states {
		if !force {
			if cur, ok := v.states[st.Domain]; ok && cur.Version > st.Version {
				continue
			}
		}
		v.states[st.Domain] = st
	}
}

// applyEvent 应用一次状态迁移，返回值含义：
//   - applied=false, gap=false：stale 事件（version <= 当前），已丢弃
//   - applied=false, gap=true：检测到事件缺口（version 跳变），调用方应重同步该域名
//   - applied=true, gap=false：迁移已应用
func (v *PresenceView) applyEvent(ev packet.PresenceEvent) (applied, gap bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.states == nil {
		v.states = make(map[string]packet.PresenceState)
	}
	cur, ok := v.states[ev.Domain]
	if ok && ev.Version <= cur.Version {
		return false, false
	}
	if ok && ev.Version > cur.Version+1 {
		return false, true
	}
	v.states[ev.Domain] = packet.PresenceState{
		Domain:  ev.Domain,
		Online:  ev.Online,
		IP:      ev.IP,
		Mac:     ev.Mac,
		Version: ev.Version,
	}
	return true, false
}

func (v *PresenceView) remove(domains ...string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	for _, d := range domains {
		delete(v.states, d)
	}
}

func (v *PresenceView) get(domain string) (packet.PresenceState, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	st, ok := v.states[domain]
	return st, ok
}

func (v *PresenceView) list() []packet.PresenceState {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make([]packet.PresenceState, 0, len(v.states))
	for _, st := range v.states {
		out = append(out, st)
	}
	return out
}
