package node

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v4/internal/idpool"
	"github.com/net-agent/flex/v4/internal/pending"
	"github.com/net-agent/flex/v4/packet"
	"github.com/net-agent/flex/v4/wire"
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

	req := packet.SubscribeRequest{Op: op, Domains: domains}
	f, err := wire.NewControl(
		wire.CmdSubscribePresence,
		wire.Addr{IP: w.host.GetIP(), Port: port},
		wire.SwitcherAddr,
		req.Encode(),
	)
	if err != nil {
		return nil, err
	}
	if err = w.host.SendFrame(f); err != nil {
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

// handleAckFrame handles a subscribe ACK. It is a control-lane handler.
func (w *Watcher) handleAckFrame(f *wire.Frame) error {
	ack := packet.DecodeSubscribeACK(f.Payload())
	if !ack.OK {
		_ = w.pending.Complete(f.Dst().Port, nil, errors.New(ack.Error))
		return nil
	}
	_ = w.pending.Complete(f.Dst().Port, ack.States, nil)
	return nil
}

// handleNotifyFrame handles a presence event. It is a control-lane handler.
func (w *Watcher) handleNotifyFrame(f *wire.Frame) error {
	ev := packet.DecodePresenceEvent(f.Payload())
	applied, gap := w.view.applyEvent(ev)
	if gap {
		w.host.logger.Warn("presence event gap detected, resync", "domain", ev.Domain, "version", ev.Version)
		w.resync(ev.Domain)
		return nil
	}
	if !applied {
		return nil
	}
	if fn := w.getHandler(); fn != nil {
		fn(ev)
	}
	return nil
}

// handleAckSubscribePresence is the legacy packet.Buffer adapter.
func (w *Watcher) handleAckSubscribePresence(pbuf *packet.Buffer) {
	f, err := bufferToFrame(pbuf)
	if err != nil {
		return
	}
	_ = w.handleAckFrame(f)
	f.Release()
}

// handleCmdNotifyPresence is the legacy packet.Buffer adapter.
func (w *Watcher) handleCmdNotifyPresence(pbuf *packet.Buffer) {
	f, err := bufferToFrame(pbuf)
	if err != nil {
		return
	}
	_ = w.handleNotifyFrame(f)
	f.Release()
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
