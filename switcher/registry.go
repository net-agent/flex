package switcher

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/net-agent/flex/v3/internal/admit"
	"github.com/net-agent/flex/v3/internal/idpool"
	"github.com/net-agent/flex/v3/packet"
)

var (
	errReplaceDomainFailed    = errors.New("replace domain context failed")
	errContextIPExist         = errors.New("context ip exist")
	errGetFreeContextIPFailed = errors.New("get unused ip failed")
	errDomainNotFound         = errors.New("domain not found")
	errInvalidContextIP       = errors.New("invalid context ip")
	errContextIPNotFound      = errors.New("context ip not found")
)

type contextRegistry struct {
	ipm      *idpool.Pool
	logger   *slog.Logger
	presence *presenceCenter

	// domainPingTimeout 是域名抢占前对旧持有者做存活探测的超时。
	// 可在测试中缩短，生产默认为 3s。
	domainPingTimeout time.Duration

	domainMu    sync.Mutex
	domainIndex map[string]*Context

	ipMu    sync.Mutex
	ipIndex map[uint16]*Context

	recordsMu sync.Mutex
	records   []*Context
}

func newContextRegistry(ipm *idpool.Pool, logger *slog.Logger, presence *presenceCenter) *contextRegistry {
	if logger == nil {
		logger = slog.Default()
	}
	return &contextRegistry{
		ipm:               ipm,
		logger:            logger,
		presence:          presence,
		domainPingTimeout: 3 * time.Second,
		domainIndex:       make(map[string]*Context),
		ipIndex:           make(map[uint16]*Context),
	}
}

func (r *contextRegistry) attach(ctx *Context) error {
	normalized, err := admit.NormalizeDomain(ctx.Domain)
	if err != nil {
		r.logger.Warn("attach failed: invalid domain", "ctx_id", ctx.id, "domain", ctx.Domain)
		return err
	}
	ctx.Domain = normalized

	// 预检：域名被占用时先在锁外做存活探测。网络 IO 不得持锁，
	// 否则一个无响应的节点会把整个 registry 卡住 domainPingTimeout。
	r.domainMu.Lock()
	occupied := r.domainIndex[ctx.Domain]
	r.domainMu.Unlock()
	if occupied != nil && occupied != ctx {
		if _, err := occupied.ping(r.domainPingTimeout); err == nil {
			r.logger.Warn("attach failed: replace domain failed", "ctx_id", ctx.id, "domain", ctx.Domain)
			return errReplaceDomainFailed
		}
	}

	// 临界区：一次性完成"确认持有者 + 分配 IP 租约 + 双索引登记"。
	// 旧实现把域名认领与 IP 分配拆成两步，中间态会成为并发替换的僵尸条目，
	// 且 IP 在 attach 与 detach 两处各释放一次。
	r.domainMu.Lock()
	current := r.domainIndex[ctx.Domain]
	if current == ctx {
		// 幂等 re-attach（同一 ctx 重复 attach）：保持既有登记不变。
		r.domainMu.Unlock()
		return nil
	}
	if current != nil && current != occupied {
		// ping 期间域名已被第三方接管：放弃本次 attach，交由上层重连。
		r.domainMu.Unlock()
		r.logger.Warn("attach failed: domain taken during probe", "ctx_id", ctx.id, "domain", ctx.Domain)
		return errReplaceDomainFailed
	}

	lease, err := r.ipm.Acquire()
	if err != nil {
		r.domainMu.Unlock()
		r.logger.Warn("attach failed: IP exhausted", "ctx_id", ctx.id, "domain", ctx.Domain, "error", err)
		return errGetFreeContextIPFailed
	}

	r.ipMu.Lock()
	if _, exists := r.ipIndex[lease.ID()]; exists {
		r.ipMu.Unlock()
		r.domainMu.Unlock()
		lease.Release()
		r.logger.Warn("attach failed: IP conflict", "ctx_id", ctx.id, "domain", ctx.Domain, "ip", lease.ID())
		return errContextIPExist
	}
	ctx.IP = lease.ID()
	ctx.ipLease = lease
	ctx.AttachTime = time.Now()
	r.ipIndex[ctx.IP] = ctx
	r.ipMu.Unlock()

	r.domainIndex[ctx.Domain] = ctx
	ctx.setAttached(true)
	r.domainMu.Unlock()

	if current != nil {
		// 旧持有者已被替换：先广播下线，再从索引/池中摘除（detach 幂等）。
		r.presence.emitOffline(current.Domain, current.IP, current.Mac)
		r.logger.Info("domain replaced", "domain", ctx.Domain, "old_ctx_id", current.id, "new_ctx_id", ctx.id)
		r.detach(current)
	}

	r.appendRecord(ctx)
	r.presence.emitOnline(ctx.Domain, ctx.IP, ctx.Mac)
	r.logger.Info("context attached", "ctx_id", ctx.id, "domain", ctx.Domain, "ip", ctx.IP, "mac", ctx.Mac)
	return nil
}

// detach 取消一个 ctx 的全部登记。整个过程幂等：
// 域名替换与连接断开可能并发/先后调用，重复调用不会误删他人条目，
// 也不会重复归还 IP（租约保证只归还一次）。
func (r *contextRegistry) detach(ctx *Context) {
	offline := false
	r.domainMu.Lock()
	if current, ok := r.domainIndex[ctx.Domain]; ok && current == ctx {
		delete(r.domainIndex, ctx.Domain)
		offline = true
	}
	r.domainMu.Unlock()

	// presence 事件入队可能阻塞（队列满时会等到 run() 消费），
	// 必须放在锁外，避免拖住整个 registry。
	if offline {
		r.presence.emitOffline(ctx.Domain, ctx.IP, ctx.Mac)
	}

	r.ipMu.Lock()
	if current, ok := r.ipIndex[ctx.IP]; ok && current == ctx {
		delete(r.ipIndex, ctx.IP)
		r.presence.purgeSubscriber(ctx.IP)
	}
	r.ipMu.Unlock()

	ctx.release()
	// 租约释放幂等：域名替换与连接断开两条路径都可能走到这里，
	// 重复释放不会把已重新分配给其他节点的同号 IP 归还池子。
	ctx.ipLease.Release()

	duration := ctx.DetachTime.Sub(ctx.AttachTime)
	r.logger.Info("context detached", "ctx_id", ctx.id, "domain", ctx.Domain, "duration", duration)
}

func (r *contextRegistry) lookupByDomain(domain string) (*Context, error) {
	domain, err := admit.NormalizeDomain(domain)
	if err != nil {
		return nil, err
	}

	r.domainMu.Lock()
	ctx, found := r.domainIndex[domain]
	r.domainMu.Unlock()
	if !found {
		return nil, errDomainNotFound
	}

	return ctx, nil
}

func (r *contextRegistry) lookupByIP(ip uint16) (*Context, error) {
	if ip == packet.LocalIP || ip == packet.SwitcherIP {
		return nil, errInvalidContextIP
	}

	r.ipMu.Lock()
	ctx, found := r.ipIndex[ip]
	r.ipMu.Unlock()
	if !found {
		return nil, errContextIPNotFound
	}
	return ctx, nil
}

func (r *contextRegistry) activeContexts() []*Context {
	r.ipMu.Lock()
	ctxs := make([]*Context, 0, len(r.ipIndex))
	for _, ctx := range r.ipIndex {
		ctxs = append(ctxs, ctx)
	}
	r.ipMu.Unlock()
	return ctxs
}

func (r *contextRegistry) appendRecord(ctx *Context) {
	r.recordsMu.Lock()
	defer r.recordsMu.Unlock()

	// Prune old detached records when exceeding 10000 entries
	if len(r.records) > 10000 {
		cutoff := time.Now().Add(-24 * time.Hour)
		kept := make([]*Context, 0, len(r.records)/2)
		for _, c := range r.records {
			if c.isAttached() || c.DetachTime.After(cutoff) || c.DetachTime.IsZero() {
				kept = append(kept, c)
			}
		}
		r.records = kept
	}

	r.records = append(r.records, ctx)
}

func (r *contextRegistry) formatRecords() [][]string {
	now := time.Now()
	titles := []string{
		"id", "domain", "mac", "vip",
		"state", "workTime",
		"attachTime", "detachTime"}

	ret := [][]string{titles}

	r.recordsMu.Lock()
	defer r.recordsMu.Unlock()

	for _, ctx := range r.records {
		state := "working"
		workTime := now.Sub(ctx.AttachTime)

		if !ctx.isAttached() && ctx.DetachTime.After(ctx.AttachTime) {
			state = "done"
			workTime = ctx.DetachTime.Sub(ctx.AttachTime)
		}
		vals := []string{
			fmt.Sprint(ctx.id),
			ctx.Domain,
			ctx.Mac,
			fmt.Sprint(ctx.IP),
			state,
			fmt.Sprint(workTime),
			fmt.Sprint(ctx.AttachTime),
			fmt.Sprint(ctx.DetachTime),
		}
		ret = append(ret, vals)
	}

	return ret
}
