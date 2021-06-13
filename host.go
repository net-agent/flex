package flex

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

type Host struct {
	conn net.Conn

	chanRead     chan *Packet
	chanWrite    chan *Packet
	chanWriteACK chan *Packet

	// streams 用于路由对端发过来的数据
	// streamID -> stream
	streams sync.Map // map[uint32]*Stream

	// localBinds 用于路由对端发过来的指令？
	// localPort -> stream
	localBinds sync.Map // map[uint16]*Listener

	availablePorts chan uint16
}

func NewHost(conn net.Conn) *Host {
	h := &Host{
		conn:           conn,
		chanRead:       make(chan *Packet, 128),
		chanWrite:      make(chan *Packet, 128),
		chanWriteACK:   make(chan *Packet, 128),
		availablePorts: make(chan uint16, 65536),
	}

	for i := 1000; i < 65536; i++ {
		h.availablePorts <- uint16(i)
	}

	go h.readLoop()
	go h.writeLoop()

	return h
}

// Dial 请求对端创建连接
func (host *Host) Dial(remotePort uint16) (*Stream, error) {
	stream := NewStream(host, true)

	// select an available port
	for {
		select {
		case localPort := <-host.availablePorts:
			_, loaded := host.localBinds.LoadOrStore(localPort, stream)
			if !loaded {
				stream.localPort = localPort
				stream.remotePort = remotePort

				// data bind
				id := stream.dataID()
				_, loaded := host.streams.LoadOrStore(id, stream)
				if loaded {
					host.availablePorts <- localPort
					return nil, errors.New("stream bind exists")
				}

				log.Printf("new stream from dail: local=%v remote=%v\n", localPort, remotePort)

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

		if debug {
			log.Printf("[read loop]%v src=%v dist=%v size=%v",
				head.CmdStr(), head.SrcPort(), head.DistPort(), head.PayloadSize())
		}

		if head.IsACK() {
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

				id := head.StreamID()
				it, found := host.streams.Load(id)
				if found {
					it.(*Stream).increasePoolSize(head.ACKInfo())
				} else if debug {
					log.Printf("ignored open packet %v\n", head)
				}
			}

		} else {
			//
			// 接收到指令请求
			//
			switch head.Cmd() {

			case CmdOpenStream:

				it, found := host.localBinds.Load(head.DistPort())
				if found {
					stream := NewStream(host, false)
					_, found := host.streams.LoadOrStore(head.StreamID(), stream)
					if !found {
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
				it, found := host.streams.LoadAndDelete(head.StreamID())
				if found {
					it.(*Stream).Close()
				}

			case CmdPushStreamData:

				payload := make([]byte, head.PayloadSize())
				rn, err := io.ReadFull(host.conn, payload)
				if rn > 0 {
					it, found := host.streams.Load(head.StreamID())
					if found {
						it.(*Stream).readPipe.append(payload[:rn])
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
func (h *Host) writeLoop() {
	aliveBuf := make([]byte, packetHeaderSize)
	_, err := (&Packet{cmd: CmdAlive}).Read(aliveBuf)
	if err != nil {
		h.emitWriteErr(err)
		return
	}

	for {
		select {
		case packet, ok := <-h.chanWrite:
			if !ok {
				h.emitWriteErr(io.EOF)
				return
			}

			// todo: 优化内存池
			buf := make([]byte, packetHeaderSize+len(packet.payload))
			_, err := packet.Read(buf)
			if err != nil {
				h.emitReadErr(err)
				return
			}
			_, err = h.conn.Write(buf)
			// recycle buf here
			if err != nil {
				h.emitWriteErr(err)
				return
			}

			log.Printf("[writeloop]%v src=%v dist=%v size=%v\n",
				packet.CmdStr(), packet.srcPort, packet.distPort, len(packet.payload))

		case packet, ok := <-h.chanWriteACK:
			if ok {
				buf := make([]byte, packetHeaderSize)
				_, err := packet.Read(buf)
				if err != nil {
					h.emitWriteErr(err)
					return
				}
				_, err = h.conn.Write(buf)
				if err != nil {
					h.emitWriteErr(err)
					return
				}

				log.Printf("[writeloop]%v src=%v dist=%v size=%v ack=%v\n",
					packet.CmdStr(), packet.srcPort, packet.distPort, len(packet.payload), packet.ackInfo)
			}

		case <-time.After(time.Minute * 3):
			//
			// 如果三分钟都没有传输数据的请求，则会触发一次心跳包，确保通道不会被关闭
			//
			// 直接发送提前构造好的数据包
			//（此处对性能影响不大，需要发送心跳包的情况必然是数据传输压力小的时刻）
			_, err := h.conn.Write(aliveBuf)
			if err != nil {
				h.emitWriteErr(err)
			}
		}
	}
}

func (h *Host) emitReadErr(err error) {
	fmt.Println("[error]", err)
}
func (h *Host) emitWriteErr(err error) {
	fmt.Println("[error]", err)
}

func (h *Host) writePacket(cmd byte, srcPort, distPort uint16, payload []byte) error {
	p := &Packet{
		cmd:      cmd,
		srcPort:  srcPort,
		distPort: distPort,
		payload:  payload,
	}

	select {
	case h.chanWrite <- p:
		// todo: 此处过早返回（不可靠）
		return nil
	case <-time.After(time.Second * 15):
		return errors.New("timeout")
	}
}
func (h *Host) writePacketACK(cmd byte, srcPort, distPort uint16, ackInfo uint16) {
	p := &Packet{
		cmd:      CmdACKFlag | cmd,
		srcPort:  srcPort,
		distPort: distPort,
		payload:  nil,
		ackInfo:  ackInfo,
	}

	h.chanWriteACK <- p
}
