package stream

import (
	"errors"

	"github.com/net-agent/flex/v4/packet"
	"github.com/net-agent/flex/v4/wire"
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

	// Do not use the packet.Buffer pool here. The legacy Writer contract only
	// guarantees the buffer is valid for the duration of WriteBuffer; with
	// net.Pipe/packet.Pipe a receiver can still be copying the payload while
	// the sender returns the buffer to the pool, which reuses the backing array
	// under the receiver (a data race). A fresh buffer keeps ownership simple;
	// this path is compatibility-only, node frames use Endpoint instead.
	b := packet.NewBuffer()
	if err := (wire.Codec{}).EncodeHeader(f.Header(), b.Head[:]); err != nil {
		return err
	}
	// The legacy packet.Buffer path predates the class bit; its consumers
	// switch on packet.Cmd* values and must not see bit 7.
	b.Head[0] &^= 0x80
	b.Payload = append([]byte(nil), f.Payload()...)
	return l.w.WriteBuffer(b)
}
