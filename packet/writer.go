package packet

import (
	"errors"
	"net"
	"sync"
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

// BatchWriter extends Writer with batch write support using writev.
type BatchWriter interface {
	Writer
	WriteBufferBatch(bufs []*Buffer) error
}

// Writer implements with net.Conn
type connWriter struct {
	conn net.Conn
	mu   sync.Mutex
}

func NewConnWriter(conn net.Conn) Writer {
	return &connWriter{
		conn: conn,
	}
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

	w.mu.Lock()
	defer w.mu.Unlock()
	_, err := buf.WriteTo(w.conn)
	return err
}

func (w *connWriter) WriteBufferBatch(bufs []*Buffer) error {
	if len(bufs) == 0 {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	vecs := make(net.Buffers, 0, len(bufs)*2)
	for _, buf := range bufs {
		vecs = append(vecs, buf.Head[:])
		if len(buf.Payload) > 0 {
			vecs = append(vecs, buf.Payload)
		}
	}
	_, err := vecs.WriteTo(w.conn)
	return err
}

// Writer implements with websocket.Conn
type wsWriter struct {
	wsconn *websocket.Conn
	mu     sync.Mutex
}

func NewWsWriter(wsconn *websocket.Conn) Writer {
	return &wsWriter{
		wsconn: wsconn,
	}
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

	w.mu.Lock()
	defer w.mu.Unlock()
	nw, err := w.wsconn.NextWriter(websocket.BinaryMessage)
	if err != nil {
		return err
	}
	defer nw.Close()

	_, err = buf.WriteTo(nw)
	return err
}
