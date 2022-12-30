package packet

import (
	"errors"
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
	SetWriteTimeout(dur time.Duration)
}

// Writer implements with net.Conn
type connWriter struct {
	conn net.Conn
}

func NewConnWriter(conn net.Conn) Writer {
	return &connWriter{conn}
}

func (w *connWriter) SetWriteTimeout(timeout time.Duration) {
	if timeout == 0 {
		w.conn.SetWriteDeadline(time.Time{})
	} else {
		w.conn.SetWriteDeadline(time.Now().Add(timeout))
	}
}

func (w *connWriter) WriteBuffer(buf *Buffer) (retErr error) {
	if buf == nil {
		return nil
	}

	_, err := buf.WriteTo(w.conn)
	return err
}

// Writer implements with websocket.Conn
type wsWriter struct {
	wsconn *websocket.Conn
}

func NewWsWriter(wsconn *websocket.Conn) Writer {
	return &wsWriter{wsconn}
}

func (w *wsWriter) SetWriteTimeout(timeout time.Duration) {
	if timeout == 0 {
		w.wsconn.SetWriteDeadline(time.Time{})
	} else {
		w.wsconn.SetWriteDeadline(time.Now().Add(timeout))
	}
}

func (w *wsWriter) WriteBuffer(buf *Buffer) (retErr error) {
	if buf == nil {
		return nil
	}

	nw, err := w.wsconn.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return err
	}
	defer nw.Close()

	_, err = buf.WriteTo(nw)
	return err
}
