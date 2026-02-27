package ws

import (
	"errors"
	"time"

	"github.com/gorilla/websocket"
	"github.com/net-agent/flex/v3/packet"
)

var ErrBadDataType = errors.New("err: bad data type")

type wsReader struct {
	wsconn *websocket.Conn
}

// NewReader creates a packet.Reader backed by a websocket.Conn.
func NewReader(wsconn *websocket.Conn) packet.Reader {
	return &wsReader{wsconn}
}

func (r *wsReader) SetReadTimeout(timeout time.Duration) error {
	if timeout == 0 {
		return r.wsconn.SetReadDeadline(time.Time{})
	}
	return r.wsconn.SetReadDeadline(time.Now().Add(timeout))
}

func (r *wsReader) ReadBuffer() (*packet.Buffer, error) {
	buf := packet.GetBuffer()

	mtype, data, err := r.wsconn.ReadMessage()
	if err != nil {
		packet.PutBuffer(buf)
		return nil, err
	}
	if mtype != websocket.BinaryMessage {
		packet.PutBuffer(buf)
		return nil, ErrBadDataType
	}

	copy(buf.Head[:], data[:packet.HeaderSz])
	buf.Payload = data[packet.HeaderSz:]

	return buf, nil
}
