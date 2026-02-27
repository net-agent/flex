package packet

import (
	"io"
	"net"
)

type Conn interface {
	io.Closer
	Reader
	Writer
	GetRawConn() net.Conn
}

type connImpl struct {
	raw net.Conn
	io.Closer
	Reader
	Writer
}

func NewWithConn(conn net.Conn) Conn {
	return &connImpl{
		raw:    conn,
		Closer: conn,
		Reader: NewConnReader(conn),
		Writer: NewConnWriter(conn),
	}
}

func (impl *connImpl) GetRawConn() net.Conn {
	return impl.raw
}

func (impl *connImpl) WriteBufferBatch(bufs []*Buffer) error {
	if bw, ok := impl.Writer.(BatchWriter); ok {
		return bw.WriteBufferBatch(bufs)
	}
	for _, buf := range bufs {
		if err := impl.WriteBuffer(buf); err != nil {
			return err
		}
	}
	return nil
}
