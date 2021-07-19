package packet

import (
	"encoding/binary"
)

type Buffer struct {
	Head    Header
	Payload []byte
}

func NewBuffer() *Buffer {
	return &Buffer{}
}

func (pb *Buffer) SetBase(cmd byte, srcIP, srcPort, distIP, distPort uint16) {
	pb.Head[0] = cmd
	binary.BigEndian.PutUint16(pb.Head[1:3], srcIP)
	binary.BigEndian.PutUint16(pb.Head[3:5], distIP)
	binary.BigEndian.PutUint16(pb.Head[5:7], srcPort)
	binary.BigEndian.PutUint16(pb.Head[7:9], distPort)
}

func (pb *Buffer) GetDistIP() uint16 {
	return binary.BigEndian.Uint16(pb.Head[3:5])
}
func (pb *Buffer) SetDistIP(ip uint16) {
	binary.BigEndian.PutUint16(pb.Head[3:5], ip)
}

func (pb *Buffer) SetPayload(buf []byte) {
	binary.BigEndian.PutUint16(pb.Head[9:11], uint16(len(buf)))
	pb.Payload = buf
}
func (pb *Buffer) SetACKInfo(ack uint16) {
	binary.BigEndian.PutUint16(pb.Head[9:11], ack)
}
func (pb *Buffer) SetCtxid(ctxid uint64) {
	pb.Head[0] = 0
	pb.Head[1] = 0
	pb.Head[2] = 0
	pb.Head[3] = 1
	binary.BigEndian.PutUint64(pb.Head[4:8], ctxid)
}
