package admit

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/net-agent/flex/v3/packet"
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

	if err := req.WriteTo(pc, password); err != nil {
		return 0, fmt.Errorf("handshake: write request: %w", err)
	}

	var resp Response
	if err := resp.ReadFrom(pc, password); err != nil {
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
	if err := req.ReadFrom(pc, pswd); err != nil {
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

	normalized, err := NormalizeDomain(req.Domain)
	if err != nil {
		return nil, err
	}
	req.Domain = normalized

	return req, nil
}

// NormalizeDomain 归一化并校验 domain。
// 返回归一化后的 domain 或 ErrInvalidDomain。
func NormalizeDomain(domain string) (string, error) {
	domain = strings.TrimSpace(strings.ToLower(domain))

	if domain == "" || len(domain) > 63 {
		return "", ErrInvalidDomain
	}
	if domain == "local" || domain == "localhost" {
		return "", ErrInvalidDomain
	}
	if domain[0] == '-' || domain[0] == '_' || domain[len(domain)-1] == '-' || domain[len(domain)-1] == '_' {
		return "", ErrInvalidDomain
	}
	for _, c := range domain {
		if !(c >= 'a' && c <= 'z') && !(c >= '0' && c <= '9') && c != '-' && c != '_' {
			return "", ErrInvalidDomain
		}
	}
	return domain, nil
}

// IsInvalidDomain 判断名称是否合法（兼容旧调用方）。
func IsInvalidDomain(domain string) bool {
	_, err := NormalizeDomain(domain)
	return err != nil
}
