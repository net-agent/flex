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

func ReadFromConn(conn net.Conn) (*Buffer, error) {
	pb := NewPacketBufs()

	_, err := io.ReadFull(conn, pb.head[:])
	if err != nil {
		return nil, err
	}

	sz := pb.head.PayloadSize()
	if sz > 0 {
		pb.payload = make([]byte, sz)
		_, err := io.ReadFull(conn, pb.payload)
		if err != nil {
			return nil, err
		}
	}

	return pb, nil
}

func ReadFromWs(wsconn *websocket.Conn) (*Buffer, error) {
	pb := NewPacketBufs()

	mtype, data, err := wsconn.ReadMessage()
	if err != nil {
		return nil, err
	}
	if mtype != websocket.BinaryMessage {
		return nil, ErrBadDataType
	}

	// pb := NewPacketBufs()
	copy(pb.head[:], data[:HeaderSz])
	pb.payload = data[HeaderSz:]

	return pb, nil
}
