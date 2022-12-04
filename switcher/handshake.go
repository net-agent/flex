package switcher

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/net-agent/flex/v2/packet"
)

type Request struct {
	Version   int
	Domain    string
	Mac       string
	Timestamp int64
	Sum       string
}

// GenSum sha256(domain + mac + timestamp + password)
func (req *Request) CalcSum(password string) string {
	h := sha256.New()
	h.Write([]byte(fmt.Sprintf("CalcSumStart,%v,%v,%v,%v,CalcSumEnd", req.Domain, req.Mac, password, req.Timestamp)))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

type Response struct {
	ErrCode int
	ErrMsg  string
	IP      uint16
}

// ServeConn
func (s *Server) ServeConn(pc packet.Conn) (retErr error) {
	defer func() {
		if retErr != nil {
			var resp Response
			resp.ErrCode = -1
			resp.ErrMsg = retErr.Error()
			resp.IP = 0
			data, err := json.Marshal(&resp)
			if err == nil {
				pbuf := packet.NewBuffer(nil)
				pbuf.SetPayload(data)
				pc.WriteBuffer(pbuf)
			}
		}
		pc.Close()
	}()

	pbuf, err := pc.ReadBuffer()
	if err != nil {
		return err
	}

	var req Request
	err = json.Unmarshal(pbuf.Payload, &req)
	if err != nil {
		return err
	}
	if req.Version != packet.VERSION {
		return fmt.Errorf("invalid client version='%v', expected='%v'", req.Version, packet.VERSION)
	}
	if req.Sum != req.CalcSum(s.password) {
		return errors.New("invalid checksum detected")
	}

	req.Domain = strings.ToLower(req.Domain)
	if req.Domain == "" || strings.HasPrefix(req.Domain, "local") {
		return errors.New("invalid domain")
	}

	ip, err := s.GetIP(req.Domain)
	if err != nil {
		return err
	}
	defer s.FreeIP(ip)

	ctx := NewContext(req.Domain, req.Mac, ip, pc)

	err = s.AttachCtx(ctx)
	if err != nil {
		return err
	}
	defer s.DetachCtx(ctx)

	// response
	var resp Response
	resp.IP = ip
	respBuf, err := json.Marshal(&resp)
	if err != nil {
		return err
	}

	pbuf.SetPayload(respBuf)
	err = pc.WriteBuffer(pbuf)
	if err != nil {
		log.Printf("node handshake failed, WriteBuffer err: %v\n", err)
		return nil
	}

	return RunPbufLoopService(s, ctx)
}
