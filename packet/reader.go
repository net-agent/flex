package packet

import (
	"errors"
	"io"
	"net"
	"time"
)

var (
	ErrSetDeadlineFailed = errors.New("set deadline failed")
	ErrReadHeaderFailed  = errors.New("read header failed")
	ErrReadPayloadFailed = errors.New("read payload failed")
)

type Reader interface {
	ReadBuffer() (*Buffer, error)
	SetReadTimeout(time.Duration) error
}

// Reader implements with net.Conn
type connReader struct {
	conn net.Conn
}

func NewConnReader(conn net.Conn) Reader {
	return &connReader{conn}
}

func (reader *connReader) SetReadTimeout(timeout time.Duration) error {
	if timeout == 0 {
		return reader.conn.SetReadDeadline(time.Time{})
	}
	return reader.conn.SetReadDeadline(time.Now().Add(timeout))
}

func (reader *connReader) ReadBuffer() (retBuf *Buffer, retErr error) {
	pb := GetBuffer()

	_, err := io.ReadFull(reader.conn, pb.Head[:])
	if err != nil {
		PutBuffer(pb)
		return nil, ErrReadHeaderFailed
	}

	sz := pb.PayloadSize()
	if sz > 0 {
		pb.Payload = make([]byte, sz)
		_, err := io.ReadFull(reader.conn, pb.Payload)
		if err != nil {
			PutBuffer(pb)
			return nil, ErrReadPayloadFailed
		}
	}

	return pb, nil
}
