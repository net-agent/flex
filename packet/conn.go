package packet

import (
	"errors"
	"log"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const logRawPacket = false

type PacketIO interface {
	Origin() interface{}
	ReadPacket(pb *Buffer) error
	WritePacket(pb *Buffer) error
	Close() error
}

//
// tcp conn implement
//
type connPacketIO struct {
	conn  net.Conn
	wlock sync.Mutex
}

func NewTcpPacketIO(conn net.Conn) PacketIO {
	return &connPacketIO{
		conn: conn,
	}
}

func (c *connPacketIO) Origin() interface{} {
	return c.conn
}

func (c *connPacketIO) ReadPacket(pb *Buffer) error {
	err := ReadConn(c.conn, pb)
	if err != nil {
		c.conn.Close()
	}

	if logRawPacket {
		payload := ""
		if pb.head[0] == CmdOpenStream {
			payload = string(pb.payload)
		}
		log.Printf("%v %v->%v %v %v\n", pb.head.CmdStr(), pb.head.Src(), pb.head.Dist(), pb.head, payload)
	}

	return err
}

func (c *connPacketIO) WritePacket(pb *Buffer) (ret error) {
	c.wlock.Lock()
	defer func() {
		if ret != nil {
			c.conn.Close()
		}
		c.wlock.Unlock()
	}()

	n, err := c.conn.Write(pb.head[:])
	if err != nil {
		return err
	}
	if n != len(pb.head) {
		return errors.New("imcomplete packet head wrote")
	}

	if len(pb.payload) > 0 {
		n, err = c.conn.Write(pb.payload)
		if err != nil {
			return err
		}
		if n != len(pb.payload) {
			return errors.New("imcomplete packet payload wrote")
		}
	}

	if pb.writeDone != nil {
		pb.writeDone <- struct{}{}
	}
	return nil
}

func (c *connPacketIO) Close() error {
	return c.conn.Close()
}

//
// websocket conn implement
//
type wsPacketIO struct {
	conn  *websocket.Conn
	wlock sync.Mutex
}

func NewWsPacketIO(wsconn *websocket.Conn) PacketIO {
	return &wsPacketIO{
		conn: wsconn,
	}
}

func (ws *wsPacketIO) Origin() interface{} {
	return ws.conn
}

func (ws *wsPacketIO) WritePacket(pb *Buffer) (ret error) {
	ws.wlock.Lock()
	defer func() {
		if ret != nil {
			ws.conn.Close()
		}
		ws.wlock.Unlock()
	}()
	if len(pb.payload) == 0 {
		return ws.conn.WriteMessage(websocket.BinaryMessage, pb.head[:])
	}
	buf := make([]byte, HeaderSz+len(pb.payload))
	copy(buf[:HeaderSz], pb.head[:])
	copy(buf[HeaderSz:], pb.payload)
	err := ws.conn.WriteMessage(websocket.BinaryMessage, buf)
	if err != nil {
		return err
	}
	if pb.writeDone != nil {
		pb.writeDone <- struct{}{}
	}
	return nil
}

func (ws *wsPacketIO) ReadPacket(pb *Buffer) error {
	for {
		msgType, buf, err := ws.conn.ReadMessage()
		if err != nil {
			return err
		}
		if msgType == websocket.BinaryMessage {
			if len(buf) < HeaderSz {
				return errors.New("imcompleted buf readed")
			}
			copy(pb.head[:], buf[:HeaderSz])
			pb.payload = buf[HeaderSz:]

			if logRawPacket {
				payload := ""
				if pb.head[0] == CmdOpenStream {
					payload = string(pb.payload)
				}
				log.Printf("%v %v->%v %v %v\n", pb.head.CmdStr(), pb.head.Src(), pb.head.Dist(), pb.head, payload)
			}

			return nil
		}

		// 忽略非BinaryMessage数据包
		log.Printf("ignored ws message. type=%v size=%v\n", msgType, len(buf))
	}
}

func (ws *wsPacketIO) Close() error {
	return ws.conn.Close()
}

//
// implement for PacketConn
//
//
type PacketConn struct {
	PacketIO
	chanPacketBufs chan *Buffer
	chanLock       sync.RWMutex
	chanClosed     bool
	onceClose      sync.Once
}

// NewTcpPacketConn 基于原始TCP连接进行协议升级
func NewTcpPacketConn(conn net.Conn) *PacketConn {
	return &PacketConn{
		PacketIO:       NewTcpPacketIO(conn),
		chanPacketBufs: make(chan *Buffer, 1024),
	}
}

func NewWsPacketConn(wsconn *websocket.Conn) *PacketConn {
	return &PacketConn{
		PacketIO:       NewWsPacketIO(wsconn),
		chanPacketBufs: make(chan *Buffer, 1024),
	}
}

func (pc *PacketConn) NonblockWritePacket(pb *Buffer, waitResult bool) error {
	pc.chanLock.RLock()
	defer pc.chanLock.RUnlock()
	if pc.chanClosed {
		return errors.New("nonblocking chan closed")
	}

	var done chan struct{}

	if waitResult {
		done = make(chan struct{}, 1)
		defer func() {
			close(done)
			pb.writeDone = nil
		}()
		pb.writeDone = done
	}

	pc.chanPacketBufs <- pb

	if waitResult {
		select {
		case <-done:
			return nil
		case <-time.After(time.Second * 5):
			return errors.New("nonblock write timeout")
		}
	}

	return nil
}

func (pc *PacketConn) WriteLoop() error {
	defer pc.Close()

	var err error
	for pb := range pc.chanPacketBufs {
		err = pc.WritePacket(pb)
		if err != nil {
			return err
		}
	}
	return nil
}

func (pc *PacketConn) Close() error {
	var err error
	pc.onceClose.Do(func() {
		pc.chanLock.Lock()
		pc.chanClosed = true
		close(pc.chanPacketBufs)
		pc.chanLock.Unlock()
		err = pc.PacketIO.Close()
	})
	return err
}
