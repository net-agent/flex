package flex

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type HostIP = uint16
type Host struct {
	conn net.Conn
	ip   HostIP

	chanRead  chan *Packet
	chanWrite chan *Packet

	writeLocker           sync.Mutex
	writtenCmdPackCount   uint32
	writtenACKPackCount   uint32
	writtenAlivePackCount uint32
	readedCmdPackCount    uint32
	readedACKPackCount    uint32
	readedAlivePackCount  uint32

	// streams 用于路由对端发过来的数据
	// streamID -> stream
	streams    sync.Map // map[uint32]*Stream
	streamsLen int64

	// localBinds 用于路由对端发过来的指令？
	// localPort -> stream
	localBinds sync.Map // map[uint16]*Listener

	availablePorts chan uint16

	switcher *Switcher
}

func NewHost(switcher *Switcher, conn net.Conn, ip HostIP) *Host {
	h := &Host{
		conn:           conn,
		ip:             ip,
		chanRead:       make(chan *Packet, 128),
		chanWrite:      make(chan *Packet, 128),
		availablePorts: make(chan uint16, 65536),
		switcher:       switcher,
	}

	for i := 1000; i < 65536; i++ {
		h.availablePorts <- uint16(i)
	}

	if switcher != nil {
		go switcher.hostReadLoop(h)
	} else {
		go h.readLoop()
	}

	go h.writeLoop()

	return h
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

	if remoteDomain == "" {
		log.Printf("dial stream: %v:%v -> %v:%v\n", host.ip, localPort, remoteIP, remotePort)
	} else {
		log.Printf("dial stream: %v:%v -> %v:%v\n", host.ip, localPort, remoteDomain, remotePort)
	}

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
func (host *Host) readLoop() {
	var head packetHeader

	for {
		_, err := io.ReadFull(host.conn, head[:])
		if err != nil {
			host.emitReadErr(err)
			return
		}

		if head[0] == CmdAlive {
			host.readedAlivePackCount++
			continue
		}

		if head.IsACK() {
			host.readedACKPackCount++
			//
			// 接收到对端应答
			//
			switch head.Cmd() {

			case CmdOpenStream:
				log.Printf("open ack %v", head)
				it, found := host.localBinds.Load(head.DistPort())
				if found {
					stream := it.(*Stream)
					stream.SetAddr(
						stream.localIP,
						stream.localPort,
						head.SrcIP(), // 如果是通过domain进行创建的，这个字段需要更新
						stream.remotePort,
					)
					host.attach(stream)
					stream.chanOpenACK <- struct{}{}
				} else {
					log.Printf("ignored open packet %v\n", head)
				}

			case CmdCloseStream:

			case CmdPushStreamData:

				id := head.StreamDataID()
				it, found := host.streams.Load(id)
				if found {
					it.(*Stream).increasePoolSize(head.ACKInfo())
				} else if debug {
					log.Printf("ignored open packet %v\n", head)
				}
			}

		} else {
			host.readedCmdPackCount++
			//
			// 接收到指令请求
			//
			switch head.Cmd() {

			case CmdOpenStream:
				log.Printf("open %v", head)
				it, found := host.localBinds.Load(head.DistPort())
				if !found {
					log.Printf("%v port=%v not available. %v\n", head.CmdStr(), head.DistPort(), head)
					continue
				}
				stream := NewStream(host, false)
				stream.SetAddr(head.DistIP(), head.DistPort(), head.SrcIP(), head.SrcPort())

				_, found = host.streams.LoadOrStore(stream.dataID, stream)
				if found {
					log.Printf("%v stream exists. %v\n", head.CmdStr(), head)
					continue
				}

				atomic.AddInt64(&host.streamsLen, 1)
				if debug {
					log.Printf("stream opened %v dataID=%x alive=%v\n", head, head.StreamDataID(), host.streamsLen)
				}

				it.(*Listener).pushStream(stream)
				stream.opened()

			case CmdCloseStream:
				//
				// 收到Close消息，可以确定对端不会再有数据包通过这个stream发过来
				// 所以可以对StreamID进行清理操作
				//
				it, found := host.streams.LoadAndDelete(head.StreamDataID())
				if found {
					atomic.AddInt64(&host.streamsLen, -1)

					// 关闭读取管道，收到对端的close指令后，不会再有新数据过来
					stream := it.(*Stream)
					stream.readPipe.Close()

					stream.Close()
					if debug {
						log.Printf("stream closed dataID=%v alive=%v\n",
							head.StreamDataID(), host.streamsLen)
					}
				}

			case CmdPushStreamData:

				payload := make([]byte, head.PayloadSize())
				rn, err := io.ReadFull(host.conn, payload)
				if rn > 0 {
					it, found := host.streams.Load(head.StreamDataID())
					if found {
						it.(*Stream).readPipe.append(payload[:rn])
					} else {
						log.Printf("[src=%v][dist=%v] ignored cmd %v dataID=%x\n",
							head.SrcPort(), head.DistPort(), head, head.StreamDataID())
					}
				}
				if err != nil {
					if err != io.EOF {
						host.emitReadErr(err)
					}
					return
				}
			}
		}
	}
}

//
// writeLoop
// 不断向连接中灌入数据
//
func (host *Host) writeLoop() {
	aliveBuf := make([]byte, packetHeaderSize)
	_, err := (&Packet{cmd: CmdAlive}).Read(aliveBuf)
	if err != nil {
		host.emitWriteErr(err)
		return
	}

	for {
		select {
		case packet, ok := <-host.chanWrite:
			if !ok {
				host.emitWriteErr(io.EOF)
				return
			}

			// todo: 优化内存池
			payloadSize := len(packet.payload)
			buf := make([]byte, packetHeaderSize+payloadSize)
			_, err := packet.Read(buf)
			if err != nil {
				host.emitReadErr(err)
				return
			}
			err = host.writeBuffer(buf)
			// recycle buf here
			if err != nil {
				host.emitWriteErr(err)
				return
			}

			if packet.done != nil {
				packet.done <- struct{}{}
				close(packet.done)
			}
			if packet.cmd&0x01 > 0 {
				host.writtenACKPackCount++
			} else {
				host.writtenCmdPackCount++
			}

			// if debug {
			// 	log.Printf("[writeloop]%v src=%v:%v dist=%v:%v size=%v\n",
			// 		packet.CmdStr(), packet.srcHost, packet.srcPort, packet.distHost, packet.distPort, len(packet.payload))
			// }

		case <-time.After(time.Minute * 3):
			//
			// 如果三分钟都没有传输数据的请求，则会触发一次心跳包，确保通道不会被关闭
			//
			// 直接发送提前构造好的数据包
			//（此处对性能影响不大，需要发送心跳包的情况必然是数据传输压力小的时刻）
			err := host.writeBuffer(aliveBuf)
			if err != nil {
				host.emitWriteErr(err)
				return
			}
			host.writtenAlivePackCount++
		}
	}
}

func (host *Host) emitReadErr(err error) {
	fmt.Println("[error]", err)
}
func (host *Host) emitWriteErr(err error) {
	fmt.Println("[error]", err)
}

func (host *Host) writePacket(
	cmd byte,
	srcIP, distIP HostIP,
	srcPort, distPort uint16,
	payload []byte,
) error {
	p := &Packet{
		cmd:      cmd,
		srcHost:  srcIP,
		distHost: distIP,
		srcPort:  srcPort,
		distPort: distPort,
		payload:  payload,
		done:     make(chan struct{}),
	}

	host.chanWrite <- p

	select {
	case <-p.done:
		return nil
	case <-time.After(time.Second * 15):
		return errors.New("timeout")
	}
}
func (host *Host) writePacketACK(cmd byte, srcHost, distHost HostIP, srcPort, distPort uint16, ackInfo uint16) {
	p := &Packet{
		cmd:      CmdACKFlag | cmd,
		srcHost:  srcHost,
		distHost: distHost,
		srcPort:  srcPort,
		distPort: distPort,
		payload:  nil,
		ackInfo:  ackInfo,
	}

	host.chanWrite <- p
}

func (host *Host) writeBuffer(bufs ...[]byte) error {
	host.writeLocker.Lock()
	defer host.writeLocker.Unlock()

	for _, buf := range bufs {
		wn := int(0)
		for {
			n, err := host.conn.Write(buf)
			wn += n
			if err != nil {
				return err
			}
			if wn < len(buf) {
				buf = buf[wn:]
			} else {
				break
			}
		}
	}
	return nil
}
