package packet

import (
	"io"
	"net"

	"github.com/gorilla/websocket"
)

type Conn interface {
	io.Closer
	Reader
	Writer
}

type connImpl struct {
	io.Closer
	Reader
	Writer
}

func NewWithConn(conn net.Conn) Conn {
	return &connImpl{
		Closer: conn,
		Reader: NewConnReader(conn),
		Writer: NewConnWriter(conn),
	}
}

func NewWithWs(wsconn *websocket.Conn) Conn {
	return &connImpl{
		Closer: wsconn,
		Reader: NewWsReader(wsconn),
		Writer: NewWsWriter(wsconn),
	}
}
