package node

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/net-agent/flex/v2/event"
	"github.com/net-agent/flex/v2/idpool"
	"github.com/net-agent/flex/v2/packet"
	"github.com/net-agent/flex/v2/stream"
	"github.com/net-agent/flex/v2/vars"
)

var (
	errInvalidIPNumber       = errors.New("invalid ip number")
	errInvalidPortNumber     = errors.New("invalid port number")
	ErrLocalPortUsed         = errors.New("local port used")
	ErrWaitResponseTimeout   = errors.New("wait dial response timeout")
	ErrWriteDialPbufFailed   = errors.New("write dial buffer failed")
	ErrUnexpectedNilResponse = errors.New("unexpected nil response")
)

type Dialer struct {
	host    *Node
	portm   *idpool.Pool
	timeout time.Duration
	// responses sync.Map // map[uint16]chan *dialresp
	evbus event.Bus
}

func (d *Dialer) Init(host *Node, portm *idpool.Pool) {
	d.host = host
	d.portm = portm
	d.SetDialTimeout(time.Second * 15)
}

func (d *Dialer) SetDialTimeout(timeout time.Duration) { d.timeout = timeout }

// Dial 通过address信息创建新的连接
func (d *Dialer) Dial(addr string) (*stream.Stream, error) {
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
func (d *Dialer) DialDomain(domain string, port uint16) (*stream.Stream, error) {
	if domain == d.host.domain || domain == "local" || domain == "localhost" {
		return d.DialIP(d.host.GetIP(), port)
	}

	pbuf := packet.NewBuffer(nil)
	pbuf.SetCmd(packet.CmdOpenStream)
	pbuf.SetSrc(d.host.GetIP(), 0)
	pbuf.SetDist(vars.SwitcherIP, port)
	// Payload: [domain] + [0x00] + [windowSize(4 bytes)]
	payload := make([]byte, 0, len(domain)+5)
	payload = append(payload, []byte(domain)...)
	payload = append(payload, 0)
	payload = appendWindowSize(payload, d.host.GetWindowSize())
	pbuf.SetPayload(payload)
	return d.dialPbuf(pbuf)
}

// DialIP 通过IP信息进行dial
func (d *Dialer) DialIP(ip, port uint16) (*stream.Stream, error) {
	pbuf := packet.NewBuffer(nil)
	pbuf.SetCmd(packet.CmdOpenStream)
	pbuf.SetSrc(d.host.GetIP(), 0)
	pbuf.SetDist(ip, port)
	// Payload: [0x00] + [windowSize(4 bytes)]
	payload := make([]byte, 0, 5)
	payload = append(payload, 0)
	payload = appendWindowSize(payload, d.host.GetWindowSize())
	pbuf.SetPayload(payload)
	return d.dialPbuf(pbuf)
}

// DialPbuf dial的底层实现
// 注意：pbuf里的srcPort还需要在writeBuffer前进行确认
func (d *Dialer) dialPbuf(pbuf *packet.Buffer) (*stream.Stream, error) {
	remoteDomain := string(pbuf.Payload)
	if remoteDomain == "" {
		distIP := pbuf.DistIP()
		if distIP == d.host.ip {
			remoteDomain = d.host.domain
		} else {
			remoteDomain = fmt.Sprintf("%v", distIP)
		}
	}

	// 第一步：选择可用端口。
	// 如果此函数退出时srcPort非0，需要回收端口
	srcPort, err := d.portm.Allocate()
	if err != nil {
		return nil, err
	}
	defer func() {
		if srcPort > 0 {
			d.portm.Release(srcPort)
		}
	}()

	// 第二步：创建一个用于接收stream的chan
	// 这个stream会在ack到达时创建并绑定sid
	ev, err := d.evbus.ListenOnce(srcPort)
	if err != nil {
		return nil, err
	}

	// 第三步：补齐pbuf的srcPort参数，向服务端发送open命令
	pbuf.SetSrcPort(srcPort)
	err = d.host.WriteBuffer(pbuf)
	if err != nil {
		return nil, ErrWriteDialPbufFailed
	}

	// 第四步：等待ack返回（设置超时时间）
	resp, err := ev.Wait(d.timeout)
	if err != nil {
		return nil, err
	}

	// HandleAckOpenStream 里面的处理逻辑，确保不会出现nil resp
	s := resp.(*stream.Stream)
	s.SetRemoteDomain(remoteDomain)

	return s, nil
}

func (d *Dialer) HandleAckOpenStream(pbuf *packet.Buffer) {
	evKey := pbuf.DistPort()
	var negotiatedWindowSize int32
	isSuccess := false

	if len(pbuf.Payload) == 0 {
		isSuccess = true
		negotiatedWindowSize = 0 // use default
	} else if pbuf.Payload[0] == 0 {
		isSuccess = true
		if len(pbuf.Payload) >= 5 {
			negotiatedWindowSize = int32(readUint32(pbuf.Payload[1:]))
		}
	}

	if !isSuccess {
		err := d.evbus.Dispatch(evKey, errors.New(string(pbuf.Payload)), nil)
		if err != nil {
			d.host.logger.Warn("dispatch ack-msg failed", "error", err)
		}
		return
	}

	//
	// create and bind stream
	//
	s := stream.NewDialStream(
		d.host,
		d.host.domain, pbuf.DistIP(), pbuf.DistPort(),
		"", pbuf.SrcIP(), pbuf.SrcPort(),
		negotiatedWindowSize,
	)

	err := d.host.AttachStream(s, pbuf.SID())
	if err != nil {
		d.host.logger.Warn("attach stream to node failed", "error", err)
		err = d.evbus.Dispatch(evKey, err, nil)
		if err != nil {
			d.host.logger.Warn("dispatch ack-err failed", "error", err)
		}
		return
	}

	err = d.evbus.Dispatch(evKey, nil, s)
	if err != nil {
		d.host.logger.Warn("dispatch ack failed", "error", err)
	}
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
