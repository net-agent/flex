package stream

import (
	"errors"
	"sync/atomic"

	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/wire"
)

var (
	ErrSendDataOversize = errors.New("send data oversize")
)

// Writer is the legacy packet sink. New code should use Endpoint.
type Writer interface {
	WriteBuffer(buf *packet.Buffer) error
}

// sender turns stream-level operations into frames. It no longer owns reusable
// packet buffers or per-direction mutexes: the endpoint/link serializes writes
// and owns frame lifetime.
type sender struct {
	ep      Endpoint
	src     wire.Addr
	dst     wire.Addr
	counter *int32
}

func newSender(w Writer, counter *int32) *sender {
	return &sender{ep: legacyEndpoint{w: w}, counter: counter}
}

func newSenderWithEndpoint(ep Endpoint, counter *int32) *sender {
	return &sender{ep: ep, counter: counter}
}

func (s *sender) SetSrc(ip, port uint16) {
	s.src = wire.Addr{IP: ip, Port: port}
}

func (s *sender) SetDst(ip, port uint16) {
	s.dst = wire.Addr{IP: ip, Port: port}
}

func (s *sender) SendData(buf []byte) error {
	if len(buf) == 0 {
		return nil
	}
	if len(buf) > packet.MaxPayloadSize {
		return ErrSendDataOversize
	}
	f, err := wire.NewData(s.src, s.dst, buf)
	if err != nil {
		return err
	}
	s.incr()
	if err := s.ep.SendFrame(f); err != nil {
		return err
	}
	return nil
}

// SendDataAck sends AckPushStreamData; the acknowledged byte count lives in the
// header, so there is no payload.
func (s *sender) SendDataAck(n uint16) error {
	s.incr()
	return s.ep.SendFrame(wire.NewAckData(n, s.src, s.dst))
}

func (s *sender) SendClose() error {
	f, err := wire.NewEmpty(wire.CmdCloseStream, s.src, s.dst)
	if err != nil {
		return err
	}
	s.incr()
	return s.ep.SendFrame(f)
}

func (s *sender) SendCloseAck() error {
	f, err := wire.NewEmpty(wire.AckCloseStream, s.src, s.dst)
	if err != nil {
		return err
	}
	s.incr()
	return s.ep.SendFrame(f)
}

func (s *sender) incr() {
	if s.counter != nil {
		atomic.AddInt32(s.counter, 1)
	}
}

// InterruptWrite is intentionally a no-op for now.
//
// NOTE(shared-conn):
// Multiple streams may multiplex over one shared net.Conn. A transport-level
// interrupt can impact unrelated streams, so we intentionally do not expose
// interrupt behavior from the default sender path until a stream-isolated
// cancellation mechanism is available.
func (s *sender) InterruptWrite() {}

func (s *sender) CanInterruptWrite() bool { return false }
