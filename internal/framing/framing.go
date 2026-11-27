// Package framing owns flex's byte/message transports. It knows how to move an
// encoded frame over a net.Conn or a WebSocket, and nothing else.
//
// It is shared by packet (the legacy public transport) and link (the new
// composition root) so both implement exactly the same two-phase framing
// contract without duplicating TCP/WS code.
package framing

import (
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/gorilla/websocket"
	"github.com/net-agent/flex/v3/wire"
)

var (
	// ErrShortFrame means the transport delivered fewer than HeaderSize bytes.
	ErrShortFrame = errors.New("link: short frame")
	// ErrFrameSizeMismatch means the frame body does not match the header size.
	ErrFrameSizeMismatch = errors.New("link: frame size mismatch")
	// ErrBadMessageType means a WebSocket transport received a non-binary message.
	ErrBadMessageType = errors.New("link: bad websocket message type")
	// ErrFrameNotConsumed means ReadHeader was called before the previous body
	// was consumed. It indicates a framing bug in the caller.
	ErrFrameNotConsumed = errors.New("link: previous frame body not consumed")
)

// FrameBytes is one encoded frame ready for the transport: an 11-byte header
// and its payload. The payload is borrowed until WriteFrames returns.
type FrameBytes struct {
	Header  [wire.HeaderSize]byte
	Payload []byte
}

// Transport moves encoded frames over a byte stream or a message transport.
//
// PayloadReader is an optional zero-allocation extension of Transport: the
// caller supplies the destination buffer. The link uses it when available and
// falls back to ReadPayload (which allocates) for custom transports.
type PayloadReader interface {
	ReadPayloadInto(dst []byte) error
}

// ReadHeader/ReadPayload are deliberately two-phase: the secure link must
// decrypt the header before it knows how many payload bytes to read.
// Implementations must make the header slice valid until the next ReadHeader.
type Transport interface {
	ReadHeader(dst []byte) error
	ReadPayload(n int) ([]byte, error)
	WriteFrames(frames []FrameBytes) error
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
	LocalAddr() net.Addr
	RemoteAddr() net.Addr
	Close() error
}

// TCPTransport frames packets over a net.Conn (TCP, TLS, net.Pipe, ...).
type TCPTransport struct {
	conn net.Conn
}

// NewTCP wraps a net.Conn. The transport takes ownership of conn.
func NewTCP(conn net.Conn) *TCPTransport {
	return &TCPTransport{conn: conn}
}

// ReadHeader reads exactly HeaderSize bytes into dst.
func (t *TCPTransport) ReadHeader(dst []byte) error {
	if len(dst) < wire.HeaderSize {
		return io.ErrShortBuffer
	}
	_, err := io.ReadFull(t.conn, dst[:wire.HeaderSize])
	return err
}

// ReadPayload reads exactly n bytes. n is bounded by the header's uint16 size.
func (t *TCPTransport) ReadPayload(n int) ([]byte, error) {
	if n <= 0 {
		return nil, nil
	}
	if n > wire.MaxPayloadSize {
		return nil, fmt.Errorf("%w: payload=%d", ErrFrameSizeMismatch, n)
	}
	p := make([]byte, n)
	if err := t.ReadPayloadInto(p); err != nil {
		return nil, err
	}
	return p, nil
}

// ReadPayloadInto reads exactly len(dst) bytes into the caller's buffer.
func (t *TCPTransport) ReadPayloadInto(dst []byte) error {
	if len(dst) == 0 {
		return nil
	}
	if len(dst) > wire.MaxPayloadSize {
		return fmt.Errorf("%w: payload=%d", ErrFrameSizeMismatch, len(dst))
	}
	_, err := io.ReadFull(t.conn, dst)
	return err
}

// WriteFrames writes all frames with as few syscalls as possible. On TCP this
// is a single writev; the header and payload stay in separate iovecs, so a data
// frame's payload is never copied or inspected.
func (t *TCPTransport) WriteFrames(frames []FrameBytes) error {
	if len(frames) == 0 {
		return nil
	}
	bufs := make(net.Buffers, 0, len(frames)*2)
	for i := range frames {
		bufs = append(bufs, frames[i].Header[:])
		if len(frames[i].Payload) > 0 {
			bufs = append(bufs, frames[i].Payload)
		}
	}
	_, err := bufs.WriteTo(t.conn)
	return err
}

func (t *TCPTransport) SetReadDeadline(deadline time.Time) error {
	return t.conn.SetReadDeadline(deadline)
}

func (t *TCPTransport) SetWriteDeadline(deadline time.Time) error {
	return t.conn.SetWriteDeadline(deadline)
}

func (t *TCPTransport) LocalAddr() net.Addr  { return t.conn.LocalAddr() }
func (t *TCPTransport) RemoteAddr() net.Addr { return t.conn.RemoteAddr() }
func (t *TCPTransport) Close() error         { return t.conn.Close() }

// WSTransport frames one flex packet per WebSocket binary message.
type WSTransport struct {
	conn *websocket.Conn
	rest []byte
}

// NewWS wraps a WebSocket connection. The transport takes ownership of conn.
func NewWS(conn *websocket.Conn) *WSTransport {
	conn.SetReadLimit(int64(wire.MaxPayloadSize) + int64(wire.HeaderSize))
	return &WSTransport{conn: conn}
}

// ReadHeader reads one binary message and returns its first HeaderSize bytes.
// The remainder is cached for the following ReadPayload call.
func (t *WSTransport) ReadHeader(dst []byte) error {
	if len(dst) < wire.HeaderSize {
		return io.ErrShortBuffer
	}
	if t.rest != nil {
		return ErrFrameNotConsumed
	}
	mt, data, err := t.conn.ReadMessage()
	if err != nil {
		return err
	}
	if mt != websocket.BinaryMessage {
		return ErrBadMessageType
	}
	if len(data) < wire.HeaderSize {
		return fmt.Errorf("%w: got=%d", ErrShortFrame, len(data))
	}
	copy(dst[:wire.HeaderSize], data[:wire.HeaderSize])
	t.rest = data[wire.HeaderSize:]
	return nil
}

// ReadPayload returns the cached message body and validates its length against
// the (already decrypted) header size.
func (t *WSTransport) ReadPayload(n int) ([]byte, error) {
	rest := t.rest
	t.rest = nil
	if n <= 0 {
		if len(rest) != 0 {
			return nil, fmt.Errorf("%w: frame=%d header=0", ErrFrameSizeMismatch, len(rest))
		}
		return nil, nil
	}
	if len(rest) != n {
		return nil, fmt.Errorf("%w: frame=%d header=%d", ErrFrameSizeMismatch, len(rest), n)
	}
	return rest, nil
}

// ReadPayloadInto copies the cached message body into dst.
func (t *WSTransport) ReadPayloadInto(dst []byte) error {
	rest := t.rest
	t.rest = nil
	if len(dst) == 0 {
		if len(rest) != 0 {
			return fmt.Errorf("%w: frame=%d header=0", ErrFrameSizeMismatch, len(rest))
		}
		return nil
	}
	if len(rest) != len(dst) {
		return fmt.Errorf("%w: frame=%d header=%d", ErrFrameSizeMismatch, len(rest), len(dst))
	}
	copy(dst, rest)
	return nil
}

// WriteFrames writes one binary message per frame. A WebSocket message has a
// single frame boundary, so batching stops at the message boundary; the link's
// fair scheduler still batches at the transport level for TCP.
func (t *WSTransport) WriteFrames(frames []FrameBytes) error {
	for i := range frames {
		nw, err := t.conn.NextWriter(websocket.BinaryMessage)
		if err != nil {
			return err
		}
		if _, err := nw.Write(frames[i].Header[:]); err != nil {
			nw.Close()
			return err
		}
		if len(frames[i].Payload) > 0 {
			if _, err := nw.Write(frames[i].Payload); err != nil {
				nw.Close()
				return err
			}
		}
		if err := nw.Close(); err != nil {
			return err
		}
	}
	return nil
}

func (t *WSTransport) SetReadDeadline(deadline time.Time) error {
	return t.conn.SetReadDeadline(deadline)
}

func (t *WSTransport) SetWriteDeadline(deadline time.Time) error {
	return t.conn.SetWriteDeadline(deadline)
}

func (t *WSTransport) LocalAddr() net.Addr  { return t.conn.LocalAddr() }
func (t *WSTransport) RemoteAddr() net.Addr { return t.conn.RemoteAddr() }
func (t *WSTransport) Close() error         { return t.conn.Close() }
