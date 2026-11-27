package switcher

import (
	"errors"
	"log/slog"
	"sync/atomic"

	"github.com/net-agent/flex/v4/packet"
	"github.com/net-agent/flex/v4/wire"
)

var (
	errResolveDomainFailed = errors.New("resolve domain failed")
)

type packetRouter struct {
	registry *contextRegistry
	logger   *slog.Logger

	// maxStreams 是单节点活跃流数量上限（0=不限），由 Server 的 Limits 注入。
	maxStreams atomic.Int32
}

func newPacketRouter(registry *contextRegistry, logger *slog.Logger) *packetRouter {
	return &packetRouter{
		registry: registry,
		logger:   logger,
	}
}

// serve reads frames from ctx and routes them.
func (rt *packetRouter) serve(ctx *Context) error {
	for {
		f, err := ctx.readFrame()
		if err != nil {
			return err
		}
		if err := rt.checkSource(ctx, f); err != nil {
			rt.logger.Warn("drop packet with spoofed source",
				"ctx_id", ctx.id, "ctx_ip", ctx.IP, "src_ip", f.Src().IP,
				"dist_ip", f.Dst().IP, "cmd", f.Cmd())
			f.Release()
			continue
		}
		ctx.recordIncoming(f)

		if f.Dst().IP != wire.MaxIP {
			// 需要保证发送顺序，不能使用协程并行
			rt.forward(f)
			continue
		}

		// 无需保证顺序
		go rt.dispatch(ctx, f)
	}
}

// checkSource 校验入站包的源虚拟 IP 是否等于认证上下文分配的 IP。
// 没有这一步时，任意已认证节点都能伪造 SrcIP/SrcPort，向其他节点的既有流
// 注入数据（SID 由地址四元组构成，虚拟端口按序分配、可枚举）。
func (rt *packetRouter) checkSource(ctx *Context, f *wire.Frame) error {
	if f.Src().IP != ctx.IP {
		return errSourceAddress
	}
	return nil
}

// forward forwards a frame to its destination by IP lookup. On success the
// destination's Context.SendFrame takes ownership; on failure it is released.
func (rt *packetRouter) forward(f *wire.Frame) {
	dist, err := rt.registry.lookupByIP(f.Dst().IP)
	if err != nil {
		rt.logger.Warn("route frame failed", "src_ip", f.Src().IP, "dist_ip", f.Dst().IP, "cmd", f.Cmd(), "error", err)
		f.Release()
		return
	}
	if err := dist.SendFrame(f); err != nil {
		rt.logger.Warn("forward to dist failed", "src_ip", f.Src().IP, "dist_ip", f.Dst().IP, "error", err)
	}
}

// dispatch handles frames addressed to the switcher itself. It owns f.
func (rt *packetRouter) dispatch(ctx *Context, f *wire.Frame) {
	switch f.Cmd().Type() {
	case wire.Cmd(wire.CmdOpenStream):
		if !f.IsAck() {
			rt.handleOpenStream(ctx, f)
			return
		}
	case wire.Cmd(wire.CmdPingDomain):
		if f.IsAck() {
			rt.handleAckPingDomain(ctx, f)
		} else {
			rt.handlePingDomain(ctx, f)
		}
		return
	case wire.Cmd(wire.CmdSubscribePresence):
		if !f.IsAck() {
			rt.handleSubscribePresence(ctx, f)
			return
		}
	}
	f.Release()
}

// handleSubscribePresence 处理节点的 presence 订阅/退订请求。
// SrcPort 原样回传给调用方，用于请求-应答关联。
func (rt *packetRouter) handleSubscribePresence(caller *Context, f *wire.Frame) {
	req := packet.DecodeSubscribeRequest(f.Payload())
	ackPort := f.Src().Port
	f.Release()
	rt.registry.presence.handleSubscribe(caller, req, ackPort)
}

// handlePingDomain resolves a domain and forwards the ping, or responds
// directly for an empty domain. It owns f.
func (rt *packetRouter) handlePingDomain(caller *Context, f *wire.Frame) {
	domain := string(f.Payload())
	if domain == "" {
		reply, err := wire.NewControl(wire.AckPingDomain, f.Dst(), f.Src(), nil)
		if err != nil {
			f.Release()
			return
		}
		f.Release()
		if err := caller.SendFrame(reply); err != nil {
			rt.logger.Warn("ping reply failed", "ctx_id", caller.id, "domain", caller.Domain, "error", err)
		}
		return
	}

	dist, err := rt.registry.lookupByDomain(domain)
	if err != nil {
		reply, buildErr := wire.NewControl(wire.AckPingDomain, f.Dst(), f.Src(), []byte(err.Error()))
		f.Release()
		if buildErr != nil {
			return
		}
		if sendErr := caller.SendFrame(reply); sendErr != nil {
			rt.logger.Warn("ping error reply failed", "ctx_id", caller.id, "domain", caller.Domain, "error", sendErr)
		}
		return
	}

	fwd, err := wire.NewControl(
		wire.CmdPingDomain,
		f.Src(),
		wire.Addr{IP: dist.IP, Port: f.Dst().Port},
		f.Payload(),
	)
	f.Release()
	if err != nil {
		return
	}
	if err := dist.SendFrame(fwd); err != nil {
		rt.logger.Warn("ping forward failed", "ctx_id", dist.id, "domain", dist.Domain, "error", err)
	}
}

// handleAckPingDomain delivers a ping response back to the waiting caller.
// 投递成功时所有权转移给 ping 等待方；未投递（无等待者或等待方已超时）在此归还。
func (rt *packetRouter) handleAckPingDomain(caller *Context, f *wire.Frame) {
	if !caller.deliverPingResponse(f.Dst().Port, f) {
		rt.logger.Warn("port not found", "ctx_id", caller.id, "domain", caller.Domain, "port", f.Dst().Port)
		f.Release()
	}
}

// handleOpenStream resolves the destination domain and forwards the open-stream
// request. It owns f.
func (rt *packetRouter) handleOpenStream(caller *Context, f *wire.Frame) {
	if limit := rt.maxStreams.Load(); limit > 0 && caller.streamCount() > limit {
		rt.logger.Warn("stream quota exceeded",
			"ctx_id", caller.id, "domain", caller.Domain, "active", caller.streamCount(), "limit", limit)
		ack := packet.OpenStreamACK{Error: errStreamQuota.Error()}
		reply, err := wire.NewControl(wire.AckOpenStream, f.Dst(), f.Src(), ack.Encode())
		f.Release()
		if err != nil {
			return
		}
		if err := caller.SendFrame(reply); err != nil {
			rt.logger.Warn("stream quota reply failed", "ctx_id", caller.id, "error", err)
		}
		return
	}

	req := packet.DecodeOpenStreamRequest(f.Payload())

	distCtx, err := rt.registry.lookupByDomain(req.Domain)
	if err != nil {
		rt.logger.Warn("resolve domain failed", "caller_id", caller.id, "caller_domain", caller.Domain, "target_domain", req.Domain, "error", err)
		ack := packet.OpenStreamACK{Error: errResolveDomainFailed.Error()}
		reply, buildErr := wire.NewControl(wire.AckOpenStream, f.Dst(), f.Src(), ack.Encode())
		f.Release()
		if buildErr != nil {
			return
		}
		if sendErr := caller.SendFrame(reply); sendErr != nil {
			rt.logger.Warn("open-stream error reply failed", "ctx_id", caller.id, "domain", caller.Domain, "error", sendErr)
		}
		return
	}

	fwd := packet.OpenStreamRequest{Domain: caller.Domain, WindowSize: req.WindowSize}
	next, err := wire.NewControl(
		wire.CmdOpenStream,
		f.Src(),
		wire.Addr{IP: distCtx.IP, Port: f.Dst().Port},
		fwd.Encode(),
	)
	f.Release()
	if err != nil {
		return
	}
	if err := distCtx.SendFrame(next); err != nil {
		rt.logger.Warn("open-stream forward failed", "ctx_id", distCtx.id, "domain", distCtx.Domain, "error", err)
	}
}
