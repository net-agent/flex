package admit

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/net-agent/flex/v2/packet"
)

const (
	handshakeTimeout = 10 * time.Second
	maxTimestampSkew = 5 * time.Minute
)

var (
	ErrInvalidPassword  = errors.New("invalid password")
	ErrInvalidDomain    = errors.New("invalid domain")
	ErrVersionMismatch  = errors.New("version mismatch")
	ErrTimestampExpired = errors.New("timestamp expired")
)

func Handshake(pc packet.Conn, domain, mac, password string) (uint16, error) {
	pc.SetWriteTimeout(handshakeTimeout)
	pc.SetReadTimeout(handshakeTimeout)
	defer pc.SetWriteTimeout(0)
	defer pc.SetReadTimeout(0)

	var req Request
	req.Version = packet.VERSION
	req.Domain = domain
	req.Mac = mac
	req.Timestamp = time.Now().UnixNano()
	req.Sum = req.CalcSum(password)

	if err := req.WriteTo(pc); err != nil {
		return 0, fmt.Errorf("handshake: write request: %w", err)
	}

	var resp Response
	if err := resp.ReadFrom(pc); err != nil {
		return 0, fmt.Errorf("handshake: read response: %w", err)
	}

	if resp.ErrCode != 0 {
		return 0, fmt.Errorf("handshake: server rejected: %v", resp.ErrMsg)
	}

	if resp.Version != packet.VERSION {
		return 0, fmt.Errorf("handshake: %w: local=%v remote=%v", ErrVersionMismatch, packet.VERSION, resp.Version)
	}

	return resp.IP, nil
}

func Accept(pc packet.Conn, pswd string) (*Request, error) {
	pc.SetWriteTimeout(handshakeTimeout)
	pc.SetReadTimeout(handshakeTimeout)
	defer pc.SetWriteTimeout(0)
	defer pc.SetReadTimeout(0)

	req := &Request{}
	if err := req.ReadFrom(pc); err != nil {
		return nil, fmt.Errorf("handshake: read request: %w", err)
	}

	if req.Version != packet.VERSION {
		return nil, fmt.Errorf("handshake: %w: local=%v remote=%v", ErrVersionMismatch, packet.VERSION, req.Version)
	}

	drift := time.Since(time.Unix(0, req.Timestamp))
	if drift < 0 {
		drift = -drift
	}
	if drift > maxTimestampSkew {
		return nil, ErrTimestampExpired
	}

	if req.Sum != req.CalcSum(pswd) {
		return nil, ErrInvalidPassword
	}

	req.Domain = strings.ToLower(req.Domain)
	if IsInvalidDomain(req.Domain) {
		return nil, ErrInvalidDomain
	}

	return req, nil
}

// IsInvalidDomain 判断名称是否合法
func IsInvalidDomain(domain string) bool {
	return domain == "" || strings.HasPrefix(domain, "local")
}
