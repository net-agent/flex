package switcher

import (
	"errors"
	"log/slog"

	"github.com/net-agent/flex/v3/packet"
)

var (
	errResolveDomainFailed = errors.New("resolve domain failed")
)

type packetRouter struct {
	registry *contextRegistry
	logger   *slog.Logger
}

func newPacketRouter(registry *contextRegistry, logger *slog.Logger) *packetRouter {
	return &packetRouter{
		registry: registry,
		logger:   logger,
	}
}

// serve reads packets from ctx in a loop and routes each one.
func (rt *packetRouter) serve(ctx *Context) error {
	for {
		pbuf, err := ctx.readBuffer()
		if err != nil {
			return err
		}
		ctx.recordIncoming(pbuf)

		if pbuf.DistIP() != packet.SwitcherIP {
			// 需要保证发送顺序，不能使用协程并行
			rt.forward(pbuf)
			continue
		}

		// 无需保证顺序
		go rt.dispatch(ctx, pbuf)
	}
}

// forward forwards a packet to its destination by IP lookup.
// pbuf 在成功 enqueue 后所有权转移给目标 ctx；路由或入队失败时在此归还。
func (rt *packetRouter) forward(pbuf *packet.Buffer) {
	dist, err := rt.registry.lookupByIP(pbuf.DistIP())
	if err != nil {
		rt.logger.Warn("route pbuf failed", "src_ip", pbuf.SrcIP(), "dist_ip", pbuf.DistIP(), "cmd", pbuf.CmdType(), "error", err)
		packet.PutBuffer(pbuf)
		return
	}

	err = dist.enqueueForward(pbuf)
	if err != nil {
		rt.logger.Warn("forward to dist failed", "src_ip", pbuf.SrcIP(), "dist_ip", pbuf.DistIP(), "error", err)
		packet.PutBuffer(pbuf)
	}
}

// dispatch handles packets addressed to the switcher itself.
func (rt *packetRouter) dispatch(ctx *Context, pbuf *packet.Buffer) {
	switch pbuf.CmdType() {
	case packet.CmdOpenStream:
		if !pbuf.IsACK() {
			rt.handleOpenStream(ctx, pbuf)
		}

	case packet.CmdPingDomain:
		if pbuf.IsACK() {
			rt.handleAckPingDomain(ctx, pbuf)
		} else {
			rt.handlePingDomain(ctx, pbuf)
		}

	case packet.CmdSubscribePresence:
		if !pbuf.IsACK() {
			rt.handleSubscribePresence(ctx, pbuf)
		}
	}
}

// handleSubscribePresence 处理节点的 presence 订阅/退订请求。
// SrcPort 原样回传给调用方，用于请求-应答关联。
// pbuf 是终点消费（同步 decode 后不再引用），在此归还。
func (rt *packetRouter) handleSubscribePresence(caller *Context, pbuf *packet.Buffer) {
	req := packet.DecodeSubscribeRequest(pbuf.Payload)
	ackPort := pbuf.SrcPort()
	packet.PutBuffer(pbuf) // 终点消费完毕，归还（Put 后 Head 清零，字段须先取）
	rt.registry.presence.handleSubscribe(caller, req, ackPort)
}

// handlePingDomain resolves a domain and forwards the ping, or responds directly for empty domain.
// pbuf 经改写后回投/转发（所有权随之转移）；入队失败的分支在此归还。
func (rt *packetRouter) handlePingDomain(caller *Context, pbuf *packet.Buffer) {
	domain := string(pbuf.Payload)
	if domain == "" {
		pbuf.SwapSrcDist()
		pbuf.SetCmd(pbuf.Cmd() | packet.CmdACKFlag)
		_ = pbuf.SetPayload(nil)
		if err := caller.enqueueForward(pbuf); err != nil {
			rt.logger.Warn("ping reply enqueue failed", "ctx_id", caller.id, "domain", caller.Domain, "error", err)
			packet.PutBuffer(pbuf)
		}
		return
	}

	dist, err := rt.registry.lookupByDomain(string(pbuf.Payload))
	if err != nil {
		pbuf.SwapSrcDist()
		pbuf.SetCmd(pbuf.Cmd() | packet.CmdACKFlag)
		_ = pbuf.SetPayload([]byte(err.Error()))
		if err := caller.enqueueForward(pbuf); err != nil {
			rt.logger.Warn("ping error reply enqueue failed", "ctx_id", caller.id, "domain", caller.Domain, "error", err)
			packet.PutBuffer(pbuf)
		}
		return
	}

	pbuf.SetDistIP(dist.IP)
	if err := dist.enqueueForward(pbuf); err != nil {
		rt.logger.Warn("ping forward enqueue failed", "ctx_id", dist.id, "domain", dist.Domain, "error", err)
		packet.PutBuffer(pbuf)
	}
}

// handleAckPingDomain delivers a ping response back to the waiting caller.
// 投递成功时所有权转移给 ping 等待方；未投递（无等待者或等待方已超时）在此归还。
func (rt *packetRouter) handleAckPingDomain(caller *Context, pbuf *packet.Buffer) {
	if !caller.deliverPingResponse(pbuf.DistPort(), pbuf) {
		rt.logger.Warn("port not found", "ctx_id", caller.id, "domain", caller.Domain, "port", pbuf.DistPort())
		packet.PutBuffer(pbuf)
	}
}

// handleOpenStream resolves the destination domain and forwards the open-stream request.
// pbuf 经改写后回投/转发（所有权随之转移）；入队失败的分支在此归还。
func (rt *packetRouter) handleOpenStream(caller *Context, pbuf *packet.Buffer) {
	req := packet.DecodeOpenStreamRequest(pbuf.Payload)

	distCtx, err := rt.registry.lookupByDomain(req.Domain)
	if err != nil {
		rt.logger.Warn("resolve domain failed", "caller_id", caller.id, "caller_domain", caller.Domain, "target_domain", req.Domain, "error", err)
		ack := packet.OpenStreamACK{Error: errResolveDomainFailed.Error()}
		pbuf.SetCmd(packet.AckOpenStream)
		pbuf.SwapSrcDist()
		_ = pbuf.SetPayload(ack.Encode())
		pbuf.SetSrcIP(0)
		if err := caller.enqueueForward(pbuf); err != nil {
			rt.logger.Warn("open-stream error reply enqueue failed", "ctx_id", caller.id, "domain", caller.Domain, "error", err)
			packet.PutBuffer(pbuf)
		}
		return
	}

	fwd := packet.OpenStreamRequest{Domain: caller.Domain, WindowSize: req.WindowSize}
	pbuf.SetDistIP(distCtx.IP)
	_ = pbuf.SetPayload(fwd.Encode())
	if err := distCtx.enqueueForward(pbuf); err != nil {
		rt.logger.Warn("open-stream forward enqueue failed", "ctx_id", distCtx.id, "domain", distCtx.Domain, "error", err)
		packet.PutBuffer(pbuf)
	}
}
