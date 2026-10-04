package switcher

import (
	"errors"
	"log/slog"
	"net"
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
	s.router = newPacketRouter(reg, newModuleLogger(logger, cfg.Router, "router"))
	return s
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

		pconn := packet.NewWithConn(conn)
		if s.enableFairConn.Load() {
			s.logger.Info("enabling fair connection scheduling")
			pconn = sched.NewFairConn(pconn)
		}
		go s.ServeConn(pconn)
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
func (s *Server) ServeConn(pc packet.Conn) error {
	defer pc.Close()

	// 第一步：交换版本和签名信息，保证版本一致与认证安全
	req, err := admit.Accept(pc, s.password)
	if err != nil {
		resp := admit.NewErrResponse(-1, "handshake rejected")
		resp.WriteTo(pc, s.password)
		s.logger.Warn("handshake failed", "error", err)
		return err
	}

	// 第二步：将ctx映射到map中
	ctx := NewContext(int(s.nextCtxID.Add(1)), pc, req.Domain, req.Mac, s.ctxLogger)
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
		return errHandlePCWriteFailed
	}

	// 第四步：绑定底层连接
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
