package packet

import (
	"io"
	"net"
	"time"

	"github.com/net-agent/flex/v3/internal/framing"
)

type Conn interface {
	io.Closer
	Reader
	Writer
	GetRawConn() net.Conn
}

// connImpl is both the legacy packet.Conn and a framing.Transport. The two
// views share the same net.Conn and are not meant to be used concurrently: a
// node/link owns the connection after construction and uses the framing view;
// the packet.Reader/Writer remain for compatibility and tests.
type connImpl struct {
	raw net.Conn
	ft  framing.Transport
	io.Closer
	Reader
	Writer
}

var _ framing.Transport = (*connImpl)(nil)

func NewWithConn(conn net.Conn) Conn {
	return &connImpl{
		raw:    conn,
		ft:     framing.NewTCP(conn),
		Closer: conn,
		Reader: NewConnReader(conn),
		Writer: NewConnWriter(conn),
	}
}

func (impl *connImpl) GetRawConn() net.Conn {
	return impl.raw
}

func (impl *connImpl) ReadHeader(dst []byte) error {
	return impl.ft.ReadHeader(dst)
}

func (impl *connImpl) ReadPayload(n int) ([]byte, error) {
	return impl.ft.ReadPayload(n)
}

func (impl *connImpl) ReadPayloadInto(dst []byte) error {
	if pr, ok := impl.ft.(framing.PayloadReader); ok {
		return pr.ReadPayloadInto(dst)
	}
	p, err := impl.ft.ReadPayload(len(dst))
	if err != nil {
		return err
	}
	copy(dst, p)
	return nil
}

func (impl *connImpl) WriteFrames(frames []framing.FrameBytes) error {
	return impl.ft.WriteFrames(frames)
}

func (impl *connImpl) SetReadDeadline(t time.Time) error {
	return impl.ft.SetReadDeadline(t)
}

func (impl *connImpl) SetWriteDeadline(t time.Time) error {
	return impl.ft.SetWriteDeadline(t)
}

func (impl *connImpl) LocalAddr() net.Addr  { return impl.ft.LocalAddr() }
func (impl *connImpl) RemoteAddr() net.Addr { return impl.ft.RemoteAddr() }

func (impl *connImpl) WriteBufferBatch(bufs []*Buffer) error {
	if bw, ok := impl.Writer.(BatchWriter); ok {
		return bw.WriteBufferBatch(bufs)
	}
	for _, buf := range bufs {
		if err := impl.WriteBuffer(buf); err != nil {
			return err
		}
	}
	return nil
}
