package stream

import (
	"errors"

	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/wire"
)

// ErrNilWriter is returned when a Stream has no packet sink.
var ErrNilWriter = errors.New("stream: nil writer")

// Endpoint is the frame sink used by a Stream. The node implements it and
// routes the frame to the local dispatcher or the link; tests can use a
// capture endpoint.
//
// SendFrame takes ownership of the frame. An implementation that queues the
// frame asynchronously must copy the payload before returning, because the
// caller (Stream.Write) may reuse its buffer as soon as Write returns.
type Endpoint interface {
	SendFrame(*wire.Frame) error
}

// legacyEndpoint adapts the old packet.Writer API to Endpoint. It is the
// compatibility bridge for stream.New/NewDialStream/NewAcceptStream; the node
// uses its own frame endpoint.
type legacyEndpoint struct {
	w Writer
}

func (l legacyEndpoint) SendFrame(f *wire.Frame) error {
	defer f.Release()
	if l.w == nil {
		return ErrNilWriter
	}

	b := packet.GetBuffer()
	defer packet.PutBuffer(b)
	if err := (wire.Codec{}).EncodeHeader(f.Header(), b.Head[:]); err != nil {
		return err
	}
	// The legacy packet.Buffer path predates the class bit; its consumers
	// switch on packet.Cmd* values and must not see bit 7.
	b.Head[0] &^= 0x80
	b.Payload = append(b.Payload[:0], f.Payload()...)
	return l.w.WriteBuffer(b)
}
