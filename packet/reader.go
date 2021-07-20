package packet

import (
	"errors"
	"io"
	"net"

	"github.com/gorilla/websocket"
)

var (
	ErrBadDataType = errors.New("err: bad data type")
)

type Reader interface {
	ReadBuffer() (*Buffer, error)
}

//
// Reader implements with net.Conn
//
type connReader struct {
	conn net.Conn
}

func NewConnReader(conn net.Conn) Reader {
	return &connReader{conn}
}

func (reader *connReader) ReadBuffer() (*Buffer, error) {
	pb := NewBuffer()

	_, err := io.ReadFull(reader.conn, pb.Head[:])
	if err != nil {
		return nil, err
	}

	sz := pb.PayloadSize()
	if sz > 0 {
		pb.Payload = make([]byte, sz)
		_, err := io.ReadFull(reader.conn, pb.Payload)
		if err != nil {
			return nil, err
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

func (reader *wsReader) ReadBuffer() (*Buffer, error) {
	buf := NewBuffer()

	mtype, data, err := reader.wsconn.ReadMessage()
	if err != nil {
		return nil, err
	}
	if mtype != websocket.BinaryMessage {
		return nil, ErrBadDataType
	}

	// pb := NewPacketBufs()
	copy(buf.Head[:], data[:HeaderSz])
	buf.Payload = data[HeaderSz:]

	return buf, nil
}
