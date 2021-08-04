package switcher

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/net-agent/flex/v2/node"
	"github.com/net-agent/flex/v2/packet"
)

func ConnectServer(addr, domain, mac, password string) (retNode *node.Node, retErr error) {
	conn, err := net.Dial("tcp4", addr)
	if err != nil {
		return nil, err
	}
	pc := packet.NewWithConn(conn)

	node, err := UpgradeToNode(pc, domain, mac, password)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return node, nil
}

func UpgradeToNode(pc packet.Conn, domain, mac, password string) (*node.Node, error) {
	pbuf := packet.NewBuffer(nil)

	var req Request
	req.Domain = domain
	req.Mac = mac
	req.Timestamp = time.Now().UnixNano()
	req.Sum = req.CalcSum(password)
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
	if resp.ErrCode != 0 {
		return nil, fmt.Errorf("close by peer: %v", resp.ErrMsg)
	}

	node := node.New(pc)
	node.SetIP(resp.IP)

	return node, nil
}
