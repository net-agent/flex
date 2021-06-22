package flex

import (
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

type PacketIO interface {
	WritePacket(pb *PacketBufs) error
	ReadPacket(pb *PacketBufs) error
	io.Closer
}

//
// tcp conn implement
//
type connPacketRWC struct {
	conn  net.Conn
	wlock sync.Mutex
}

func NewConnPacketIO(conn net.Conn) PacketIO {
	return &connPacketRWC{
		conn: conn,
	}
}

func (c *connPacketRWC) ReadPacket(pb *PacketBufs) error {
	_, err := pb.ReadFrom(c.conn)
	if err != nil {
		c.conn.Close()
	}
	return err
}

func (c *connPacketRWC) WritePacket(pb *PacketBufs) (ret error) {
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

func (c *connPacketRWC) Close() error {
	return c.conn.Close()
}

//
// implement for PacketConn
//
//
type PacketConn struct {
	PacketIO
	chanPacketBufs chan *PacketBufs
	raw            net.Conn
}

// NewPacketConn 基于原始TCP连接进行协议升级
func NewPacketConn(conn net.Conn) *PacketConn {
	return &PacketConn{
		raw:            conn,
		PacketIO:       NewConnPacketIO(conn),
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
