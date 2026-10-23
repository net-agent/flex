package switcher

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v3/internal/idpool"
	"github.com/net-agent/flex/v3/internal/sched"
	"github.com/net-agent/flex/v3/link"
	"github.com/net-agent/flex/v3/packet"
)

var (
	errHandlePCWriteFailed = errors.New("write to packet.Conn failed")
)

func linkTransport(pc packet.Conn) link.Transport {
	if lt, ok := pc.(link.Transport); ok {
		return lt
	}
	return link.NewPacketTransport(pc)
}

type OnContextStartHandler func(ctx *Context)
type OnContextStopHandler func(ctx *Context, duration time.Duration)

type Server struct {
	listenerMu sync.Mutex
	listener   net.Listener
	password   string
	nextCtxID  atomic.Int32
	startedAt  time.Time

	// fairMu / fairCfg 保护新连接的 link 写调度策略（SetSchedulerConfig）。
	fairMu   sync.RWMutex
	fairCfg  sched.SchedulerConfig
	acceptor *link.Acceptor

	// limits 是准入与资源配额，必须在 Serve 之前通过 SetLimits 设置。
	limits    Limits
	connSem   chan struct{} // nil 表示不限连接数
	hsLimiter *ipLimiter

	OnContextStart OnContextStartHandler
	OnContextStop  OnContextStopHandler

	registry  *contextRegistry
	router    *packetRouter
	presence  *presenceCenter
	logger    *slog.Logger
	ctxLogger *slog.Logger
}

type ServerError struct {
	Message  string // 错误描述信息
	RawError string // 产生的原始错误
}

// StatsResponse 汇总 Switcher 的运行时统计
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

func NewServer(password string, logger *slog.Logger, logCfg *LogConfig) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	cfg := DefaultLogConfig()
	if logCfg != nil {
		cfg = *logCfg
	}

	ipm, _ := idpool.New(1, packet.MaxIP-1)
	regLogger := newModuleLogger(logger, cfg.Registry, "registry")
	presence := newPresenceCenter(newModuleLogger(logger, cfg.Presence, "presence"))
	reg := newContextRegistry(ipm, regLogger, presence)

	s := &Server{
		password:  password,
		startedAt: time.Now(),
		registry:  reg,
		presence:  presence,
		logger:    newModuleLogger(logger, cfg.Server, "server"),
		ctxLogger: newModuleLogger(logger, cfg.Context, "context"),
	}
	s.fairCfg = sched.DefaultSchedulerConfig()
	s.acceptor = link.NewAcceptor(link.AcceptConfig{PSK: password, Scheduler: s.fairCfg})
	s.router = newPacketRouter(reg, newModuleLogger(logger, cfg.Router, "router"))
	s.limits = DefaultLimits()
	s.applyLimits()
	return s
}

// SetLimits 覆盖默认的准入与资源配额。必须在 Serve 之前调用；
// 运行期修改不会同步已建立的连接。
func (s *Server) SetLimits(l Limits) {
	s.limits = l
	s.applyLimits()
}

func (s *Server) applyLimits() {
	if n := s.limits.MaxConnections; n > 0 {
		s.connSem = make(chan struct{}, n)
	} else {
		s.connSem = nil
	}
	if s.limits.HandshakeRatePerIP > 0 {
		s.hsLimiter = newIPLimiter(s.limits.HandshakeRatePerIP, s.limits.HandshakeBurstPerIP)
	} else {
		s.hsLimiter = nil
	}
	if s.router != nil {
		s.router.maxStreams.Store(s.limits.MaxStreamsPerContext)
	}
	if s.presence != nil {
		s.presence.maxSubscriptions.Store(int32(s.limits.MaxPresenceSubscriptions))
	}
}

// acquireConnSlot 尝试占用一个连接名额。限流采用非阻塞语义：
// 超额直接拒绝，避免 accept 循环在满负载下堆积 goroutine。
func (s *Server) acquireConnSlot() bool {
	if s.connSem == nil {
		return true
	}
	select {
	case s.connSem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (s *Server) releaseConnSlot() {
	if s.connSem == nil {
		return
	}
	select {
	case <-s.connSem:
	default:
	}
}

// SetEnableFairConn is retained for API compatibility. Every link now uses the
// fair writer; this method has no effect.
//
// Deprecated: fair scheduling is always enabled by link.Link.
func (s *Server) SetEnableFairConn(enable bool) {}

func (s *Server) GetStats() *StatsResponse {
	ctxs := s.registry.activeContexts()
	return &StatsResponse{
		ActiveConnections: len(ctxs),
		TotalContexts:     int64(s.nextCtxID.Load()),
		UptimeSeconds:     int64(time.Since(s.startedAt).Seconds()),
	}
}

func (s *Server) GetClients() []ClientInfo {
	ctxs := s.registry.activeContexts()
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

func (s *Server) Serve(l net.Listener) error {
	s.listenerMu.Lock()
	if s.listener != nil {
		s.listenerMu.Unlock()
		return nil
	}
	s.listener = l
	s.listenerMu.Unlock()

	for {
		conn, err := l.Accept()
		if err != nil {
			s.listenerMu.Lock()
			closed := s.listener == nil
			s.listenerMu.Unlock()
			if closed {
				return nil
			}
			return err
		}

		// ServeConn 内部完成 v4 握手并创建 link；TCP 与 WebSocket
		// 路径行为一致。
		go s.ServeConn(packet.NewWithConn(conn))
	}
}

func (s *Server) Close() error {
	s.listenerMu.Lock()
	l := s.listener
	s.listener = nil
	s.listenerMu.Unlock()

	// 断开所有活跃连接：detach 完成索引清理、presence purge 与 conn 释放
	// （不能只调 ctx.release——它会先置 attached=false，使后续 detach 空转）
	for _, ctx := range s.registry.activeContexts() {
		s.registry.detach(ctx)
	}
	s.presence.stop()

	if l != nil {
		return l.Close()
	}
	return nil
}

// ServeConn handles the full lifecycle of a single packet connection:
// handshake, context registration, packet loop, and cleanup.
//
// 该函数是单条连接的服务边界，必须对输入驱动的 panic 兜底：
// 任一客户端都可以发送任意字节，读路径上的越界/断言失败不得打崩整个进程。
// recover 只覆盖本连接的 goroutine，业务回调中的 panic 仍应视为 bug 向上暴露。
func (s *Server) ServeConn(pc packet.Conn) (err error) {
	defer pc.Close()
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("switcher: recovered from panic: %v", r)
			s.logger.Error("panic recovered in ServeConn", "panic", r, "stack", string(debug.Stack()))
		}
	}()

	// 第零步：准入控制。握手在认证之前，是唯一可用的限流维度；
	// 连接数配额在握手前拦截，避免半开连接把 goroutine/内存吃满。
	ip := remoteIP(pc.GetRawConn())
	if s.hsLimiter != nil && !s.hsLimiter.allow(ip) {
		s.logger.Warn("handshake rate limited", "ip", ip)
		return errRateLimited
	}
	if !s.acquireConnSlot() {
		s.logger.Warn("connection limit reached, reject", "ip", ip)
		return errTooManyConnections
	}
	defer s.releaseConnSlot()

	// 第一步：v4 握手（密钥协商 + 认证 + 抗重放）。握手失败时直接关闭，
	// 不返回错误响应，避免给探测者提供 oracle。
	tr := linkTransport(pc)
	pending, hs, err := s.acceptor.Accept(tr)
	if err != nil {
		s.logger.Warn("handshake failed", "ip", ip, "error", err)
		return err
	}

	// 第二步：注册 Context 并分配虚拟 IP。Context 尚未绑定 link/ready，
	// 其他节点此时命中它时会等待握手的应答窗口。
	ctx := newContext(int(s.nextCtxID.Add(1)), nil, hs.Domain, hs.Mac, s.ctxLogger)
	defer ctx.release()

	if err := s.registry.attach(ctx); err != nil {
		s.logger.Warn("attach context failed", "domain", hs.Domain, "error", err)
		return err
	}
	defer s.registry.detach(ctx)

	// 第三步：发送握手应答（含分配的 IP），然后切换到安全 Link。
	lnk, err := pending.Respond(ctx.IP)
	if err != nil {
		s.logger.Warn("response client failed", "domain", ctx.Domain, "mac", ctx.Mac, "error", err)
		return fmt.Errorf("%w: %w", errHandlePCWriteFailed, err)
	}
	ctx.setLink(lnk)
	ctx.markReady()

	// 记录服务时长
	start := time.Now()
	if s.OnContextStart != nil {
		s.OnContextStart(ctx)
	}

	err = s.router.serve(ctx)

	// 计算并打印时长
	dur := time.Since(start).Round(time.Second)
	if s.OnContextStop != nil {
		s.OnContextStop(ctx, dur)
	}

	if err != nil {
		s.logger.Info("context serve ended", "ctx_id", ctx.id, "domain", ctx.Domain, "error", err)
	}

	return err
}
