package packet

import (
	"log"
	"net"
	"time"

	"github.com/gorilla/websocket"
)

type Writer interface {
	WriteBuffer(buf *Buffer) error
}

// Writer implements with net.Conn
type connWriter struct {
	conn net.Conn
}

func NewConnWriter(conn net.Conn) Writer {
	return &connWriter{conn}
}

func (writer *connWriter) WriteBuffer(buf *Buffer) (retErr error) {
	if LOG_WRITE_BUFFER_HEADER {
		start := time.Now()
		defer func() {
			log.Printf("[W]%v timeuse=%v\n", buf.HeaderString(), time.Since(start))
		}()
	}
	err := writer.conn.SetWriteDeadline(time.Now().Add(DefaultWriteDeadline))
	if err != nil {
		return err
	}
	_, err = writer.conn.Write(buf.Head[:])
	if err != nil {
		return err
	}
	if len(buf.Payload) > 0 {
		_, err = writer.conn.Write(buf.Payload)
		if err != nil {
			return err
		}
	}
	writer.conn.SetWriteDeadline(time.Time{})
	return nil
}

// Writer implements with websocket.Conn
type wsWriter struct {
	wsconn *websocket.Conn
}

func NewWsWriter(wsconn *websocket.Conn) Writer {
	return &wsWriter{wsconn}
}

func (writer *wsWriter) WriteBuffer(buf *Buffer) (retErr error) {
	if LOG_WRITE_BUFFER_HEADER {
		start := time.Now()
		defer func() {
			log.Printf("[W]%v timeuse=%v\n", buf.HeaderString(), time.Since(start))
		}()
	}

	w, err := writer.wsconn.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return err
	}
	defer w.Close()

	_, err = w.Write(buf.Head[:])
	if err != nil {
		return err
	}
	if len(buf.Payload) > 0 {
		_, err = w.Write(buf.Payload)
		if err != nil {
			return err
		}
	}

	return nil
}
