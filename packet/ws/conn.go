package ws

import (
	"net"

	"github.com/gorilla/websocket"
	"github.com/net-agent/flex/v3/packet"
)

type connImpl struct {
	raw net.Conn
	closer interface{ Close() error }
	packet.Reader
	packet.Writer
}

// NewConn wraps a websocket.Conn as a packet.Conn.
func NewConn(wsconn *websocket.Conn) packet.Conn {
	return &connImpl{
		raw:    wsconn.UnderlyingConn(),
		closer: wsconn,
		Reader: NewReader(wsconn),
		Writer: NewWriter(wsconn),
	}
}

func (c *connImpl) Close() error         { return c.closer.Close() }
func (c *connImpl) GetRawConn() net.Conn  { return c.raw }
