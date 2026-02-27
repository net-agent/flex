package ws

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/net-agent/flex/v3/packet"
)

type wsWriter struct {
	wsconn *websocket.Conn
	mu     sync.Mutex
}

// NewWriter creates a packet.Writer backed by a websocket.Conn.
func NewWriter(wsconn *websocket.Conn) packet.Writer {
	return &wsWriter{wsconn: wsconn}
}

func (w *wsWriter) SetWriteTimeout(timeout time.Duration) {
	if timeout == 0 {
		w.wsconn.SetWriteDeadline(time.Time{})
	} else {
		w.wsconn.SetWriteDeadline(time.Now().Add(timeout))
	}
}

func (w *wsWriter) WriteBuffer(buf *packet.Buffer) error {
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
