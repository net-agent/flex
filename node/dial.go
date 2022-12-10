package node

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/stream"
	"github.com/net-agent/flex/v2/vars"
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
	if err != nil || ip > int(vars.MaxIP) {
		return node.DialDomain(hostStr, uint16(port))
	}

	return node.DialIP(uint16(ip), uint16(port))
}

func (node *Node) DialDomain(domain string, port uint16) (*stream.Conn, error) {
	if domain == node.domain || domain == "local" || domain == "localhost" {
		return node.DialIP(node.GetIP(), port)
	}

	pbuf := packet.NewBuffer(nil)
	pbuf.SetCmd(packet.CmdOpenStream)
	pbuf.SetSrc(node.GetIP(), 0)
	pbuf.SetDist(vars.SwitcherIP, port)
	pbuf.SetPayload([]byte(domain))
	return node.dialPbuf(pbuf)
}

func (node *Node) DialIP(ip, port uint16) (*stream.Conn, error) {
	// return node.dial("", ip, port)
	pbuf := packet.NewBuffer(nil)
	pbuf.SetCmd(packet.CmdOpenStream)
	pbuf.SetSrc(node.GetIP(), 0)
	pbuf.SetDist(ip, port)
	pbuf.SetPayload(nil)
	return node.dialPbuf(pbuf)
}

func (node *Node) dialPbuf(pbuf *packet.Buffer) (*stream.Conn, error) {
	// 第一步：选择可用端口。
	// 如果此函数退出时srcPort非0，需要回收端口
	srcPort, err := node.portm.GetFreeNumberSrc()
	if err != nil {
		return nil, err
	}
	defer func() {
		if srcPort > 0 {
			node.portm.ReleaseNumberSrc(srcPort)
		}
	}()

	// 第二步：创建一个用于接收stream的chan
	// 这个stream会在ack到达时创建并绑定sid
	ch := make(chan *stream.Conn)
	defer close(ch)
	_, loaded := node.ackstreams.LoadOrStore(srcPort, ch)
	if loaded {
		return nil, errors.New("local port used")
	}
	defer node.ackstreams.Delete(srcPort)

	// 第三步：补齐pbuf的srcPort参数，向服务端发送open命令
	pbuf.SetSrcPort(srcPort)
	err = node.WriteBuffer(pbuf)
	if err != nil {
		return nil, err
	}

	// 第四步：等待ack返回（设置超时时间）
	select {
	case s := <-ch:
		if s == nil {
			return nil, errors.New("open stream failed")
		}
		srcPort = 0
		return s, nil
	case <-time.After(time.Second * 8):
		return nil, errors.New("dial timeout")
	}
}

// func (node *Node) dial(distDomain string, distIP, distPort uint16) (*stream.Conn, error) {
// 	localIP := node.GetIP()
// 	localPort, err := node.portm.GetFreeNumberSrc()
// 	if err != nil {
// 		return nil, err
// 	}
// 	s := stream.New(true)
// 	s.SetLocal(localIP, localPort)
// 	s.SetRemote(distIP, distPort) // 如果是dialDomain，则此处的端口是不正确的

// 	_, loaded := node.ackstreams.LoadOrStore(localPort, s)
// 	if loaded {
// 		return nil, errors.New("local port used")
// 	}
// 	defer node.ackstreams.Delete(localPort)

// 	buf := packet.NewBuffer(nil)
// 	buf.SetCmd(packet.CmdOpenStream)
// 	buf.SetSrc(localIP, localPort)
// 	buf.SetDist(distIP, distPort)
// 	buf.SetPayload([]byte(distDomain))
// 	err = node.WriteBuffer(buf)
// 	if err != nil {
// 		if errors.Is(err, os.ErrDeadlineExceeded) {
// 			log.Printf("dial failed, probably write data into an offline node: %v\n", err)
// 		}
// 		return nil, err
// 	}

// 	pbuf, err := s.WaitOpenResp()
// 	if err != nil {
// 		return nil, err
// 	}
// 	s.SetLocal(pbuf.DistIP(), pbuf.DistPort())
// 	s.SetRemote(pbuf.SrcIP(), pbuf.SrcPort())
// 	s.InitWriter(node)
// 	return s, nil
// }
