package node

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v3/internal/idpool"
	"github.com/net-agent/flex/v3/internal/pending"
	"github.com/net-agent/flex/v3/packet"
)

var (
	ErrWatchTimeout      = errors.New("watch subscribe timeout")
	ErrEmptyWatchDomains = errors.New("watch domains is empty")
	ErrTooManyDomains    = errors.New("too many domains in one watch call")
)

// 单次 Watch/Unwatch 最多携带的域名数（受 SubscribeRequest 编码的 count 字段宽度限制）
const maxWatchDomains = 255

// Watcher 提供 named node 上下线状态的订阅与通知能力。
// 基于 switcher 控制面实现：订阅请求的应答携带当前状态快照，
// 之后的状态迁移以 CmdNotifyPresence 事件实时推送。
// Watcher 只投递事件，不做任何自动动作（如关闭 stream）；
// 需要 fail-fast 语义的应用可在事件回调中自行组合。
type Watcher struct {
	host    *Node
	portm   *idpool.Pool
	pending pending.Requests[[]packet.PresenceState]

	hmut    sync.RWMutex
	handler func(packet.PresenceEvent)

	view      PresenceView
	resyncing sync.Map // domain → struct{}，缺口重同步去重
}

func (w *Watcher) init(host *Node) {
	w.host = host
	w.portm, _ = idpool.New(1, 0xffff)
}

// SetHandler 设置 presence 事件回调，可在运行期间替换。
// 回调在 dispatcher 的 cmd goroutine 中同步执行，不得阻塞。
func (w *Watcher) SetHandler(fn func(packet.PresenceEvent)) {
	w.hmut.Lock()
	w.handler = fn
	w.hmut.Unlock()
}

func (w *Watcher) getHandler() func(packet.PresenceEvent) {
	w.hmut.RLock()
	defer w.hmut.RUnlock()
	return w.handler
}

// Watch 订阅一组域名的上下线事件，返回这些域名的当前状态快照。
// 快照与后续事件在 switcher 侧原子衔接：订阅生效后的每次状态迁移都会推送给本节点。
// 返回的快照同时用于维护本地 PresenceView（见 GetPresence/ListPresence）。
func (w *Watcher) Watch(timeout time.Duration, domains ...string) ([]packet.PresenceState, error) {
	states, err := w.subscribe(packet.SubscribeAdd, timeout, domains...)
	if err != nil {
		return nil, err
	}
	w.view.applySnapshot(states)
	return states, nil
}

// Unwatch 取消对一组域名的订阅，并将其从本地 PresenceView 中移除。
func (w *Watcher) Unwatch(timeout time.Duration, domains ...string) error {
	if _, err := w.subscribe(packet.SubscribeRemove, timeout, domains...); err != nil {
		return err
	}
	w.view.remove(domains...)
	return nil
}

func (w *Watcher) subscribe(op byte, timeout time.Duration, domains ...string) ([]packet.PresenceState, error) {
	if len(domains) == 0 {
		return nil, ErrEmptyWatchDomains
	}
	if len(domains) > maxWatchDomains {
		return nil, ErrTooManyDomains
	}

	port, err := w.portm.Allocate()
	if err != nil {
		w.host.tracePortExhausted("watcher", err) // watcher 使用独立的端口池
		return nil, err
	}
	defer w.portm.Release(port)

	ch, err := w.pending.Register(port)
	if err != nil {
		return nil, err
	}
	defer w.pending.Remove(port)

	pbuf := packet.NewBufferWithCmd(packet.CmdSubscribePresence)
	pbuf.SetSrc(w.host.GetIP(), port)
	pbuf.SetDist(packet.SwitcherIP, 0)
	req := packet.SubscribeRequest{Op: op, Domains: domains}
	_ = pbuf.SetPayload(req.Encode())
	if err = w.host.WriteBuffer(pbuf); err != nil {
		atomic.AddInt64(&w.host.failures.WatchFailed, 1)
		return nil, err
	}

	select {
	case res, ok := <-ch:
		if !ok {
			atomic.AddInt64(&w.host.failures.WatchFailed, 1)
			return nil, ErrWatchTimeout
		}
		if res.Err != nil {
			atomic.AddInt64(&w.host.failures.WatchFailed, 1)
			return nil, res.Err
		}
		return res.Val, nil
	case <-time.After(timeout):
		atomic.AddInt64(&w.host.failures.WatchFailed, 1)
		return nil, ErrWatchTimeout
	}
}

// handleAckSubscribePresence 处理 switcher 的订阅应答
func (w *Watcher) handleAckSubscribePresence(pbuf *packet.Buffer) {
	ack := packet.DecodeSubscribeACK(pbuf.Payload)
	if !ack.OK {
		_ = w.pending.Complete(pbuf.DistPort(), nil, errors.New(ack.Error))
		return
	}
	_ = w.pending.Complete(pbuf.DistPort(), ack.States, nil)
}

// handleCmdNotifyPresence 处理 switcher 推送的上下线事件。
// 事件先用于维护本地 PresenceView：stale 事件丢弃并静默；
// version 跳变说明中间有事件缺失，触发该域名的自动重同步（事件本身不再投递，
// 因为缺口期间的真实状态已不可知）；正常迁移才调用用户 handler。
func (w *Watcher) handleCmdNotifyPresence(pbuf *packet.Buffer) {
	ev := packet.DecodePresenceEvent(pbuf.Payload)
	applied, gap := w.view.applyEvent(ev)
	if gap {
		w.host.logger.Warn("presence event gap detected, resync", "domain", ev.Domain, "version", ev.Version)
		w.resync(ev.Domain)
		return
	}
	if !applied {
		return
	}
	if fn := w.getHandler(); fn != nil {
		fn(ev)
	}
}

// resync 对指定域名发起一次 Watch 重同步（异步、按域名去重）。
// TCP 有序可靠的承载下缺口理论上不应出现，此为自愈兜底。
func (w *Watcher) resync(domain string) {
	if _, loaded := w.resyncing.LoadOrStore(domain, struct{}{}); loaded {
		return
	}
	go func() {
		defer w.resyncing.Delete(domain)
		if _, err := w.Watch(time.Second*5, domain); err != nil {
			w.host.logger.Warn("presence resync failed", "domain", domain, "error", err)
		}
	}()
}
