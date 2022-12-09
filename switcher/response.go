package switcher

import (
	"encoding/json"

	"github.com/net-agent/flex/v2/packet"
)

type Response struct {
	ErrCode int
	ErrMsg  string
	IP      uint16
	Version int
}

func (resp *Response) WriteToPacketConn(pc packet.Conn) error {
	payload, _ := json.Marshal(resp)
	pbuf := packet.NewBuffer(nil)
	pbuf.SetPayload(payload)
	return pc.WriteBuffer(pbuf)
}
