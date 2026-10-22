package packet

import (
	"errors"
	"fmt"
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
		// 同时包装哨兵值与底层原因：调用方既能 errors.Is(err, ErrReadHeaderFailed)，
		// 也能区分 io.EOF / net.ErrClosed / 超时，便于日志与重连决策。
		return nil, fmt.Errorf("%w: %w", ErrReadHeaderFailed, err)
	}

	sz := pb.PayloadSize()
	if sz > 0 {
		if cap(pb.Payload) < int(sz) {
			pb.Payload = make([]byte, sz)
		} else {
			pb.Payload = pb.Payload[:sz]
		}
		_, err := io.ReadFull(reader.conn, pb.Payload)
		if err != nil {
			PutBuffer(pb)
			return nil, fmt.Errorf("%w: %w", ErrReadPayloadFailed, err)
		}
	}

	return pb, nil
}
