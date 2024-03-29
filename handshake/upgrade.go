package handshake

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/net-agent/flex/v2/packet"
)

var (
	errUpgradeWriteFailed    = errors.New("upgrade write buffer failed")
	errUpgradeReadFailed     = errors.New("upgrade read buffer failed")
	errUnmarshalFailed       = errors.New("unmarshal payload failed")
	errPacketVersionNotMatch = errors.New("packet version not match")
	ErrInvalidPassword       = errors.New("invalid password")
	ErrInvalidDomain         = errors.New("invalid domain")
)

func UpgradeRequest(pc packet.Conn, domain, mac, password string) (uint16, error) {
	var req Request
	req.Version = packet.VERSION
	req.Domain = domain
	req.Mac = mac
	req.Timestamp = time.Now().UnixNano()
	req.Sum = req.CalcSum(password)

	pbuf := packet.NewBuffer(nil)
	pbuf.SetPayload(req.Marshal())
	err := pc.WriteBuffer(pbuf)
	if err != nil {
		return 0, errUpgradeWriteFailed
	}

	pbuf, err = pc.ReadBuffer()
	if err != nil {
		return 0, errUpgradeReadFailed
	}

	var resp Response
	err = json.Unmarshal(pbuf.Payload, &resp)
	if err != nil {
		return 0, errUnmarshalFailed
	}
	if resp.ErrCode != 0 {
		return 0, fmt.Errorf("server side response: %v", resp.ErrMsg)
	}

	// 检查版本一致性
	if resp.Version != packet.VERSION {
		return 0, errPacketVersionNotMatch
	}

	// node := node.New(pc)
	// node.SetIP(resp.IP)
	// node.SetDomain(domain)

	return resp.IP, nil
}

// HandleUpgradeRequest 在服务端处理Upgrade请求
func HandleUpgradeRequest(pc packet.Conn, pswd string) (*Request, error) {
	pbuf, err := pc.ReadBuffer()
	if err != nil {
		return nil, errUpgradeReadFailed
	}

	req := &Request{}
	err = json.Unmarshal(pbuf.Payload, req)
	if err != nil {
		return nil, errUnmarshalFailed
	}

	// 检查请求的合法性
	if req.Version != packet.VERSION {
		return nil, errPacketVersionNotMatch
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
