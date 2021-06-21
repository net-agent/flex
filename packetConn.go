package flex

import (
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

type PacketWriter interface {
	WritePacket(pb *PacketBufs) error
}

type PacketReader interface {
	ReadPacket(pb *PacketBufs) error
}

type PacketReadWriteCloser interface {
	PacketWriter
	PacketReader
	io.Closer
}

//
// tcp conn implement
//
type connPacketRWC struct {
	conn  net.Conn
	wlock sync.Mutex
}

func NewConnPacketIO(conn net.Conn) PacketReadWriteCloser {
	return &connPacketRWC{
		conn: conn,
	}
}

func (c *connPacketRWC) ReadPacket(pb *PacketBufs) error {
	_, err := pb.ReadFrom(c.conn)
	return err
}

func (c *connPacketRWC) WritePacket(pb *PacketBufs) error {
	c.wlock.Lock()
	defer c.wlock.Unlock()

	c.conn.Write(pb.head[:])
	c.conn.Write(pb.payload)

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
	PacketReadWriteCloser
	chanPacketBufs chan *PacketBufs
	raw            net.Conn
}

func NewPacketConn(conn net.Conn) *PacketConn {
	return &PacketConn{
		raw:                   conn,
		PacketReadWriteCloser: NewConnPacketIO(conn),
		chanPacketBufs:        make(chan *PacketBufs, 1024),
	}
}

func (pc *PacketConn) NonblockWritePacket(pb *PacketBufs, waitResult bool) error {
	var done chan struct{}

	if waitResult {
		done = make(chan struct{}, 1)
		defer close(done)
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

func (pc *PacketConn) WriteLoop() {
	var err error
	for pb := range pc.chanPacketBufs {
		err = pc.WritePacket(pb)
		if err != nil {
			pc.EmitError(err)
			return
		}
	}
}

func (pc *PacketConn) EmitError(err error) {
	log.Printf("[packetConn] err=%v\n", err)
}
