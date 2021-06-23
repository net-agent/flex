package flex

import (
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type PacketIO interface {
	Origin() interface{}
	WritePacket(pb *PacketBufs) error
	ReadPacket(pb *PacketBufs) error
	io.Closer
}

//
// tcp conn implement
//
type connPacketIO struct {
	conn  net.Conn
	wlock sync.Mutex
}

func NewConnPacketIO(conn net.Conn) PacketIO {
	return &connPacketIO{
		conn: conn,
	}
}

func (c *connPacketIO) Origin() interface{} {
	return c.conn
}

func (c *connPacketIO) ReadPacket(pb *PacketBufs) error {
	_, err := pb.ReadFrom(c.conn)
	if err != nil {
		c.conn.Close()
	}
	return err
}

func (c *connPacketIO) WritePacket(pb *PacketBufs) (ret error) {
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
	conn *websocket.Conn
}

func NewWsPacketIO(wsconn *websocket.Conn) PacketIO {
	return &wsPacketIO{
		conn: wsconn,
	}
}

func (ws *wsPacketIO) Origin() interface{} {
	return ws.conn
}

func (ws *wsPacketIO) WritePacket(pb *PacketBufs) error {
	if len(pb.payload) == 0 {
		return ws.conn.WriteMessage(websocket.BinaryMessage, pb.head[:])
	}
	buf := make([]byte, packetHeaderSize+len(pb.payload))
	copy(buf[:packetHeaderSize], pb.head[:])
	copy(buf[packetHeaderSize:], pb.payload)
	return ws.conn.WriteMessage(websocket.BinaryMessage, buf)
}

func (ws *wsPacketIO) ReadPacket(pb *PacketBufs) error {
	for {
		msgType, buf, err := ws.conn.ReadMessage()
		if err != nil {
			return err
		}
		if msgType == websocket.BinaryMessage {
			if len(buf) < packetHeaderSize {
				return errors.New("imcompleted buf readed")
			}
			copy(pb.head[:], buf[:packetHeaderSize])
			pb.payload = buf[packetHeaderSize:]
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
	chanPacketBufs chan *PacketBufs
}

// NewPacketConnFromConn 基于原始TCP连接进行协议升级
func NewPacketConnFromConn(conn net.Conn) *PacketConn {
	return &PacketConn{
		PacketIO:       NewConnPacketIO(conn),
		chanPacketBufs: make(chan *PacketBufs, 1024),
	}
}

func NewPacketConnFromWebsocket(wsconn *websocket.Conn) *PacketConn {
	return &PacketConn{
		PacketIO:       NewWsPacketIO(wsconn),
		chanPacketBufs: make(chan *PacketBufs, 1024),
	}
}

// todo: 基于WebSocket协议升级为PacketConn（复用ws的Message读写）
func NewPacketWsConn(wsconn net.Conn) *PacketConn {
	return nil
}

func (pc *PacketConn) NonblockWritePacket(pb *PacketBufs, waitResult bool) error {
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
			return errors.New("timeout")
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
