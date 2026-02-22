package admit

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/net-agent/flex/v3/packet"
)

type Request struct {
	Version   int
	Domain    string
	Mac       string
	Timestamp int64
	Sum       string
}

func (req *Request) CalcSum(password string) string {
	h := hmac.New(sha256.New, []byte(password))
	fmt.Fprintf(h, "%v,%v,%v", req.Domain, req.Mac, req.Timestamp)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func (req *Request) Marshal() ([]byte, error) {
	return json.Marshal(req)
}

func (req *Request) WriteTo(pc packet.Conn) error {
	buf, err := req.Marshal()
	if err != nil {
		return err
	}
	pbuf := packet.NewBuffer()
	if err := pbuf.SetPayload(buf); err != nil {
		return err
	}
	return pc.WriteBuffer(pbuf)
}

func (req *Request) ReadFrom(pc packet.Conn) error {
	pbuf, err := pc.ReadBuffer()
	if err != nil {
		return err
	}
	return json.Unmarshal(pbuf.Payload, req)
}
