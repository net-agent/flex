package flex

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net"
	"regexp"
	"sync/atomic"
	"time"

	"github.com/net-agent/cipherconn"
)

func init() {
	rand.Seed(time.Now().UnixNano())
}

var randDomainIndex int32

func RandDomain() string {
	return fmt.Sprintf("rnd_%x_%v", rand.Int63(), atomic.AddInt32(&randDomainIndex, 1))
}

type HostRequest struct {
	Domain string
	Mac    string
}

type HostResponse struct {
	IP HostIP
}

func UpgradeConnToHost(conn net.Conn, password string, req *HostRequest) (*Host, error) {
	if password != "" {
		cc, err := cipherconn.New(conn, password)
		if err != nil {
			return nil, err
		}
		conn = cc
	}

	return UpgradeToHost(NewPacketConnFromConn(conn), req)
}

func UpgradeToHost(pc *PacketConn, req *HostRequest) (retHost *Host, retErr error) {
	var pb = NewPacketBufs()
	//
	// send request
	//
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	pb.SetPayload(data)
	err = pc.WritePacket(pb)
	if err != nil {
		return nil, err
	}

	err = pc.ReadPacket(pb)
	if err != nil {
		return nil, err
	}
	if len(pb.payload) != 2 {
		return nil, errors.New("unexpected resp size")
	}

	ip := binary.BigEndian.Uint16(pb.payload[0:2])
	return NewHost(pc, ip), nil
}

func (switcher *Switcher) UpgradeConnToHost(conn net.Conn) (*switchContext, error) {
	if switcher.password != "" {
		cc, err := cipherconn.New(conn, switcher.password)
		if err != nil {
			return nil, err
		}
		conn = cc
	}

	return switcher.UpgradeToContext(NewPacketConnFromConn(conn))
}

// UpgradeToContext 把连接升级为Host，并返回对端HostIP
func (switcher *Switcher) UpgradeToContext(pc *PacketConn) (retCtx *switchContext, retErr error) {
	pb := NewPacketBufs()
	err := pc.ReadPacket(pb)
	if err != nil {
		return nil, err
	}

	var req HostRequest
	err = json.Unmarshal(pb.payload, &req)
	if err != nil {
		return nil, err
	}

	if req.Domain == "" {
		req.Domain = RandDomain()
	} else if !isValidDomain(req.Domain) {
		return nil, errors.New("invalid domain")
	}

	ip, err := switcher.selectIP(req.Mac)
	if err != nil {
		return nil, err
	}

	//
	// send response
	//
	resp := make([]byte, 2)
	binary.BigEndian.PutUint16(resp[0:2], ip)
	pb.SetPayload(resp)
	err = pc.WritePacket(pb)
	if err != nil {
		return nil, err
	}

	return &switchContext{
		host:   NewSwitcherHost(switcher, pc),
		ip:     ip,
		domain: req.Domain,
		mac:    req.Mac,
	}, nil
}

func isValidDomain(domain string) bool {
	re := regexp.MustCompile(`^[a-zA-Z]([a-zA-Z0-9_.]){2,255}[a-zA-Z0-9_]$`)
	return re.MatchString(domain)
}
