package ws

import (
	"net"
	"time"

	"github.com/gorilla/websocket"
	"github.com/net-agent/flex/v3/internal/framing"
	"github.com/net-agent/flex/v3/packet"
)

// connImpl is both the legacy packet.Conn and a framing.Transport. The two
// views share the same websocket.Conn and are not meant to be used
// concurrently; a link owns the connection after construction.
type connImpl struct {
	raw    net.Conn
	closer interface{ Close() error }
	ft     framing.Transport
	packet.Reader
	packet.Writer
}

var _ framing.Transport = (*connImpl)(nil)

// NewConn wraps a websocket.Conn as a packet.Conn.
func NewConn(wsconn *websocket.Conn) packet.Conn {
	return &connImpl{
		raw:    wsconn.UnderlyingConn(),
		closer: wsconn,
		ft:     framing.NewWS(wsconn),
		Reader: NewReader(wsconn),
		Writer: NewWriter(wsconn),
	}
}

func (c *connImpl) Close() error         { return c.closer.Close() }
func (c *connImpl) GetRawConn() net.Conn { return c.raw }

func (c *connImpl) ReadHeader(dst []byte) error {
	return c.ft.ReadHeader(dst)
}

func (c *connImpl) ReadPayload(n int) ([]byte, error) {
	return c.ft.ReadPayload(n)
}

func (c *connImpl) ReadPayloadInto(dst []byte) error {
	if pr, ok := c.ft.(framing.PayloadReader); ok {
		return pr.ReadPayloadInto(dst)
	}
	p, err := c.ft.ReadPayload(len(dst))
	if err != nil {
		return err
	}
	copy(dst, p)
	return nil
}

func (c *connImpl) WriteFrames(frames []framing.FrameBytes) error {
	return c.ft.WriteFrames(frames)
}

func (c *connImpl) SetReadDeadline(t time.Time) error {
	return c.ft.SetReadDeadline(t)
}

func (c *connImpl) SetWriteDeadline(t time.Time) error {
	return c.ft.SetWriteDeadline(t)
}

func (c *connImpl) LocalAddr() net.Addr  { return c.ft.LocalAddr() }
func (c *connImpl) RemoteAddr() net.Addr { return c.ft.RemoteAddr() }
