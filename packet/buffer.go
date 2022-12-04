package packet

import (
	"encoding/binary"
	"fmt"

	"github.com/net-agent/flex/v2/vars"
)

const (
	CmdACKFlag    = byte(1)
	CmdOpenStream = byte(iota << 1)
	CmdCloseStream
	CmdPushStreamData
	CmdAlive
	CmdPushMessage
	CmdPingDomain
)

// Header
// +-------+------+--------+----------+--------+---------+-------------+
// | Field | Cmd  | DistIP | DistPort | SrcIP  | SrcPort | PayloadSize |
// +-------+------+--------+----------+--------+---------+-------------+
// | Type  | byte | uint16 | uint16   | uint16 | uint16  | uint16      |
// | Pos   | 0    | 1      | 3        | 5      | 7       | 9           |
// | Size  | 1    | 2      | 2        | 2      | 2       | 2           |
// +-------+------+--------+----------+--------+---------+-------------+
const HeaderSz = 1 + 2 + 2 + 2 + 2 + 2

type Header [HeaderSz]byte
type Buffer struct {
	Head    *Header
	Payload []byte
}

func NewBuffer(head *Header) *Buffer {
	if head == nil {
		head = &Header{}
	}
	return &Buffer{Head: head}
}

// SetCmd 设置命令字段
func (buf *Buffer) SetCmd(cmd byte) {
	buf.Head[0] = cmd
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

// SetPayload 设置payload字段，字段最大长度不能超过vars.MaxPayloadSize
func (buf *Buffer) SetPayload(payload []byte) {
	if len(payload) > vars.MaxPayloadSize {
		panic("payload overflow")
	}
	binary.BigEndian.PutUint16(buf.Head[9:11], uint16(len(payload)))
	buf.Payload = payload
}

// PayloadSize 获取payload的长度
func (buf *Buffer) PayloadSize() uint16 {
	if buf.Cmd() == CmdPushStreamData|CmdACKFlag {
		return 0
	}
	return binary.BigEndian.Uint16(buf.Head[9:11])
}

// SetACKInfo 设置ack附带信息。ack附带信息为uint16的值
func (buf *Buffer) SetACKInfo(ack uint16) {
	binary.BigEndian.PutUint16(buf.Head[9:11], ack)
	buf.Payload = nil
}

// ACKInfo 获取ack附带信息
func (buf *Buffer) ACKInfo() uint16 {
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

// SetOpenACK 针对Open命令设置应答包，msg是返回客户端的信息
func (buf *Buffer) SetOpenACK(msg string) *Buffer {
	buf.Head[0] |= CmdACKFlag
	buf.SwapSrcDist()

	if msg == "" {
		buf.SetPayload(nil)
	} else {
		buf.SetPayload([]byte(msg))
	}
	return buf
}
