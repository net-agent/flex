package flex

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"

	"github.com/net-agent/cipherconn"
)

type HostRequest struct {
	Domain string
	Mac    string
}

type HostResponse struct {
	IP HostIP
}

func UpgradeToHost(conn net.Conn, password string, req *HostRequest) (*Host, error) {
	if password != "" {
		cc, err := cipherconn.New(conn, password)
		if err != nil {
			conn.Close()
			return nil, err
		}
		conn = cc
	}

	//
	// send request
	//

	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	buf := make([]byte, len(data)+3)
	buf[0] = 0x01
	binary.BigEndian.PutUint16(buf[1:3], uint16(len(data)))
	copy(buf[3:], data)

	_, err = conn.Write(buf)
	if err != nil {
		return nil, err
	}

	//
	// recv response
	//

	buf = make([]byte, 3)
	_, err = io.ReadFull(conn, buf)
	if err != nil {
		return nil, err
	}

	ip := binary.BigEndian.Uint16(buf[1:3])

	return NewHost(NewPacketConn(conn), ip), nil
}

// UpgradeHost 把连接升级为Host，并返回对端HostIP
func (switcher *Switcher) UpgradeHost(conn net.Conn) (*switchContext, error) {
	//
	// recv request
	//
	head := make([]byte, 3)
	_, err := io.ReadFull(conn, head)
	if err != nil {
		return nil, err
	}
	payloadSize := binary.BigEndian.Uint16(head[1:3])
	payload := make([]byte, payloadSize)

	_, err = io.ReadFull(conn, payload)
	if err != nil {
		return nil, err
	}

	var req HostRequest
	err = json.Unmarshal(payload, &req)
	if err != nil {
		return nil, err
	}

	ip, err := switcher.selectIP(req.Mac)
	if err != nil {
		return nil, err
	}

	//
	// add dns record
	//

	//
	// send response
	//
	resp := make([]byte, 3)
	binary.BigEndian.PutUint16(resp[1:3], ip)
	wn, err := conn.Write(resp)
	if err != nil {
		return nil, err
	}
	if wn != len(resp) {
		return nil, errors.New("write failed")
	}

	return &switchContext{
		host:   NewSwitcherHost(switcher, NewPacketConn(conn)),
		ip:     ip,
		domain: req.Domain,
		mac:    req.Mac,
	}, nil
}