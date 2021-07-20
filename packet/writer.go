package packet

import (
	"net"

	"github.com/gorilla/websocket"
)

type Writer interface {
	WriteBuffer(buf *Buffer) error
}

//
// Writer implements with net.Conn
//
type connWriter struct {
	conn net.Conn
}

func NewConnWriter(conn net.Conn) Writer {
	return &connWriter{conn}
}

func (writer *connWriter) WriteBuffer(buf *Buffer) error {
	_, err := writer.conn.Write(buf.Head[:])
	if err != nil {
		return err
	}
	if len(buf.Payload) > 0 {
		_, err = writer.conn.Write(buf.Payload)
		if err != nil {
			return err
		}
	}
	return nil
}

//
// Writer implements with websocket.Conn
//
type wsWriter struct {
	wsconn *websocket.Conn
}

func NewWsWriter(wsconn *websocket.Conn) Writer {
	return &wsWriter{wsconn}
}

func (writer *wsWriter) WriteBuffer(pb *Buffer) error {
	w, err := writer.wsconn.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return err
	}
	defer w.Close()

	_, err = w.Write(pb.Head[:])
	if err != nil {
		return err
	}
	if len(pb.Payload) > 0 {
		_, err = w.Write(pb.Payload)
		if err != nil {
			return err
		}
	}

	return nil
}
