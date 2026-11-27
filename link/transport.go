package link

import (
	"net"

	"github.com/gorilla/websocket"
	"github.com/net-agent/flex/v4/internal/framing"
)

// Transport and FrameBytes are aliases of the shared framing types. Keeping
// them here gives the link package a stable public surface while packet and
// link share one implementation.
type (
	Transport  = framing.Transport
	FrameBytes = framing.FrameBytes
)

var (
	ErrShortFrame        = framing.ErrShortFrame
	ErrFrameSizeMismatch = framing.ErrFrameSizeMismatch
	ErrBadMessageType    = framing.ErrBadMessageType
	ErrFrameNotConsumed  = framing.ErrFrameNotConsumed
)

// NewTCP wraps a net.Conn as a frame transport.
func NewTCP(conn net.Conn) Transport { return framing.NewTCP(conn) }

// NewWS wraps a WebSocket connection as a frame transport.
func NewWS(conn *websocket.Conn) Transport { return framing.NewWS(conn) }
