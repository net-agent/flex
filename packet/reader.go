package packet

import (
	"errors"
	"io"
	"net"
	"time"

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
	// 如果30秒读不到任何数据，则会报错关闭
	// 所以心跳包的时间间隔不应该超过这个数值
	err := reader.conn.SetReadDeadline(time.Now().Add(time.Second * 30))
	if err != nil {
		return nil, err
	}
	pb := NewBuffer(nil)

	_, err = io.ReadFull(reader.conn, pb.Head[:])
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

	reader.conn.SetReadDeadline(time.Time{})
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
