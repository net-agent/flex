package packet

import (
	"encoding/binary"
	"fmt"

	"github.com/net-agent/flex/v2/vars"
)

const (
	CmdACKFlag = byte(1 << iota)
	CmdOpenStream
	CmdCloseStream
	CmdPushStreamData
	CmdAlive
	CmdPushMessage
)

//
// Header
// +-------+------+--------+----------+--------+---------+-------------+
// | Field | Cmd  | DistIP | DistPort | SrcIP  | SrcPort | PayloadSize |
// +-------+------+--------+----------+--------+---------+-------------+
// | Type  | byte | uint16 | uint16   | uint16 | uint16  | uint16      |
// | Pos   | 0    | 1      | 3        | 5      | 7       | 9          |
// | Size  | 1    | 2      | 2        | 2      | 2       | 2           |
// +-------+------+--------+----------+--------+---------+-------------+
//
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

//
// Cmd
//
func (buf *Buffer) SetCmd(cmd byte) { buf.Head[0] = cmd }
func (buf *Buffer) Cmd() byte       { return buf.Head[0] }
func (buf *Buffer) IsACK() bool     { return buf.Head[0]&CmdACKFlag > 0 }

//
func (buf *Buffer) SID() uint64 {
	return binary.BigEndian.Uint64(buf.Head[1:9])
}
func (buf *Buffer) SIDStr() string {
	return fmt.Sprintf("%v:%v-%v:%v", buf.SrcIP(), buf.SrcPort(), buf.DistIP(), buf.DistPort())
}

//
// Dist
//
func (buf *Buffer) SetDist(ip, port uint16) {
	buf.SetDistIP(ip)
	buf.SetDistPort(port)
}
func (buf *Buffer) SetDistIP(ip uint16) {
	binary.BigEndian.PutUint16(buf.Head[1:3], ip)
}
func (buf *Buffer) SetDistPort(port uint16) {
	binary.BigEndian.PutUint16(buf.Head[3:5], port)
}
func (buf *Buffer) DistIP() uint16   { return binary.BigEndian.Uint16(buf.Head[1:3]) }
func (buf *Buffer) DistPort() uint16 { return binary.BigEndian.Uint16(buf.Head[3:5]) }

//
// Src
//
func (buf *Buffer) SetSrc(ip, port uint16) {
	buf.SetSrcIP(ip)
	buf.SetSrcPort(port)
}
func (buf *Buffer) SetSrcIP(ip uint16) {
	binary.BigEndian.PutUint16(buf.Head[5:7], ip)
}
func (buf *Buffer) SetSrcPort(port uint16) {
	binary.BigEndian.PutUint16(buf.Head[7:9], port)
}
func (buf *Buffer) SrcIP() uint16   { return binary.BigEndian.Uint16(buf.Head[5:7]) }
func (buf *Buffer) SrcPort() uint16 { return binary.BigEndian.Uint16(buf.Head[7:9]) }

//
// Header
//
func (buf *Buffer) SetHeader(cmd byte, distIP, distPort, srcIP, srcPort uint16) {
	buf.SetCmd(cmd)
	buf.SetDist(distIP, distPort)
	buf.SetSrc(srcIP, srcPort)
}

// Payload
func (buf *Buffer) SetPayload(payload []byte) {
	if len(payload) > vars.MaxPayloadSize {
		panic("payload overflow")
	}
	binary.BigEndian.PutUint16(buf.Head[9:11], uint16(len(payload)))
	buf.Payload = payload
}
func (buf *Buffer) PayloadSize() uint16 {
	if buf.Cmd() == CmdPushStreamData|CmdACKFlag {
		return 0
	}
	return binary.BigEndian.Uint16(buf.Head[9:11])
}

// ACKInfo
func (buf *Buffer) SetACKInfo(ack uint16) {
	binary.BigEndian.PutUint16(buf.Head[9:11], ack)
	buf.Payload = nil
}
func (buf *Buffer) ACKInfo() uint16 {
	if buf.Cmd() != CmdPushStreamData|CmdACKFlag {
		return 0
	}
	return binary.BigEndian.Uint16(buf.Head[9:11])
}
