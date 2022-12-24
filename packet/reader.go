package packet

import (
	"errors"
	"io"
	"log"
	"net"
	"time"

	"github.com/gorilla/websocket"
)

var (
	ErrBadDataType       = errors.New("err: bad data type")
	ErrSetDeadlineFailed = errors.New("set deadline failed")
	ErrReadHeaderFailed  = errors.New("read header failed")
	ErrReadPayloadFailed = errors.New("read payload failed")
)

type Reader interface {
	ReadBuffer() (*Buffer, error)
	SetReadTimeout(time.Duration)
}

// Reader implements with net.Conn
type connReader struct {
	conn        net.Conn
	readTimeout time.Duration
}

func NewConnReader(conn net.Conn) Reader {
	return &connReader{conn, DefaultReadTimeout}
}

func (reader *connReader) SetReadTimeout(timeout time.Duration) { reader.readTimeout = timeout }

func (reader *connReader) ReadBuffer() (retBuf *Buffer, retErr error) {
	if LOG_READ_BUFFER_HEADER {
		defer func() {
			if retErr == nil {
				log.Printf("[R]%v\n", retBuf.HeaderString())
			} else {
				log.Printf("[R][err=%v]\n", retErr)
			}
		}()
	}
	// 如果30秒读不到任何数据，则会报错关闭
	// 所以心跳包的时间间隔不应该超过这个数值
	err := reader.conn.SetReadDeadline(time.Now().Add(reader.readTimeout))
	if err != nil {
		return nil, ErrSetDeadlineFailed
	}
	pb := NewBuffer(nil)

	_, err = io.ReadFull(reader.conn, pb.Head[:])
	if err != nil {
		return nil, ErrReadHeaderFailed
	}

	sz := pb.PayloadSize()
	if sz > 0 {
		pb.Payload = make([]byte, sz)
		_, err := io.ReadFull(reader.conn, pb.Payload)
		if err != nil {
			return nil, ErrReadPayloadFailed
		}
	}

	reader.conn.SetReadDeadline(time.Time{})
	return pb, nil
}

//
// Reader implements with websocket.Conn
//

type wsReader struct {
	wsconn      *websocket.Conn
	readTimeout time.Duration
}

func NewWsReader(wsconn *websocket.Conn) Reader {
	return &wsReader{wsconn, DefaultReadTimeout}
}

func (reader *wsReader) SetReadTimeout(timeout time.Duration) { reader.readTimeout = timeout }

func (reader *wsReader) ReadBuffer() (retBuf *Buffer, retErr error) {
	if LOG_READ_BUFFER_HEADER {
		defer func() {
			if retErr == nil {
				log.Printf("[R]%v\n", retBuf.HeaderString())
			} else {
				log.Printf("[R][err=%v]\n", retErr)
			}
		}()
	}
	buf := NewBuffer(nil)

	reader.wsconn.SetReadDeadline(time.Now().Add(reader.readTimeout))
	mtype, data, err := reader.wsconn.ReadMessage()
	if err != nil {
		return nil, err
	}
	if mtype != websocket.BinaryMessage {
		return nil, ErrBadDataType
	}

	copy(buf.Head[:], data[:HeaderSz])
	buf.Payload = data[HeaderSz:]

	return buf, nil
}
