package packet

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
)

var ErrPayloadOverflow = errors.New("payload size exceeds MaxPayloadSize")

const (
	MaxIP      = uint16(0xffff)
	DNSIP      = uint16(0xffff)
	SwitcherIP = uint16(0xffff)
	LocalIP    = uint16(0)
)

const MaxPayloadSize = 0xFFFF

// VERSION 标识协议版本。当数据包结构出现不兼容改动时，此处需要更新
const VERSION = int(20260225)

// CmdAdmit 表示认证包：第一个包用于发送认证信息，DistIP/DistPort/SrcIP/SrcPort 字段不使用，Cmd字段固定为 CmdAdmit，Payload为加密后的认证信息
const CmdAdmit = byte(0)

const (
	CmdACKFlag    = byte(1)
	CmdOpenStream = byte(iota << 1)
	CmdCloseStream
	CmdPushStreamData
	CmdPushMessage
	CmdPingDomain
	CmdSubscribePresence
	CmdNotifyPresence
)

const (
	AckOpenStream        = CmdOpenStream | CmdACKFlag
	AckCloseStream       = CmdCloseStream | CmdACKFlag
	AckPushStreamData    = CmdPushStreamData | CmdACKFlag
	AckPushMessage       = CmdPushMessage | CmdACKFlag
	AckPingDomain        = CmdPingDomain | CmdACKFlag
	AckSubscribePresence = CmdSubscribePresence | CmdACKFlag
)

// HeaderSz 是包头长度，布局如下：
// +-------+------+--------+----------+--------+---------+---------------------+
// | Field | Cmd  | DistIP | DistPort | SrcIP  | SrcPort | PayloadSize/ACKSize |
// +-------+------+--------+----------+--------+---------+---------------------+
// | Type  | byte | uint16 | uint16   | uint16 | uint16  | uint16              |
// | Pos   | 0    | 1      | 3        | 5      | 7       | 9                   |
// | Size  | 1    | 2      | 2        | 2      | 2       | 2                   |
// +-------+------+--------+----------+--------+---------+---------------------+
//
// 注意：字节 [9:11] 是 union 字段：
//   - 非 ACK 包：表示 PayloadSize，通过 SetPayload/PayloadSize 访问
//   - AckPushStreamData：表示已确认的数据大小，通过 SetDataACKSize/DataACKSize 访问
const HeaderSz = 1 + 2 + 2 + 2 + 2 + 2

type Header [HeaderSz]byte
type Buffer struct {
	Head    Header
	Payload []byte
}

// Buffer 所有权约定：
//   - ReadBuffer 返回的 Buffer 归调用方所有，用毕应调用 PutBuffer 归还；
//     所有权可沿调用链单播转移，但每个 Buffer 全程只能有一个终点执行 Put。
//   - Writer.WriteBuffer 为同步写：函数返回后写方不再持有 buf，
//     调用方可在返回后立即安全回收（全部内置 Writer 实现均满足）。
//   - PutBuffer 之后不得再访问 buf 及其 Payload（底层数组可能被复用覆写）。
//     重复 Put 会让同一数组被多方共享，属于严重错误；漏 Put 仅损失复用收益。
var bufferPool = sync.Pool{
	New: func() any { return &Buffer{} },
}

// GetBuffer returns a Buffer from the pool. Caller must call PutBuffer when done.
// 返回的 Buffer 其 Payload 长度恒为 0，但底层数组可能复用自池（容量保留）。
func GetBuffer() *Buffer {
	buf := bufferPool.Get().(*Buffer)
	buf.Payload = buf.Payload[:0]
	return buf
}

// PutBuffer returns a Buffer to the pool：Head 清零；Payload 底层数组保留在池中，
// 供后续同尺寸读取直接复用（单包 payload 上限 MaxPayloadSize=64KB，
// 且 sync.Pool 条目受 GC 回收约束，内存不会无界增长）。
func PutBuffer(buf *Buffer) {
	buf.Head = Header{}
	bufferPool.Put(buf)
}

func NewBuffer() *Buffer {
	return &Buffer{}
}

func NewBufferWithCmd(cmd byte) *Buffer {
	pbuf := NewBuffer()
	pbuf.SetCmd(cmd)
	return pbuf
}

func (buf *Buffer) WriteTo(w io.Writer) (total int64, err error) {
	n, err := w.Write(buf.Head[:])
	total += int64(n)
	if err != nil {
		return total, ErrWriteHeaderFailed
	}

	if len(buf.Payload) == 0 {
		return total, nil
	}

	n, err = w.Write(buf.Payload)
	total += int64(n)
	if err != nil {
		return total, ErrWritePayloadFailed
	}

	return total, nil
}

func (buf *Buffer) HeaderString() string {
	return fmt.Sprintf("[%v][src=%v:%v][dist=%v:%v][size=%v]",
		buf.CmdName(),
		buf.SrcIP(), buf.SrcPort(),
		buf.DistIP(), buf.DistPort(),
		buf.PayloadSize(),
	)
}

// SetCmd 设置命令字段
func (buf *Buffer) SetCmd(cmd byte) {
	buf.Head[0] = cmd
}
func (buf *Buffer) CmdName() string {
	var name string
	t := buf.CmdType()
	switch t {
	case CmdOpenStream:
		name = "open"
	case CmdCloseStream:
		name = "close"
	case CmdPushStreamData:
		name = "data"
	case CmdPushMessage:
		name = "push"
	case CmdPingDomain:
		name = "ping"
	case CmdSubscribePresence:
		name = "subscribe"
	case CmdNotifyPresence:
		name = "notify"
	default:
		name = fmt.Sprintf("<%v>", t)
	}
	if buf.IsACK() {
		name = name + ".ack"
	}
	return name
}

// Cmd 获取命令字段
func (buf *Buffer) Cmd() byte {
	return buf.Head[0]
}

func (buf *Buffer) CmdType() byte {
	return buf.Head[0] & 0xFE
}

// IsACK 判断命令是否为ACK类型
func (buf *Buffer) IsACK() bool {
	return buf.Head[0]&CmdACKFlag > 0
}

// SID 获取SID（用于标识唯一stream）
func (buf *Buffer) SID() uint64 {
	return binary.BigEndian.Uint64(buf.Head[1:9])
}

// SIDStr 获取SID的字符串表示
func (buf *Buffer) SIDStr() string {
	return fmt.Sprintf("%v:%v-%v:%v", buf.SrcIP(), buf.SrcPort(), buf.DistIP(), buf.DistPort())
}

// SetDist 同时设置目标ip和port
func (buf *Buffer) SetDist(ip, port uint16) {
	buf.SetDistIP(ip)
	buf.SetDistPort(port)
}

// SetDistIP 单独设置目标ip
func (buf *Buffer) SetDistIP(ip uint16) {
	binary.BigEndian.PutUint16(buf.Head[1:3], ip)
}

// SetDistPort 单独设置目标port
func (buf *Buffer) SetDistPort(port uint16) {
	binary.BigEndian.PutUint16(buf.Head[3:5], port)
}

// DistIP 获取目标ip
func (buf *Buffer) DistIP() uint16 {
	return binary.BigEndian.Uint16(buf.Head[1:3])
}

// DistPort 获取目标port
func (buf *Buffer) DistPort() uint16 {
	return binary.BigEndian.Uint16(buf.Head[3:5])
}

// SetSrc 同时设置源ip和端口
func (buf *Buffer) SetSrc(ip, port uint16) {
	buf.SetSrcIP(ip)
	buf.SetSrcPort(port)
}

// SetSrcIP 单独设置源ip
func (buf *Buffer) SetSrcIP(ip uint16) {
	binary.BigEndian.PutUint16(buf.Head[5:7], ip)
}

// SetSrcPort 单独设置源port
func (buf *Buffer) SetSrcPort(port uint16) {
	binary.BigEndian.PutUint16(buf.Head[7:9], port)
}

// SrcIP 获取源ip
func (buf *Buffer) SrcIP() uint16 {
	return binary.BigEndian.Uint16(buf.Head[5:7])
}

// SrcPort 获取源端口
func (buf *Buffer) SrcPort() uint16 {
	return binary.BigEndian.Uint16(buf.Head[7:9])
}

// SetHeader 同时设置buf的所有字段
func (buf *Buffer) SetHeader(cmd byte, distIP, distPort, srcIP, srcPort uint16) {
	buf.SetCmd(cmd)
	buf.SetDist(distIP, distPort)
	buf.SetSrc(srcIP, srcPort)
}

// SetPayload 设置payload字段，字段最大长度不能超过MaxPayloadSize
func (buf *Buffer) SetPayload(payload []byte) error {
	if len(payload) > MaxPayloadSize {
		return ErrPayloadOverflow
	}
	binary.BigEndian.PutUint16(buf.Head[9:11], uint16(len(payload)))
	buf.Payload = payload
	return nil
}

// PayloadSize 获取payload的长度
func (buf *Buffer) PayloadSize() uint16 {
	if buf.Cmd() == CmdPushStreamData|CmdACKFlag {
		return 0
	}
	return binary.BigEndian.Uint16(buf.Head[9:11])
}

// SetDataACKSize 设置 AckPushStreamData 包中已确认的数据大小。
// 此方法复用 Head[9:11]（与 PayloadSize 共享），并清空 Payload。
func (buf *Buffer) SetDataACKSize(size uint16) {
	binary.BigEndian.PutUint16(buf.Head[9:11], size)
	buf.Payload = nil
}

// DataACKSize 获取 AckPushStreamData 包中已确认的数据大小。
// 仅当 Cmd 为 AckPushStreamData 时有效，其他命令返回 0。
func (buf *Buffer) DataACKSize() uint16 {
	if buf.Cmd() != CmdPushStreamData|CmdACKFlag {
		return 0
	}
	return binary.BigEndian.Uint16(buf.Head[9:11])
}

// SwapSrcDist 交换src和dist的地址，包含ip和port
func (buf *Buffer) SwapSrcDist() {
	var addr [4]byte
	copy(addr[:], buf.Head[1:5])
	copy(buf.Head[1:5], buf.Head[5:9])
	copy(buf.Head[5:9], addr[:])
}
