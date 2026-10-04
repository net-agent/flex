package switcher

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v3/internal/admit"
	"github.com/net-agent/flex/v3/packet"
)

const presenceEventQueueSize = 1024

// presence 消息类型
const (
	presenceEventOnline = iota
	presenceEventOffline
	presenceEventSubscribe
	presenceEventPurge
)

// presenceMsg 是 presenceCenter 内部事件队列的消息。
type presenceMsg struct {
	kind   int
	seq    uint64
	domain string
	ip     uint16
	mac    string

	// subscribe 消息专用
	subCtx  *Context
	op      byte
	domains []string
	ackPort uint16
}

type presenceEntry struct {
	online  bool
	ip      uint16
	mac     string
	version uint64 // per-domain 单调版本号，每次生效的状态迁移 +1

	// offline 生效的时间。条目在 offline 后存续以维持 version 连续，
	// 但超过 offlineTTL 且始终无订阅者时被 sweep 回收，防止 state 无界增长
	offlineAt time.Time
}

type presenceSub struct {
	ctx     *Context
	domains map[string]struct{}
}

// presenceCenter 是节点上下线事件的分发中心。
// registry 在状态突变点入队事件，run() 单 goroutine 串行消费，
// 是 state/subs/watchers 的唯一读写者，因此事件对订阅者严格按序投递。
type presenceCenter struct {
	seq       atomic.Uint64
	events    chan presenceMsg
	done      chan struct{}
	closeOnce sync.Once
	logger    *slog.Logger

	offlineTTL time.Duration // offline 条目在无订阅者时的保留时长
	sweepEvery time.Duration // 淘汰清扫周期

	// 以下字段仅由 run() goroutine 访问，无需加锁
	state    map[string]presenceEntry       // domain → 节点信息（快照数据源）
	subs     map[uint16]*presenceSub        // subscriber IP → 订阅关系
	watchers map[string]map[uint16]struct{} // domain → subscriber IPs（fanout/淘汰的倒排索引）
}

func newPresenceCenter(logger *slog.Logger) *presenceCenter {
	if logger == nil {
		logger = slog.Default()
	}
	pc := &presenceCenter{
		events:     make(chan presenceMsg, presenceEventQueueSize),
		done:       make(chan struct{}),
		logger:     logger,
		offlineTTL: time.Hour,
		sweepEvery: time.Minute,
		state:      make(map[string]presenceEntry),
		subs:       make(map[uint16]*presenceSub),
		watchers:   make(map[string]map[uint16]struct{}),
	}
	go pc.run()
	return pc
}

func (pc *presenceCenter) stop() {
	pc.closeOnce.Do(func() { close(pc.done) })
}

// emit 入队一条消息。调用方可能持有 registry 锁，run() 不持有任何 registry 锁，
// 队列满时仅表现为短暂阻塞，不会死锁。stop 之后直接丢弃。
func (pc *presenceCenter) emit(msg presenceMsg) {
	msg.seq = pc.seq.Add(1)
	select {
	case pc.events <- msg:
	case <-pc.done:
	}
}

func (pc *presenceCenter) emitOnline(domain string, ip uint16, mac string) {
	pc.emit(presenceMsg{kind: presenceEventOnline, domain: domain, ip: ip, mac: mac})
}

func (pc *presenceCenter) emitOffline(domain string, ip uint16, mac string) {
	pc.emit(presenceMsg{kind: presenceEventOffline, domain: domain, ip: ip, mac: mac})
}

func (pc *presenceCenter) purgeSubscriber(ip uint16) {
	pc.emit(presenceMsg{kind: presenceEventPurge, ip: ip})
}

// handleSubscribe 处理节点发来的订阅/退订请求（由 router 调用）。
// 域名在入队前完成归一化与校验，非法域名直接回错误 ACK。
func (pc *presenceCenter) handleSubscribe(ctx *Context, req packet.SubscribeRequest, ackPort uint16) {
	normalized := make([]string, 0, len(req.Domains))
	for _, d := range req.Domains {
		nd, err := admit.NormalizeDomain(d)
		if err != nil {
			pc.replySubscribeACK(ctx, ackPort, packet.SubscribeACK{OK: false, Error: err.Error()})
			return
		}
		normalized = append(normalized, nd)
	}
	pc.emit(presenceMsg{
		kind:    presenceEventSubscribe,
		subCtx:  ctx,
		op:      req.Op,
		domains: normalized,
		ackPort: ackPort,
	})
}

func (pc *presenceCenter) run() {
	ticker := time.NewTicker(pc.sweepEvery)
	defer ticker.Stop()
	for {
		select {
		case <-pc.done:
			return
		case now := <-ticker.C:
			pc.sweep(now)
		case msg := <-pc.events:
			switch msg.kind {
			case presenceEventOnline:
				pc.handleOnline(msg)
			case presenceEventOffline:
				pc.handleOffline(msg)
			case presenceEventSubscribe:
				pc.handleSubscribeMsg(msg)
			case presenceEventPurge:
				pc.handlePurge(msg.ip)
			}
		}
	}
}

func (pc *presenceCenter) handleOnline(msg presenceMsg) {
	entry := pc.state[msg.domain]
	if entry.online && entry.ip == msg.ip {
		return // 重复事件，无状态迁移
	}
	entry.online = true
	entry.ip = msg.ip
	entry.mac = msg.mac
	entry.version++
	entry.offlineAt = time.Time{}
	pc.state[msg.domain] = entry
	pc.fanout(msg, entry.version, true)
}

func (pc *presenceCenter) handleOffline(msg presenceMsg) {
	entry, ok := pc.state[msg.domain]
	if !ok || !entry.online || entry.ip != msg.ip {
		return // stale：域名不在线或已被新 ctx 接管（replace 场景）
	}
	// entry 保留（含 version 与最后已知的 ip/mac），由 sweep 按 TTL 回收
	entry.online = false
	entry.version++
	entry.offlineAt = time.Now()
	pc.state[msg.domain] = entry
	pc.fanout(msg, entry.version, false)
}

// sweep 回收"offline 且持续无订阅者超过 offlineTTL"的条目。
// version 的连续单调性只在有观察者期间才有意义：无人订阅且长期离线的域名，
// 回收后 version 重新计数；重订阅方的视图由 Session 的权威重置规则对齐
// （重连后的快照无条件覆盖本地视图），因此不会产生可感知的分叉。
func (pc *presenceCenter) sweep(now time.Time) {
	for domain, entry := range pc.state {
		if entry.online || len(pc.watchers[domain]) > 0 {
			continue
		}
		if now.Sub(entry.offlineAt) < pc.offlineTTL {
			continue
		}
		delete(pc.state, domain)
	}
}

// handlePurge 清理断连订阅者的全部订阅关系（正排与倒排索引）。
func (pc *presenceCenter) handlePurge(ip uint16) {
	sub, ok := pc.subs[ip]
	if !ok {
		return
	}
	for domain := range sub.domains {
		delete(pc.watchers[domain], ip)
		if len(pc.watchers[domain]) == 0 {
			delete(pc.watchers, domain)
		}
	}
	delete(pc.subs, ip)
}

// fanout 向订阅了该域名的所有节点投递通知（倒排索引，只遍历该域名的订阅者）。
// 写失败的订阅者视为已死，统一清理其订阅关系（其 ctx 的 detach purge 兜底幂等）。
func (pc *presenceCenter) fanout(msg presenceMsg, version uint64, online bool) {
	ev := packet.PresenceEvent{Seq: msg.seq, Version: version, Domain: msg.domain, Online: online, IP: msg.ip, Mac: msg.mac}
	payload := ev.Encode()
	var dead []uint16
	for subIP := range pc.watchers[msg.domain] {
		sub, ok := pc.subs[subIP]
		if !ok {
			continue
		}
		pbuf := packet.NewBufferWithCmd(packet.CmdNotifyPresence)
		pbuf.SetSrc(packet.SwitcherIP, 0)
		pbuf.SetDist(subIP, 0)
		if err := pbuf.SetPayload(payload); err != nil {
			continue
		}
		if err := sub.ctx.enqueueForward(pbuf); err != nil {
			pc.logger.Warn("presence notify enqueue failed, drop subscriber",
				"domain", msg.domain, "subscriber_ip", subIP, "error", err)
			packet.PutBuffer(pbuf) // 未入队，所有权未转移
			dead = append(dead, subIP)
		}
	}
	for _, ip := range dead {
		pc.handlePurge(ip)
	}
}

func (pc *presenceCenter) handleSubscribeMsg(msg presenceMsg) {
	sub, ok := pc.subs[msg.subCtx.IP]
	if !ok {
		sub = &presenceSub{ctx: msg.subCtx, domains: make(map[string]struct{})}
		pc.subs[msg.subCtx.IP] = sub
	} else {
		sub.ctx = msg.subCtx // 防御：同 IP 复用时刷新 ctx 指针
	}

	ack := packet.SubscribeACK{OK: true}
	switch msg.op {
	case packet.SubscribeAdd:
		for _, d := range msg.domains {
			sub.domains[d] = struct{}{}
			set, ok := pc.watchers[d]
			if !ok {
				set = make(map[uint16]struct{})
				pc.watchers[d] = set
			}
			set[msg.subCtx.IP] = struct{}{}
			st := packet.PresenceState{Domain: d}
			if entry, ok := pc.state[d]; ok {
				st.Online = entry.online
				st.IP = entry.ip
				st.Mac = entry.mac
				st.Version = entry.version
			}
			ack.States = append(ack.States, st)
		}
	case packet.SubscribeRemove:
		for _, d := range msg.domains {
			delete(sub.domains, d)
			delete(pc.watchers[d], msg.subCtx.IP)
			if len(pc.watchers[d]) == 0 {
				delete(pc.watchers, d)
			}
		}
		if len(sub.domains) == 0 {
			delete(pc.subs, msg.subCtx.IP)
		}
	default:
		ack.OK = false
		ack.Error = "unknown subscribe op"
	}
	pc.replySubscribeACK(msg.subCtx, msg.ackPort, ack)
}

func (pc *presenceCenter) replySubscribeACK(ctx *Context, ackPort uint16, ack packet.SubscribeACK) {
	pbuf := packet.NewBufferWithCmd(packet.AckSubscribePresence)
	pbuf.SetSrc(packet.SwitcherIP, 0)
	pbuf.SetDist(ctx.IP, ackPort)
	if err := pbuf.SetPayload(ack.Encode()); err != nil {
		pc.logger.Warn("presence subscribe ack encode failed", "ctx_id", ctx.id, "error", err)
		packet.PutBuffer(pbuf)
		return
	}
	if err := ctx.enqueueForward(pbuf); err != nil {
		pc.logger.Warn("presence subscribe ack enqueue failed", "ctx_id", ctx.id, "error", err)
		packet.PutBuffer(pbuf)
	}
}
