package node

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/stream"
)

const (
	MaxIP      = uint16(0xffff)
	DNSIP      = uint16(0xffff)
	SwitcherIP = uint16(0xffff)
	LocalIP    = uint16(0)
)

func (node *Node) Dial(addr string) (*stream.Conn, error) {
	hostStr, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("split address to host/port failed: %v", err)
	}
	// parse port
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return nil, fmt.Errorf("parse port string to number failed: %v", err)
	}
	// try to parse host as ip
	ip, err := strconv.Atoi(hostStr)
	if err != nil || ip > int(MaxIP) {
		return node.DialDomain(hostStr, uint16(port))
	}

	return node.DialIP(uint16(ip), uint16(port))
}

func (node *Node) DialDomain(domain string, port uint16) (*stream.Conn, error) {
	return node.dial(domain, DNSIP, port)
}

func (node *Node) DialIP(ip, port uint16) (*stream.Conn, error) {
	return node.dial("", ip, port)
}

func (node *Node) dial(distDomain string, distIP, distPort uint16) (*stream.Conn, error) {
	localIP := node.GetIP()
	localPort, err := node.GetFreePort()
	if err != nil {
		return nil, err
	}
	s := stream.New(true)
	s.SetLocal(localIP, localPort)
	s.SetRemote(distIP, distPort)

	_, loaded := node.usedPorts.LoadOrStore(localPort, s)
	if loaded {
		return nil, errors.New("local port used")
	}
	defer node.usedPorts.Delete(localPort)

	buf := packet.NewBuffer(nil)
	buf.SetCmd(packet.CmdOpenStream)
	buf.SetSrc(localIP, localPort)
	buf.SetDist(distIP, distPort)
	buf.SetPayload([]byte(distDomain))
	err = node.WriteBuffer(buf)
	if err != nil {
		if errors.Is(err, os.ErrDeadlineExceeded) {
			log.Printf("dial failed, probably write data into an offline node: %v\n", err)
		}
		return nil, err
	}

	pbuf, err := s.WaitOpenResp()
	if err != nil {
		return nil, err
	}
	s.SetLocal(pbuf.DistIP(), pbuf.DistPort())
	s.SetRemote(pbuf.SrcIP(), pbuf.SrcPort())
	s.InitWriter(node)
	return s, nil
}
