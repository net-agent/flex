package flex

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
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

	go h.readLoop()
	go h.writeLoop()

	return h
}

// Dial 请求对端创建连接
func (host *Host) Dial(remoteIP HostIP, remotePort uint16) (*Stream, error) {
	stream := NewStream(host, true)

	// select an available port
	for {
		select {
		case localPort := <-host.availablePorts:
			_, loaded := host.localBinds.LoadOrStore(localPort, stream)
			if !loaded {
				stream.localIP = host.ip
				stream.remoteIP = remoteIP
				stream.localPort = localPort
				stream.remotePort = remotePort

				// data bind
				id := stream.dataID()
				_, loaded := host.streams.LoadOrStore(id, stream)
				if loaded {
					host.availablePorts <- localPort
					return nil, errors.New("stream bind exists")
				}
				atomic.AddInt64(&host.streamsLen, 1)

				log.Printf("try to dial stream: %v:%v -> %v:%v\n", host.ip, localPort, remoteIP, remotePort)

				err := stream.open()
				if err != nil {
					host.localBinds.Delete(localPort)
					host.availablePorts <- localPort
					return nil, err
				}

				return stream, nil
			}
		default:
			return nil, errors.New("local port resource depletion")
		}
	}
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

		// 判断是否需要通过switcher分发packet
		if head.DistIP() != host.ip {
			log.Printf("switch packet: dist=%v:%v\n", head.DistIP(), head.DistPort())
			if host.switcher != nil {
				packetBuf := make([]byte, packetHeaderSize+head.PayloadSize())
				copy(packetBuf[:packetHeaderSize], head[:])
				if len(packetBuf) > packetHeaderSize {
					_, err := io.ReadFull(host.conn, packetBuf[packetHeaderSize:])
					if err != nil {
						host.emitReadErr(err)
					}
				}
				host.switcher.chanPacketBufferRoute <- packetBuf
			}
			continue
		}

		if head[0] == CmdAlive {
			host.readedAlivePackCount++
			continue
		}

		// if debug {
		// 	log.Printf("[read loop]%v src=%v:%v dist=%v:%v size=%v",
		// 		head.CmdStr(), head.SrcIP(), head.SrcPort(), head.DistIP(), head.DistPort(), head.PayloadSize())
		// }

		if head.IsACK() {
			host.readedACKPackCount++
			//
			// 接收到对端应答
			//
			switch head.Cmd() {

			case CmdOpenStream:
				it, found := host.localBinds.Load(head.DistPort())
				if found {
					it.(*Stream).chanOpenACK <- struct{}{}
				} else if debug {
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

				it, found := host.localBinds.Load(head.DistPort())
				if found {
					stream := NewStream(host, false)
					_, found := host.streams.LoadOrStore(head.StreamDataID(), stream)
					if !found {

						atomic.AddInt64(&host.streamsLen, 1)
						if debug {
							log.Printf("stream opened %v dataID=%x alive=%v\n",
								head, head.StreamDataID(), host.streamsLen)
						}

						stream.localIP = head.DistIP()
						stream.remoteIP = head.SrcIP()
						stream.localPort = head.DistPort()
						stream.remotePort = head.SrcPort()
						it.(*Listener).pushStream(stream)
						stream.opened()
					}
				}

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

func (host *Host) writeBuffer(buf []byte) error {
	host.writeLocker.Lock()
	defer host.writeLocker.Unlock()

	wn := int(0)
	for wn < len(buf) {
		n, err := host.conn.Write(buf)
		wn += n
		if err != nil {
			return err
		}
		buf = buf[wn:]
	}
	return nil
}
