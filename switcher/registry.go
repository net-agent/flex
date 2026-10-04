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
		ipm:         ipm,
		logger:      logger,
		presence:    presence,
		domainIndex: make(map[string]*Context),
		ipIndex:     make(map[uint16]*Context),
	}
}

func (r *contextRegistry) attach(ctx *Context) error {
	normalized, err := admit.NormalizeDomain(ctx.Domain)
	if err != nil {
		r.logger.Warn("attach failed: invalid domain", "ctx_id", ctx.id, "domain", ctx.Domain)
		return err
	}
	ctx.Domain = normalized

	prev, acquired := r.acquireDomain(ctx)
	if !acquired {
		r.logger.Warn("attach failed: replace domain failed", "ctx_id", ctx.id, "domain", ctx.Domain)
		return errReplaceDomainFailed
	}

	if prev != nil {
		prev.release()
		r.ipm.Release(prev.IP)
	}

	ip, err := r.ipm.Allocate()
	if err != nil {
		// 域名已被 acquireDomain 认领，失败路径必须回滚，否则留下永不 serve 的僵尸占位
		r.rollbackDomainClaim(ctx)
		r.logger.Warn("attach failed: IP exhausted", "ctx_id", ctx.id, "domain", ctx.Domain, "error", err)
		return errGetFreeContextIPFailed
	}
	ctx.IP = ip

	r.ipMu.Lock()
	if _, exists := r.ipIndex[ctx.IP]; exists {
		r.ipMu.Unlock()
		r.rollbackDomainClaim(ctx)
		ctx.release()
		r.ipm.Release(ctx.IP)
		r.logger.Warn("attach failed: IP conflict", "ctx_id", ctx.id, "domain", ctx.Domain, "ip", ctx.IP)
		return errContextIPExist
	}
	r.ipIndex[ctx.IP] = ctx
	r.ipMu.Unlock()

	ctx.AttachTime = time.Now()
	r.presence.emitOnline(ctx.Domain, ctx.IP, ctx.Mac)
	r.logger.Info("context attached", "ctx_id", ctx.id, "domain", ctx.Domain, "ip", ctx.IP, "mac", ctx.Mac)
	return nil
}

// rollbackDomainClaim 在 attach 失败时撤销 acquireDomain 的域名认领。
// 仅当域名仍指向本 ctx 时才删除，避免误删并发替换者的认领。
// 失败路径上本 ctx 从未 emitOnline，因此无需补偿 presence 事件。
func (r *contextRegistry) rollbackDomainClaim(ctx *Context) {
	r.domainMu.Lock()
	if current, ok := r.domainIndex[ctx.Domain]; ok && current == ctx {
		delete(r.domainIndex, ctx.Domain)
	}
	r.domainMu.Unlock()
	ctx.setAttached(false)
}

// acquireDomain tries to claim the domain slot for newCtx.
// If the slot is occupied, pings the existing holder to check liveness;
// replaces it only if the ping fails.
// Uses optimistic locking: lock to check, unlock to ping, re-lock to verify and replace.
func (r *contextRegistry) acquireDomain(newCtx *Context) (prev *Context, ok bool) {
	r.domainMu.Lock()
	existing, loaded := r.domainIndex[newCtx.Domain]
	if !loaded {
		r.domainIndex[newCtx.Domain] = newCtx
		r.domainMu.Unlock()
		r.appendRecord(newCtx)
		newCtx.setAttached(true)
		return nil, true
	}
	r.domainMu.Unlock()

	// Ping outside the lock to avoid holding it during network I/O
	_, err := existing.ping(time.Second * 3)
	if err == nil {
		return existing, false
	}

	// Ping failed — re-lock and verify the domain still points to the same context
	r.domainMu.Lock()
	current, stillExists := r.domainIndex[newCtx.Domain]
	if stillExists && current == existing {
		r.domainIndex[newCtx.Domain] = newCtx
		r.presence.emitOffline(existing.Domain, existing.IP, existing.Mac)
		r.domainMu.Unlock()
		r.logger.Info("domain replaced", "domain", newCtx.Domain, "old_ctx_id", existing.id, "new_ctx_id", newCtx.id)
		r.detach(existing)
		r.appendRecord(newCtx)
		newCtx.setAttached(true)
		return existing, true
	}
	r.domainIndex[newCtx.Domain] = newCtx
	r.presence.emitOffline(existing.Domain, existing.IP, existing.Mac)
	r.domainMu.Unlock()
	r.logger.Info("domain replaced", "domain", newCtx.Domain, "old_ctx_id", existing.id, "new_ctx_id", newCtx.id)
	r.appendRecord(newCtx)
	newCtx.setAttached(true)
	return existing, true
}

func (r *contextRegistry) detach(ctx *Context) {
	if !ctx.isAttached() {
		return
	}

	r.domainMu.Lock()
	if current, ok := r.domainIndex[ctx.Domain]; ok && current == ctx {
		delete(r.domainIndex, ctx.Domain)
		r.presence.emitOffline(ctx.Domain, ctx.IP, ctx.Mac)
	}
	r.domainMu.Unlock()

	r.ipMu.Lock()
	if current, ok := r.ipIndex[ctx.IP]; ok && current == ctx {
		delete(r.ipIndex, ctx.IP)
		r.presence.purgeSubscriber(ctx.IP)
	}
	r.ipMu.Unlock()

	ctx.release()
	r.ipm.Release(ctx.IP)

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
