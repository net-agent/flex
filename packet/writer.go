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
	SetWriteTimeout(dur time.Duration)
}

// Writer implements with net.Conn
type connWriter struct {
	conn         net.Conn
	writeTimeout time.Duration
}

func NewConnWriter(conn net.Conn) Writer {
	return &connWriter{conn, DefaultWriteTimeout}
}

func (w *connWriter) SetWriteTimeout(dur time.Duration) { w.writeTimeout = dur }

func (w *connWriter) WriteBuffer(buf *Buffer) (retErr error) {
	if buf == nil {
		return nil
	}
	if LOG_WRITE_BUFFER_HEADER {
		start := time.Now()
		defer func() {
			log.Printf("[W]%v timeuse=%v\n", buf.HeaderString(), time.Since(start))
		}()
	}
	err := w.conn.SetWriteDeadline(time.Now().Add(w.writeTimeout))
	if err != nil {
		return err
	}
	_, err = buf.WriteTo(w.conn)
	w.conn.SetWriteDeadline(time.Time{})
	return err
}

// Writer implements with websocket.Conn
type wsWriter struct {
	wsconn       *websocket.Conn
	writeTimeout time.Duration
}

func NewWsWriter(wsconn *websocket.Conn) Writer {
	return &wsWriter{wsconn, DefaultWriteTimeout}
}

func (w *wsWriter) SetWriteTimeout(dur time.Duration) { w.writeTimeout = dur }

func (w *wsWriter) WriteBuffer(buf *Buffer) (retErr error) {
	if buf == nil {
		return nil
	}
	if LOG_WRITE_BUFFER_HEADER {
		start := time.Now()
		defer func() {
			log.Printf("[W]%v timeuse=%v\n", buf.HeaderString(), time.Since(start))
		}()
	}

	w.wsconn.SetWriteDeadline(time.Now().Add(w.writeTimeout))
	nw, err := w.wsconn.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return err
	}
	defer nw.Close()

	_, err = buf.WriteTo(nw)
	return err
}
