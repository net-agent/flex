package packet

import (
	"errors"
	"log"
	"net"
	"time"

	"github.com/gorilla/websocket"
)

var (
	ErrWriteHeaderFailed  = errors.New("write header failed")
	ErrWritePayloadFailed = errors.New("write payload failed")
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

func (w *connWriter) WriteBytes(buf []byte) (int, error) {
	return w.conn.Write(buf)
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
		return ErrWriteHeaderFailed
	}
	if len(buf.Payload) > 0 {
		_, err = writer.conn.Write(buf.Payload)
		if err != nil {
			return ErrWritePayloadFailed
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

	writer.wsconn.SetWriteDeadline(time.Now().Add(DefaultWriteDeadline))
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

func (writer *wsWriter) WriteBytes(buf []byte) (int, error) {
	// writer.wsconn.SetWriteDeadline(time.Now().Add(DefaultWriteDeadline))
	// w, err := writer.wsconn.NextWriter(websocket.BinaryMessage)
	// if err != nil {
	// 	return 0, err
	// }
	// defer w.Close()

	// _, err = w.Write(buf.Head[:])
	// if err != nil {
	// 	return err
	// }
	// if len(buf.Payload) > 0 {
	// 	_, err = w.Write(buf.Payload)
	// 	if err != nil {
	// 		return err
	// 	}
	// }
	return 0, errors.New("TODO")
}
