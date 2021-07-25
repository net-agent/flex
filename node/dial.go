package node

import (
	"errors"

	"github.com/net-agent/flex/packet"
	"github.com/net-agent/flex/stream"
)

func (node *Node) Dial(addr string) (*stream.Conn, error) {
	return nil, errors.New("not implement")
}

func (node *Node) DialDomain(domain string, port uint16) (*stream.Conn, error) {
	return node.dial(domain, 0, port)
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
