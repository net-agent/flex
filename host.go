package flex

import (
	"errors"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
)

type HostIP = uint16
type Host struct {
	pc *PacketConn

	// streams 用于路由对端发过来的数据
	// streamID -> stream
	streams    sync.Map // map[uint32]*Stream
	streamsLen int64

	// localBinds 用于路由对端发过来的指令？
	// localPort -> stream
	localBinds     sync.Map // map[uint16]*Listener
	availablePorts chan uint16
	ip             HostIP

	switcher *Switcher
}

// NewHost agent side host
func NewHost(pc *PacketConn, ip HostIP) *Host {
	host := &Host{
		pc: pc,
		ip: ip,
	}
	host.init()
	go host.Run()
	return host
}

// NewSwitcherHost server side host
func NewSwitcherHost(switcher *Switcher, pc *PacketConn) *Host {
	host := &Host{
		pc:       pc,
		ip:       0xffff,
		switcher: switcher,
	}
	host.init()
	return host
}

func (host *Host) init() {
	host.availablePorts = make(chan uint16, 65535)
	for i := 1000; i < 65536; i++ {
		host.availablePorts <- uint16(i)
	}
}

func (host *Host) Run() {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		host.readLoop()
		wg.Done()
	}()
	go func() {
		host.pc.WriteLoop()
		wg.Done()
	}()

	wg.Wait()
}

// Dial 请求对端创建连接
func (host *Host) Dial(addr string) (*Stream, error) {
	remoteHost, remotePort, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if remoteHost == "" {
		return nil, errors.New("empty remote host")
	}

	port, err := strconv.ParseInt(remotePort, 10, 16)
	if err != nil {
		return nil, err
	}

	ip, err := strconv.ParseInt(remoteHost, 10, 16)
	if err == nil {
		return host.dial("", HostIP(ip), uint16(port))
	}
	return host.dial(remoteHost, 0, uint16(port))
}

func (host *Host) dial(remoteDomain string, remoteIP HostIP, remotePort uint16) (*Stream, error) {
	localPort, err := host.selectLocalPort()
	if err != nil {
		return nil, err
	}

	stream := NewStream(host, true)
	stream.SetAddr(host.ip, localPort, remoteIP, remotePort)

	// local bind
	if _, loaded := host.localBinds.LoadOrStore(localPort, stream); loaded {
		// 也许是被Listen占用了
		return nil, errors.New("port not availble")
	}

	// if remoteDomain == "" {
	// 	log.Printf("dial stream: %v:%v -> %v:%v\n", host.ip, localPort, remoteIP, remotePort)
	// } else {
	// 	log.Printf("dial stream: %v:%v -> %v:%v\n", host.ip, localPort, remoteDomain, remotePort)
	// }

	err = stream.open(remoteDomain)
	if err != nil {
		host.localBinds.Delete(localPort)
		host.availablePorts <- localPort
		return nil, err
	}

	return stream, nil
}

func (host *Host) attach(stream *Stream) error {
	_, loaded := host.streams.LoadOrStore(stream.dataID, stream)
	if loaded {
		return errors.New("stream bind exists")
	}
	atomic.AddInt64(&host.streamsLen, 1)
	return nil
}

func (host *Host) detach(dataID uint64) (*Stream, error) {
	it, found := host.streams.LoadAndDelete(dataID)
	if !found {
		return nil, errors.New("dataID not found")
	}
	atomic.AddInt64(&host.streamsLen, -1)
	return it.(*Stream), nil
}

// Listen 监听对端创建连接的请求
func (host *Host) Listen(port uint16) (*Listener, error) {
	listener := NewListener()
	_, found := host.localBinds.LoadOrStore(port, listener)
	if found {
		return nil, errors.New("listen on used port")
	}
	return listener, nil
}

func (host *Host) selectLocalPort() (uint16, error) {
	for {
		select {
		case localPort := <-host.availablePorts:
			_, loaded := host.localBinds.Load(localPort)
			if !loaded {
				return localPort, nil
			}
		default:
			return 0, errors.New("local port resource depletion")
		}
	}
}

//
// readLoop
// 不断读取连接中的数据，并且根据解包的结果进行数据派发
//
func (host *Host) readLoop() error {
	defer host.pc.Close()

	var pb PacketBufs
	for {
		err := host.pc.ReadPacket(&pb)
		if err != nil {
			return err
		}

		if pb.head.Cmd() == CmdAlive {
			continue
		}

		if pb.head.IsACK() {
			switch pb.head.Cmd() {
			case CmdOpenStream:
				go host.onOpenStreamACK(
					pb.head.SrcIP(), pb.head.SrcPort(),
					pb.head.DistIP(), pb.head.DistPort())
			case CmdCloseStream:
				go host.onCloseStreamACK(pb.head.StreamDataID())
			case CmdPushStreamData:
				go host.onPushDataACK(pb.head.StreamDataID(), pb.head.ACKInfo())
			}
			continue
		}

		switch pb.head.Cmd() {
		case CmdOpenStream:
			go host.onOpenStream(
				pb.head.SrcIP(), pb.head.SrcPort(),
				pb.head.DistIP(), pb.head.DistPort(),
				string(pb.payload),
			)
		case CmdCloseStream:
			go host.onCloseStream(pb.head.StreamDataID())
		case CmdPushStreamData:
			// 为了保证数据顺序，此处不能使用goroutine
			host.onPushData(pb.head.StreamDataID(), pb.payload)
		}
	}
}

func (host *Host) writePacket(
	cmd byte,
	srcIP, distIP HostIP,
	srcPort, distPort uint16,
	ackInfo uint16, payload []byte,
) error {
	isCmd := (cmd&CmdACKFlag == 0)
	pb := &PacketBufs{}
	pb.SetBase(cmd, srcIP, srcPort, distIP, distPort)
	if isCmd {
		pb.SetPayload(payload)
	} else {
		pb.SetACKInfo(ackInfo)
	}

	return host.pc.NonblockWritePacket(pb, isCmd)
}

func (host *Host) LocalAddr() net.Addr {
	return host.pc.raw.LocalAddr()
}

func (host *Host) RemoteAddr() net.Addr {
	return host.pc.raw.RemoteAddr()
}

func (host *Host) IP() HostIP {
	return host.ip
}
