package node

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/net-agent/flex/v4/internal/idpool"
	"github.com/net-agent/flex/v4/internal/pending"
	"github.com/net-agent/flex/v4/packet"
	"github.com/net-agent/flex/v4/stream"
	"github.com/net-agent/flex/v4/wire"
)

var (
	errInvalidIPNumber       = errors.New("invalid ip number")
	errInvalidPortNumber     = errors.New("invalid port number")
	ErrLocalPortUsed         = errors.New("local port used")
	ErrWaitResponseTimeout   = errors.New("wait dial response timeout")
	ErrWriteDialPbufFailed   = errors.New("write dial buffer failed")
	ErrUnexpectedNilResponse = errors.New("unexpected nil response")
)

type Dialer struct {
	host    *Node
	portm   *idpool.Pool
	timeout time.Duration
	pending pending.Requests[*stream.Stream]
}

func (d *Dialer) init(host *Node, portm *idpool.Pool) {
	d.host = host
	d.portm = portm
	d.SetDialTimeout(time.Second * 15)
}

func (d *Dialer) SetDialTimeout(timeout time.Duration) { d.timeout = timeout }

// Dial 通过 address 信息创建新的连接
func (d *Dialer) Dial(addr string) (*stream.Stream, error) {
	isDomain, domain, ip, port, err := parseAddress(addr)
	if err != nil {
		return nil, err
	}
	if isDomain {
		return d.DialDomain(domain, port)
	}
	return d.DialIP(ip, port)
}

// DialDomain 通过 domain 信息进行 dial
func (d *Dialer) DialDomain(domain string, port uint16) (*stream.Stream, error) {
	if domain == d.host.domain || domain == "local" || domain == "localhost" {
		return d.DialIP(d.host.GetIP(), port)
	}

	req := packet.OpenStreamRequest{Domain: domain, WindowSize: uint32(d.host.effectiveWindowSize())}
	return d.dialPayload(d.host.GetIP(), wire.Addr{IP: wire.MaxIP, Port: port}, req.Encode(), domain)
}

// DialIP 通过 IP 信息进行 dial
func (d *Dialer) DialIP(ip, port uint16) (*stream.Stream, error) {
	req := packet.OpenStreamRequest{WindowSize: uint32(d.host.effectiveWindowSize())}
	remoteDomain := fmt.Sprintf("%v", ip)
	if ip == d.host.ip {
		remoteDomain = d.host.domain
	}
	return d.dialPayload(d.host.GetIP(), wire.Addr{IP: ip, Port: port}, req.Encode(), remoteDomain)
}

// dialPayload allocates a local port and sends the OpenStream control frame.
func (d *Dialer) dialPayload(srcIP uint16, dst wire.Addr, payload []byte, remoteDomain string) (*stream.Stream, error) {
	// 第一步：选择可用端口。
	srcPort, err := d.portm.Allocate()
	if err != nil {
		d.host.tracePortExhausted("shared", err) // dialer/listenHub/streamHub 共用一个端口池
		return nil, err
	}
	defer func() {
		if srcPort > 0 {
			d.portm.Release(srcPort)
		}
	}()

	// 第二步：创建用于接收 stream 的 chan。
	ch, err := d.pending.Register(srcPort)
	if err != nil {
		return nil, err
	}
	defer d.pending.Remove(srcPort)

	// 第三步：补齐 srcPort，向服务端发送 open 命令。
	f, err := wire.NewControl(
		wire.CmdOpenStream,
		wire.Addr{IP: srcIP, Port: srcPort},
		dst,
		payload,
	)
	if err != nil {
		return nil, err
	}
	if err := d.host.SendFrame(f); err != nil {
		atomic.AddInt64(&d.host.failures.DialWriteFailed, 1)
		return nil, fmt.Errorf("%w: %w", ErrWriteDialPbufFailed, err)
	}

	// 第四步：等待 ack。
	select {
	case res, ok := <-ch:
		if !ok {
			atomic.AddInt64(&d.host.failures.DialTimeout, 1)
			return nil, ErrWaitResponseTimeout
		}
		if res.Err != nil {
			atomic.AddInt64(&d.host.failures.DialRejected, 1)
			return nil, res.Err
		}
		s := res.Val
		s.SetRemoteDomain(remoteDomain)
		srcPort = 0 // 端口所有权转移给 stream，阻止 defer 释放
		return s, nil
	case <-time.After(d.timeout):
		atomic.AddInt64(&d.host.failures.DialTimeout, 1)
		return nil, ErrWaitResponseTimeout
	}
}

// dialPbuf is the legacy packet.Buffer entry point kept for tests and source
// compatibility. Payload ownership stays with the caller.
func (d *Dialer) dialPbuf(pbuf *packet.Buffer) (*stream.Stream, error) {
	remoteDomain := packet.DecodeOpenStreamRequest(pbuf.Payload).Domain
	if remoteDomain == "" {
		distIP := pbuf.DistIP()
		if distIP == d.host.ip {
			remoteDomain = d.host.domain
		} else {
			remoteDomain = fmt.Sprintf("%v", distIP)
		}
	}
	return d.dialPayload(d.host.GetIP(), wire.Addr{IP: pbuf.DistIP(), Port: pbuf.DistPort()}, pbuf.Payload, remoteDomain)
}

// negotiateWindow 钳制对端应答的窗口值。
func (d *Dialer) negotiateWindow(raw uint32) int32 {
	local := d.host.effectiveWindowSize()
	if raw == 0 || raw > uint32(local) {
		return local
	}
	return int32(raw)
}

// handleAckOpenStreamFrame processes AckOpenStream. It owns f.
func (d *Dialer) handleAckOpenStreamFrame(f *wire.Frame) error {
	defer f.Release()

	evKey := f.Dst().Port
	ack := packet.DecodeOpenStreamACK(f.Payload())

	if !ack.OK {
		err := d.pending.Complete(evKey, nil, errors.New(ack.Error))
		if err != nil {
			d.host.logger.Warn("dispatch ack-msg failed", "error", err)
		}
		return nil
	}

	negotiatedWindowSize := d.negotiateWindow(ack.WindowSize)

	s := stream.NewDialStreamWithEndpoint(
		d.host,
		d.host.domain, f.Dst().IP, f.Dst().Port,
		"", f.Src().IP, f.Src().Port,
		negotiatedWindowSize,
	)

	err := d.host.streamHub.attachStream(s, f.SID().Uint64())
	if err != nil {
		d.host.logger.Warn("attach stream to node failed", "error", err)
		err = d.pending.Complete(evKey, nil, err)
		if err != nil {
			d.host.logger.Warn("dispatch ack-err failed", "error", err)
		}
		return nil
	}

	err = d.pending.Complete(evKey, s, nil)
	if err != nil {
		if errors.Is(err, pending.ErrNotFound) {
			// Dial caller already timed out/cancelled. Reclaim the stream
			// immediately to avoid orphaned entries staying in StreamHub.
			_ = s.CloseRead()
			_ = s.CloseWrite()
			d.host.logger.Warn("late open-stream ack discarded", "sid", f.SID().String())
			return nil
		}
		d.host.logger.Warn("dispatch ack failed", "error", err)
	}
	return nil
}

// handleAckOpenStream is the legacy packet.Buffer adapter.
func (d *Dialer) handleAckOpenStream(pbuf *packet.Buffer) {
	f, err := bufferToFrame(pbuf)
	if err != nil {
		return
	}
	_ = d.handleAckOpenStreamFrame(f)
}

// parseAddress 对字符串地址进行解析
func parseAddress(addr string) (isDomain bool, domain string, ip uint16, port uint16, err error) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return
	}

	intPort, err := strconv.Atoi(p)
	if err != nil {
		return
	}
	if intPort < 0 || intPort > 0xffff {
		err = errInvalidPortNumber
		return
	}
	port = uint16(intPort)

	intIP, err := strconv.Atoi(h)
	if err != nil {
		// treat as domain
		return true, h, 0, port, nil
	}
	if intIP < 0 || intIP > int(packet.MaxIP) {
		err = errInvalidIPNumber
		return
	}
	return false, "", uint16(intIP), port, nil
}
