package switcher

import (
	"encoding/json"
	"net"

	"github.com/net-agent/flex/node"
	"github.com/net-agent/flex/packet"
)

func ConnectServer(addr, domain, password string) (retNode *node.Node, retErr error) {
	conn, err := net.Dial("tcp4", addr)
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil && conn != nil {
			conn.Close()
		}
	}()

	pc := packet.NewWithConn(conn)
	pbuf := packet.NewBuffer(nil)

	var req Request
	req.Domain = domain
	req.Mac = "test-mac-info"
	req.IV = packet.GetIV()
	reqBuf, err := json.Marshal(&req)
	if err != nil {
		return nil, err
	}
	pbuf.SetPayload(reqBuf)

	err = pc.WriteBuffer(pbuf)
	if err != nil {
		return nil, err
	}

	var resp Response
	pbuf, err = pc.ReadBuffer()
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal(pbuf.Payload, &resp)
	if err != nil {
		return nil, err
	}

	packet.Xor(&req.IV, &resp.IV)

	pc, err = packet.UpgradeCipher(pc, password, req.IV)
	if err != nil {
		return nil, err
	}

	node := node.New(pc)
	node.SetIP(resp.IP)

	return node, nil
}
