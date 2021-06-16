package flex

import "net"

const upgradeHeaderSize = int(12)

type upgradeHeader [upgradeHeaderSize]byte

type upgradeReq struct{}

type upgradeResp struct{}

func (switcher *Switcher) upgrade(conn net.Conn) (*Host, error) {
	return nil, nil
}
