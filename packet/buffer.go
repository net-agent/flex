package packet

import (
	"encoding/binary"
)

type Buffer struct {
	head      Header
	payload   []byte
	writeDone chan struct{}
}

func NewPacketBufs() *Buffer {
	return &Buffer{}
}

func (pb *Buffer) SetBase(cmd byte, srcIP, srcPort, distIP, distPort uint16) {
	pb.head[0] = cmd
	binary.BigEndian.PutUint16(pb.head[1:3], srcIP)
	binary.BigEndian.PutUint16(pb.head[3:5], distIP)
	binary.BigEndian.PutUint16(pb.head[5:7], srcPort)
	binary.BigEndian.PutUint16(pb.head[7:9], distPort)
}

func (pb *Buffer) SetDistIP(ip uint16) {
	binary.BigEndian.PutUint16(pb.head[3:5], ip)
}
func (pb *Buffer) SetPayload(buf []byte) {
	binary.BigEndian.PutUint16(pb.head[9:11], uint16(len(buf)))
	pb.payload = buf
}
func (pb *Buffer) SetACKInfo(ack uint16) {
	binary.BigEndian.PutUint16(pb.head[9:11], ack)
}
func (pb *Buffer) SetCtxid(ctxid uint64) {
	pb.head[0] = 0
	pb.head[1] = 0
	pb.head[2] = 0
	pb.head[3] = 1
	binary.BigEndian.PutUint64(pb.head[4:8], ctxid)
}
