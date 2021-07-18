package packet

import (
	"net"

	"github.com/gorilla/websocket"
)

type Conn interface {
	Reader
	Writer
}

type connImpl struct {
	Reader
	Writer
}

func NewWithConn(conn net.Conn) Conn {
	return &connImpl{
		Reader: NewConnReader(conn),
		Writer: NewConnWriter(conn),
	}
}

func NewWithWs(wsconn *websocket.Conn) Conn {
	return &connImpl{
		Reader: NewWsReader(wsconn),
		Writer: NewWsWriter(wsconn),
	}
}
