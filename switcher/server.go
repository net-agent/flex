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

	"github.com/net-agent/flex/v3/internal/admit"
	"github.com/net-agent/flex/v3/internal/idpool"
	"github.com/net-agent/flex/v3/internal/sched"
	"github.com/net-agent/flex/v3/packet"
)

var (
	errHandlePCWriteFailed = errors.New("write to packet.Conn failed")
)

type OnContextStartHandler func(ctx *Context)
type OnContextStopHandler func(ctx *Context, duration time.Duration)

type Server struct {
	listenerMu sync.Mutex
	listener   net.Listener
	password   string
	nextCtxID  atomic.Int32
	startedAt  time.Time

	enableFairConn atomic.Bool

	// fairMu / fairCfg 保护新连接的 FairConn 调度策略（SetSchedulerConfig）。
	fairMu  sync.RWMutex
	fairCfg sched.SchedulerConfig

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
	s.enableFairConn.Store(true)
	s.fairCfg = sched.DefaultSchedulerConfig()
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

// SetEnableFairConn controls whether incoming connections are wrapped with
// sched.FairConn for fair stream scheduling. Default is true (enabled).
func (s *Server) SetEnableFairConn(enable bool) {
	s.enableFairConn.Store(enable)
}

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

		// FairConn 在 ServeConn 完成握手应答后启用（见第四步），
		// 这样 TCP 与直接调用 ServeConn 的 WebSocket 路径行为一致，
		// 且握手写失败仍能同步返回给调用方。
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

	// 第一步：交换版本和签名信息，保证版本一致与认证安全
	req, err := admit.Accept(pc, s.password)
	if err != nil {
		resp := admit.NewErrResponse(-1, "handshake rejected")
		resp.WriteTo(pc, s.password)
		s.logger.Warn("handshake failed", "error", err)
		return err
	}

	// 第二步：将ctx映射到map中。
	// NewContext 会启动 runForwardLoop，因此从这一刻起必须保证 release 一定执行，
	// 否则 attach 失败或应答写失败都会泄漏一个 goroutine 与 Context。
	// 握手应答必须先于任何转发写出：ctx 在 attach 后即可被其他节点按域名/IP 命中，
	// 因此先以 nil conn 注册（转发会失败丢弃），应答写完后再绑定 conn。
	ctx := NewContext(int(s.nextCtxID.Add(1)), nil, req.Domain, req.Mac, s.ctxLogger)
	defer ctx.release()

	err = s.registry.attach(ctx)
	if err != nil {
		resp := admit.NewErrResponse(-2, "handshake rejected")
		resp.WriteTo(pc, s.password)
		s.logger.Warn("attach context failed", "domain", req.Domain, "error", err)
		return err
	}
	defer s.registry.detach(ctx)

	// 第三步：应答客户端
	resp := admit.NewOKResponse(ctx.IP)
	err = resp.WriteTo(pc, s.password)
	if err != nil {
		s.logger.Warn("response client failed", "domain", ctx.Domain, "mac", ctx.Mac, "error", err)
		return fmt.Errorf("%w: %w", errHandlePCWriteFailed, err)
	}

	// 第四步：数据面启用 FairConn。
	// 握手阶段保持裸连接：应答写失败必须同步暴露给调用方；
	// 直接调用 ServeConn 的 WebSocket 场景也在此获得公平调度与限流配置。
	if s.enableFairConn.Load() {
		if _, ok := pc.(*sched.FairConn); !ok {
			fc := sched.NewFairConnWithConfig(pc, s.schedulerConfig())
			defer fc.Close()
			pc = fc
		}
	}
	ctx.setConn(pc)

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
