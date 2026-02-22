package admit

import (
	"encoding/json"

	"github.com/net-agent/flex/v3/packet"
)

type Response struct {
	ErrCode int
	ErrMsg  string
	IP      uint16
	Version int
}

func NewOKResponse(ip uint16) *Response {
	return &Response{Version: packet.VERSION, IP: ip}
}

func NewErrResponse(code int, msg string) *Response {
	return &Response{Version: packet.VERSION, ErrCode: code, ErrMsg: msg}
}

func (resp *Response) WriteTo(pc packet.Conn) error {
	payload, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	pbuf := packet.NewBuffer()
	if err := pbuf.SetPayload(payload); err != nil {
		return err
	}
	return pc.WriteBuffer(pbuf)
}

func (resp *Response) ReadFrom(pc packet.Conn) error {
	pbuf, err := pc.ReadBuffer()
	if err != nil {
		return err
	}
	return json.Unmarshal(pbuf.Payload, resp)
}
