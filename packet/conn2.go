package packet

import (
	"net"

	"github.com/gorilla/websocket"
)

type Conn struct {
}

func NewWithConn(conn net.Conn) *Conn {
	return &Conn{}
}

func NewWithWs(wsconn *websocket.Conn) *Conn {
	return &Conn{}
}

func (c *Conn) SendCommand(cmd byte, payload []byte)       {}
func (c *Conn) PushSessionData(sid uint64, payload []byte) {}
func (c *Conn) ListenCommand(cmd byte, callback error)     {}
func (c *Conn) RecvSessionData(sid uint64, callback error) {}
