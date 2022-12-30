package packet

import (
	"errors"
	"io"
	"net"
	"time"

	"github.com/gorilla/websocket"
)

var (
	ErrBadDataType       = errors.New("err: bad data type")
	ErrSetDeadlineFailed = errors.New("set deadline failed")
	ErrReadHeaderFailed  = errors.New("read header failed")
	ErrReadPayloadFailed = errors.New("read payload failed")
)

type Reader interface {
	ReadBuffer() (*Buffer, error)
	SetReadTimeout(time.Duration) error
}

// Reader implements with net.Conn
type connReader struct {
	conn net.Conn
}

func NewConnReader(conn net.Conn) Reader {
	return &connReader{conn}
}

func (reader *connReader) SetReadTimeout(timeout time.Duration) error {
	if timeout == 0 {
		return reader.conn.SetReadDeadline(time.Time{})
	}
	return reader.conn.SetReadDeadline(time.Now().Add(timeout))
}

func (reader *connReader) ReadBuffer() (retBuf *Buffer, retErr error) {
	pb := NewBuffer(nil)

	_, err := io.ReadFull(reader.conn, pb.Head[:])
	if err != nil {
		return nil, ErrReadHeaderFailed
	}

	sz := pb.PayloadSize()
	if sz > 0 {
		pb.Payload = make([]byte, sz)
		_, err := io.ReadFull(reader.conn, pb.Payload)
		if err != nil {
			return nil, ErrReadPayloadFailed
		}
	}

	return pb, nil
}

//
// Reader implements with websocket.Conn
//

type wsReader struct {
	wsconn *websocket.Conn
}

func NewWsReader(wsconn *websocket.Conn) Reader {
	return &wsReader{wsconn}
}

func (reader *wsReader) SetReadTimeout(timeout time.Duration) error {
	if timeout == 0 {
		return reader.wsconn.SetReadDeadline(time.Time{})
	}
	return reader.wsconn.SetReadDeadline(time.Now().Add(timeout))
}

func (reader *wsReader) ReadBuffer() (retBuf *Buffer, retErr error) {
	buf := NewBuffer(nil)

	mtype, data, err := reader.wsconn.ReadMessage()
	if err != nil {
		return nil, err
	}
	if mtype != websocket.BinaryMessage {
		return nil, ErrBadDataType
	}

	copy(buf.Head[:], data[:HeaderSz])
	buf.Payload = data[HeaderSz:]

	return buf, nil
}
