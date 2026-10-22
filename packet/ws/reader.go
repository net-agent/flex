package ws

import (
	"errors"
	"fmt"
	"time"

	"github.com/gorilla/websocket"
	"github.com/net-agent/flex/v3/packet"
)

var (
	ErrBadDataType = errors.New("err: bad data type")
	// ErrShortFrame 表示 WebSocket 消息短于 flex 协议头长度。
	// 这类输入必须拒绝而不是切片，否则会触发 slice bounds panic 打崩进程。
	ErrShortFrame = errors.New("err: short frame")
	// ErrBadFrameSize 表示帧内实际 payload 长度与头部 Size 字段不一致。
	// 若不校验，把该包转发到 TCP 对端时对端会按头部长度读取，
	// 造成字节流错位（协议混淆），因此必须在入口拒绝。
	ErrBadFrameSize = errors.New("err: frame size mismatch")
)

// MaxFrameSize 是单个 WebSocket 消息允许的最大长度。
// flex 协议单包 payload 上限为 MaxPayloadSize，加上固定包头。
// 该上限通过 websocket.SetReadLimit 生效，超限由 gorilla 直接关闭连接，
// 避免对端用超大帧把内存吃满。
const MaxFrameSize int64 = int64(packet.MaxPayloadSize) + int64(packet.HeaderSz)

type wsReader struct {
	wsconn *websocket.Conn
}

// NewReader creates a packet.Reader backed by a websocket.Conn.
func NewReader(wsconn *websocket.Conn) packet.Reader {
	wsconn.SetReadLimit(MaxFrameSize)
	return &wsReader{wsconn}
}

func (r *wsReader) SetReadTimeout(timeout time.Duration) error {
	if timeout == 0 {
		return r.wsconn.SetReadDeadline(time.Time{})
	}
	return r.wsconn.SetReadDeadline(time.Now().Add(timeout))
}

func (r *wsReader) ReadBuffer() (*packet.Buffer, error) {
	buf := packet.GetBuffer()

	mtype, data, err := r.wsconn.ReadMessage()
	if err != nil {
		packet.PutBuffer(buf)
		return nil, err
	}
	if mtype != websocket.BinaryMessage {
		packet.PutBuffer(buf)
		return nil, ErrBadDataType
	}
	if len(data) < packet.HeaderSz {
		packet.PutBuffer(buf)
		return nil, fmt.Errorf("%w: got=%d want>=%d", ErrShortFrame, len(data), packet.HeaderSz)
	}

	copy(buf.Head[:], data[:packet.HeaderSz])

	// 头部 [9:11] 在 AckPushStreamData 中是 ACK 计数而非 payload 长度，
	// 该命令不允许携带 payload；其余命令的 payload 长度必须与头部一致。
	payloadLen := len(data) - packet.HeaderSz
	if payloadLen != int(buf.PayloadSize()) {
		packet.PutBuffer(buf)
		return nil, fmt.Errorf("%w: frame=%d header=%d", ErrBadFrameSize, payloadLen, buf.PayloadSize())
	}
	buf.Payload = append(buf.Payload[:0], data[packet.HeaderSz:]...)

	return buf, nil
}
