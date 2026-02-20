package switcher

import (
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v2/numsrc"
	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/packet/sched"
	"github.com/net-agent/flex/v2/vars"
	"github.com/net-agent/flex/v2/warning"
)

var (
	errReplaceDomainFailed    = errors.New("replace domain context failed")
	errContextIPExist         = errors.New("context ip exist")
	errGetFreeContextIPFailed = errors.New("get unused ip failed")
)

type Server struct {
	listenerMu sync.Mutex
	listener   net.Listener
	password   string
	ipm        *numsrc.Manager
	nextCtxID  int32

	domainMu    sync.Mutex
	nodeDomains map[string]*Context

	ipMu    sync.Mutex
	nodeIps map[uint16]*Context

	ctxRecords    []*Context
	ctxRecordsMut sync.Mutex
	warning.Guard
}

type ServerError struct {
	Message  string // 错误描述信息
	RawError string // 产生的原始错误
}

// Responses
type StatsResponse struct {
	ActiveConnections int   `json:"active_connections"`
	TotalContexts     int64 `json:"total_contexts"`
	UptimeSeconds     int64 `json:"uptime_seconds"`
}

type ClientInfo struct {
	ID          int         `json:"id"`
	Domain      string      `json:"domain"`
	IP          uint16      `json:"ip"`
	Mac         string      `json:"mac"`
	ConnectedAt time.Time   `json:"connected_at"`
	Stats       ClientStats `json:"stats"`
}

type ClientStats struct {
	StreamCount   int32  `json:"active_streams"`
	BytesReceived int64  `json:"bytes_in"`
	BytesSent     int64  `json:"bytes_out"`
	LastRTT       string `json:"rtt"`
	LastRTTMs     int64  `json:"rtt_ms"`
}

func NewServer(password string) *Server {
	ipm, _ := numsrc.NewManager(1, 1, vars.MaxIP-1)

	return &Server{
		password:    password,
		ipm:         ipm,
		nodeDomains: make(map[string]*Context),
		nodeIps:     make(map[uint16]*Context),
	}
}

func (s *Server) GetStats() *StatsResponse {
	ctxs := s.GetActiveContexts()
	return &StatsResponse{
		ActiveConnections: len(ctxs),
		TotalContexts:     int64(atomic.LoadInt32(&s.nextCtxID)),
	}
}

func (s *Server) GetClients() []ClientInfo {
	ctxs := s.GetActiveContexts()
	infos := make([]ClientInfo, 0, len(ctxs))

	for _, ctx := range ctxs {
		rttNs := atomic.LoadInt64(&ctx.Stats.LastRTT)
		rtt := time.Duration(rttNs)

		info := ClientInfo{
			ID:          ctx.id,
			Domain:      ctx.Domain,
			IP:          ctx.IP,
			Mac:         ctx.Mac,
			ConnectedAt: ctx.AttachTime,
			Stats: ClientStats{
				StreamCount:   atomic.LoadInt32(&ctx.Stats.StreamCount),
				BytesReceived: atomic.LoadInt64(&ctx.Stats.BytesReceived),
				BytesSent:     atomic.LoadInt64(&ctx.Stats.BytesSent),
				LastRTT:       rtt.String(),
				LastRTTMs:     rtt.Milliseconds(),
			},
		}
		infos = append(infos, info)
	}
	return infos
}

func (s *Server) Run(l net.Listener) {
	s.listenerMu.Lock()
	if s.listener != nil {
		s.listenerMu.Unlock()
		return
	}
	s.listener = l
	s.listenerMu.Unlock()

	for {
		conn, err := l.Accept()
		if err != nil {
			return
		}

		// Wrap accepted connection with FairConn to ensure fair write scheduling
		pconn := sched.NewFairConn(packet.NewWithConn(conn))
		go s.HandlePacketConn(pconn, nil, nil)
	}
}

func (s *Server) Close() error {
	s.listenerMu.Lock()
	l := s.listener
	s.listenerMu.Unlock()
	if l != nil {
		return l.Close()
	}
	return nil
}

// AttachCtx 附加上下文
func (s *Server) AttachCtx(ctx *Context) error {
	old, replaced := s.replaceDomain(ctx)
	if !replaced {
		// 当前存在同名的context，并且通过了ping测试
		return errReplaceDomainFailed
	}

	if old != nil {
		s.clearCtx(old)
	}

	ip, err := s.ipm.GetFreeNumberSrc()
	if err != nil {
		return errGetFreeContextIPFailed
	}
	ctx.IP = ip

	s.ipMu.Lock()
	if _, exists := s.nodeIps[ctx.IP]; exists {
		s.ipMu.Unlock()
		// 该IP已经存在绑定，并且还未清理
		s.domainMu.Lock()
		delete(s.nodeDomains, ctx.Domain)
		s.domainMu.Unlock()
		s.clearCtx(ctx)
		return errContextIPExist
	}
	s.nodeIps[ctx.IP] = ctx
	s.ipMu.Unlock()

	ctx.AttachTime = time.Now()
	return nil
}

// replaceDomain 如果存在同名的context，进行检测
// Uses optimistic locking: lock to check, unlock to ping, re-lock to verify and replace.
func (s *Server) replaceDomain(newCtx *Context) (curr *Context, replaceOK bool) {
	s.domainMu.Lock()
	existing, loaded := s.nodeDomains[newCtx.Domain]
	if !loaded {
		// 不存在重名情况（绝大多数正常情况）
		s.nodeDomains[newCtx.Domain] = newCtx
		s.domainMu.Unlock()
		s.pushCtxRecord(newCtx)
		newCtx.setAttached(true)
		return nil, true
	}
	s.domainMu.Unlock()

	// Ping outside the lock to avoid holding it during network I/O
	_, err := existing.Ping(time.Second * 3)
	if err == nil {
		// Existing context is alive, reject the new one
		return existing, false
	}

	// Ping failed — re-lock and verify the domain still points to the same context
	s.domainMu.Lock()
	current, stillExists := s.nodeDomains[newCtx.Domain]
	if stillExists && current == existing {
		// Still the same stale context, safe to replace
		s.nodeDomains[newCtx.Domain] = newCtx
		s.domainMu.Unlock()
		s.DetachCtx(existing)
		s.pushCtxRecord(newCtx)
		newCtx.setAttached(true)
		return existing, true
	}
	// Someone else already replaced it — try again with simple store
	s.nodeDomains[newCtx.Domain] = newCtx
	s.domainMu.Unlock()
	s.pushCtxRecord(newCtx)
	newCtx.setAttached(true)
	return existing, true
}

func (s *Server) pushCtxRecord(ctx *Context) {
	s.ctxRecordsMut.Lock()
	defer s.ctxRecordsMut.Unlock()

	// Prune old detached records when exceeding 10000 entries
	if len(s.ctxRecords) > 10000 {
		cutoff := time.Now().Add(-24 * time.Hour)
		kept := make([]*Context, 0, len(s.ctxRecords)/2)
		for _, c := range s.ctxRecords {
			if c.isAttached() || c.DetachTime.After(cutoff) || c.DetachTime.IsZero() {
				kept = append(kept, c)
			}
		}
		s.ctxRecords = kept
	}

	s.ctxRecords = append(s.ctxRecords, ctx)
}

func (s *Server) GetCtxRecords() [][]string {
	now := time.Now()
	titles := []string{
		"id", "domain", "mac", "vip",
		"state", "workTime",
		"attachTime", "detachTime"}

	ret := [][]string{titles}

	s.ctxRecordsMut.Lock()
	defer s.ctxRecordsMut.Unlock()

	for _, ctx := range s.ctxRecords {
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

// GetActiveContexts returns a snapshot of all active contexts
func (s *Server) GetActiveContexts() []*Context {
	s.ipMu.Lock()
	ctxs := make([]*Context, 0, len(s.nodeIps))
	for _, ctx := range s.nodeIps {
		ctxs = append(ctxs, ctx)
	}
	s.ipMu.Unlock()
	return ctxs
}

// DetachCtx 分离上下文
func (s *Server) DetachCtx(ctx *Context) {
	if !ctx.isAttached() {
		return
	}

	s.domainMu.Lock()
	// Verify ctx still owns the domain before deleting
	if current, ok := s.nodeDomains[ctx.Domain]; ok && current == ctx {
		delete(s.nodeDomains, ctx.Domain)
	}
	s.domainMu.Unlock()

	s.ipMu.Lock()
	if current, ok := s.nodeIps[ctx.IP]; ok && current == ctx {
		delete(s.nodeIps, ctx.IP)
	}
	s.ipMu.Unlock()

	s.clearCtx(ctx)
}

func (s *Server) clearCtx(ctx *Context) {
	c := ctx.getConn()
	if c != nil {
		c.Close()
	}
	ctx.setConn(nil)
	ctx.setAttached(false)
	atomic.StoreInt32(&ctx.Stats.StreamCount, 0)
	ctx.DetachTime = time.Now()
	s.ipm.ReleaseNumberSrc(ctx.IP)
}
