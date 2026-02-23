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

func (resp *Response) WriteTo(pc packet.Conn, password string) error {
	plaintext, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	ciphertext, err := encrypt(plaintext, password)
	if err != nil {
		return err
	}
	pbuf := packet.NewBufferWithCmd(packet.CmdAdmit)
	randomFillHeader(pbuf)
	if err := pbuf.SetPayload(ciphertext); err != nil {
		return err
	}
	return pc.WriteBuffer(pbuf)
}

func (resp *Response) ReadFrom(pc packet.Conn, password string) error {
	pbuf, err := pc.ReadBuffer()
	if err != nil {
		return err
	}
	plaintext, err := decrypt(pbuf.Payload, password)
	if err != nil {
		return err
	}
	return json.Unmarshal(plaintext, resp)
}
