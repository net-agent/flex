package node

import (
	"errors"
	"log"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/net-agent/flex/v2/numsrc"
	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/stream"
	"github.com/net-agent/flex/v2/vars"
)

var (
	errInvalidIPNumber   = errors.New("invalid ip number")
	errInvalidPortNumber = errors.New("invalid port number")
)

type dialresp struct {
	err    error
	stream *stream.Conn
}

type Dialer struct {
	host      *Node
	portm     *numsrc.Manager
	responses sync.Map // map[uint16]chan *dialresp
}

func (d *Dialer) Init(host *Node, portm *numsrc.Manager) {
	d.host = host
	d.portm = portm
}

// Dial 通过address信息创建新的连接
func (d *Dialer) Dial(addr string) (*stream.Conn, error) {
	isDomain, domain, ip, port, err := parseAddress(addr)
	if err != nil {
		return nil, err
	}

	if isDomain {
		return d.DialDomain(domain, port)
	}

	return d.DialIP(ip, port)
}

// DialDomain 通过domain信息进行dial
func (d *Dialer) DialDomain(domain string, port uint16) (*stream.Conn, error) {
	if domain == d.host.domain || domain == "local" || domain == "localhost" {
		return d.DialIP(d.host.GetIP(), port)
	}

	pbuf := packet.NewBuffer(nil)
	pbuf.SetCmd(packet.CmdOpenStream)
	pbuf.SetSrc(d.host.GetIP(), 0)
	pbuf.SetDist(vars.SwitcherIP, port)
	pbuf.SetPayload([]byte(domain))
	return d.dialPbuf(pbuf)
}

// DialIP 通过IP信息进行dial
func (d *Dialer) DialIP(ip, port uint16) (*stream.Conn, error) {
	// return node.dial("", ip, port)
	pbuf := packet.NewBuffer(nil)
	pbuf.SetCmd(packet.CmdOpenStream)
	pbuf.SetSrc(d.host.GetIP(), 0)
	pbuf.SetDist(ip, port)
	pbuf.SetPayload(nil)
	return d.dialPbuf(pbuf)
}

// DialPbuf dial的底层实现
// 注意：pbuf里的srcPort还需要在writeBuffer前进行确认
func (d *Dialer) dialPbuf(pbuf *packet.Buffer) (*stream.Conn, error) {
	// 第一步：选择可用端口。
	// 如果此函数退出时srcPort非0，需要回收端口
	srcPort, err := d.portm.GetFreeNumberSrc()
	if err != nil {
		return nil, err
	}
	defer func() {
		if srcPort > 0 {
			d.portm.ReleaseNumberSrc(srcPort)
		}
	}()

	// 第二步：创建一个用于接收stream的chan
	// 这个stream会在ack到达时创建并绑定sid
	ch := make(chan *dialresp)
	defer close(ch)
	_, loaded := d.responses.LoadOrStore(srcPort, ch)
	if loaded {
		return nil, errors.New("local port used")
	}
	defer d.responses.Delete(srcPort)

	// 第三步：补齐pbuf的srcPort参数，向服务端发送open命令
	pbuf.SetSrcPort(srcPort)
	err = d.host.WriteBuffer(pbuf)
	if err != nil {
		return nil, err
	}

	// 第四步：等待ack返回（设置超时时间）
	select {
	case resp := <-ch:
		if resp == nil {
			return nil, errors.New("open stream failed")
		}
		if resp.err != nil {
			return nil, resp.err
		}
		srcPort = 0
		return resp.stream, nil
	case <-time.After(time.Second * 8):
		return nil, errors.New("dial timeout")
	}
}

func (d *Dialer) HandleCmdOpenStreamAck(pbuf *packet.Buffer) {
	it, found := d.responses.LoadAndDelete(pbuf.DistPort())
	if !found {
		log.Printf("open ack response chan not found, port=%v\n", pbuf.DistPort())
		return
	}
	ch, ok := it.(chan *dialresp)
	if !ok {
		log.Printf("convert response chan failed")
		return
	}

	ackMessage := string(pbuf.Payload)
	if ackMessage != "" {
		ch <- &dialresp{errors.New(ackMessage), nil}
		return
	}

	//
	// create and bind stream
	//
	s := stream.New(d.host, true)
	s.SetLocal(pbuf.DistIP(), pbuf.DistPort())
	s.SetRemote(pbuf.SrcIP(), pbuf.SrcPort())
	defer func() {
		if s != nil {
			s.Close()
		}
	}()

	err := d.host.AttachStream(s, pbuf.SID())
	if err != nil {
		log.Printf("attach stream to node failed, err=%v\n", err)
		ch <- &dialresp{err, nil}
		return
	}

	ch <- &dialresp{nil, s}
	s = nil
}

// parseAddress 对字符串地址进行解析
func parseAddress(addr string) (isDomain bool, domain string, ip uint16, port uint16, err error) {
	h, p, err := net.SplitHostPort(addr)
	if err != nil {
		return
	}

	intPort, err := strconv.Atoi(p)
	if err != nil {
		return
	}
	if intPort < 0 || intPort > 0xffff {
		err = errInvalidPortNumber
		return
	}
	port = uint16(intPort)

	intIp, err := strconv.Atoi(h)
	if err != nil {
		// treat as domain
		return true, h, 0, port, nil
	}
	if intIp < 0 || intIp > int(vars.MaxIP) {
		err = errInvalidIPNumber
		return
	}
	return false, "", uint16(intIp), port, nil
}
