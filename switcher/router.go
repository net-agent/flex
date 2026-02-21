package switcher

import (
	"errors"
	"log/slog"

	"github.com/net-agent/flex/v2/packet"
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
func (rt *packetRouter) forward(pbuf *packet.Buffer) {
	dist, err := rt.registry.lookupByIP(pbuf.DistIP())
	if err != nil {
		rt.logger.Warn("route pbuf failed", "src_ip", pbuf.SrcIP(), "dist_ip", pbuf.DistIP(), "cmd", pbuf.CmdType(), "error", err)
		return
	}

	err = dist.enqueueForward(pbuf)
	if err != nil {
		rt.logger.Warn("forward to dist failed", "src_ip", pbuf.SrcIP(), "dist_ip", pbuf.DistIP(), "error", err)
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
	}
}

// handlePingDomain resolves a domain and forwards the ping, or responds directly for empty domain.
func (rt *packetRouter) handlePingDomain(caller *Context, pbuf *packet.Buffer) {
	domain := string(pbuf.Payload)
	if domain == "" {
		pbuf.SwapSrcDist()
		pbuf.SetCmd(pbuf.Cmd() | packet.CmdACKFlag)
		pbuf.SetPayload(nil)
		if err := caller.writeBuffer(pbuf); err != nil {
			rt.logger.Warn("ping reply write failed", "ctx_id", caller.id, "domain", caller.Domain, "error", err)
		}
		return
	}

	dist, err := rt.registry.lookupByDomain(string(pbuf.Payload))
	if err != nil {
		pbuf.SwapSrcDist()
		pbuf.SetCmd(pbuf.Cmd() | packet.CmdACKFlag)
		pbuf.SetPayload([]byte(err.Error()))
		if err := caller.writeBuffer(pbuf); err != nil {
			rt.logger.Warn("ping error reply write failed", "ctx_id", caller.id, "domain", caller.Domain, "error", err)
		}
		return
	}

	pbuf.SetDistIP(dist.IP)
	if err := dist.writeBuffer(pbuf); err != nil {
		rt.logger.Warn("ping forward write failed", "ctx_id", dist.id, "domain", dist.Domain, "error", err)
	}
}

// handleAckPingDomain delivers a ping response back to the waiting caller.
func (rt *packetRouter) handleAckPingDomain(caller *Context, pbuf *packet.Buffer) {
	if !caller.deliverPingResponse(pbuf.DistPort(), pbuf) {
		rt.logger.Warn("port not found", "ctx_id", caller.id, "domain", caller.Domain, "port", pbuf.DistPort())
	}
}

// handleOpenStream resolves the destination domain and forwards the open-stream request.
func (rt *packetRouter) handleOpenStream(caller *Context, pbuf *packet.Buffer) {
	req := packet.DecodeOpenStreamRequest(pbuf.Payload)

	distCtx, err := rt.registry.lookupByDomain(req.Domain)
	if err != nil {
		rt.logger.Warn("resolve domain failed", "caller_id", caller.id, "caller_domain", caller.Domain, "target_domain", req.Domain, "error", err)
		ack := packet.OpenStreamACK{Error: errResolveDomainFailed.Error()}
		pbuf.SetCmd(packet.AckOpenStream)
		pbuf.SwapSrcDist()
		pbuf.SetPayload(ack.Encode())
		pbuf.SetSrcIP(0)
		if err := caller.writeBuffer(pbuf); err != nil {
			rt.logger.Warn("open-stream error reply write failed", "ctx_id", caller.id, "domain", caller.Domain, "error", err)
		}
		return
	}

	fwd := packet.OpenStreamRequest{Domain: caller.Domain, WindowSize: req.WindowSize}
	pbuf.SetDistIP(distCtx.IP)
	pbuf.SetPayload(fwd.Encode())
	if err := distCtx.writeBuffer(pbuf); err != nil {
		rt.logger.Warn("open-stream forward write failed", "ctx_id", distCtx.id, "domain", distCtx.Domain, "error", err)
	}
}
