package flex

import (
	"encoding/binary"
	"fmt"
	"io"
)

const (
	CmdACKFlag = byte(1 << iota)
	CmdOpenStream
	CmdCloseStream
	CmdPushStreamData
	CmdAlive
	CmdPushMessage
)

type PacketBufs struct {
	head      packetHeader
	payload   []byte
	writeDone chan struct{}
}

func NewPacketBufs() *PacketBufs {
	return &PacketBufs{}
}

// cmd(byte) +
// srcHost(uint16) + distHost(uint16) +
// srcPort(uint16) + distPort(uint16) +
// bodyLen(uint16)
const packetHeaderSize = 1 + 4 + 4 + 2

type packetHeader [packetHeaderSize]byte

func (h *packetHeader) Cmd() byte            { return h[0] & 0xFE }
func (h *packetHeader) IsACK() bool          { return (h[0] & 0x01) == 0x01 }
func (h *packetHeader) SrcIP() HostIP        { return binary.BigEndian.Uint16(h[1:3]) }
func (h *packetHeader) DistIP() HostIP       { return binary.BigEndian.Uint16(h[3:5]) }
func (h *packetHeader) SrcPort() uint16      { return binary.BigEndian.Uint16(h[5:7]) }
func (h *packetHeader) DistPort() uint16     { return binary.BigEndian.Uint16(h[7:9]) }
func (h *packetHeader) Src() string          { return fmt.Sprintf("%v:%v", h.SrcIP(), h.SrcPort()) }
func (h *packetHeader) Dist() string         { return fmt.Sprintf("%v:%v", h.DistIP(), h.DistPort()) }
func (h *packetHeader) StreamDataID() uint64 { return binary.BigEndian.Uint64(h[1:9]) }
func (h *packetHeader) CmdStr() string {
	b := h[0]
	strs := []string{"[ack]", "[open]", "[close]", "[push]", "[alive]", "[domain]"}
	ret := ""
	for i, str := range strs {
		if ((1 << i) & b) > 0 {
			ret = ret + str
		} else if i == 0 {
			ret = ret + "[cmd]"
		}
	}
	if ret == "" {
		return "[-]"
	}
	return ret
}

func (h *packetHeader) PayloadSize() uint16 {
	if (h[0] & 0x01) == 1 {
		return 0
	}
	return binary.BigEndian.Uint16(h[9:11])
}

func (h *packetHeader) ACKInfo() uint16 {
	if (h[0] & 0x01) == 0 {
		return 0
	}
	return binary.BigEndian.Uint16(h[9:11])
}

func (pb *PacketBufs) ReadFrom(r io.Reader) (int64, error) {
	var rn int64 = 0
	n, err := io.ReadFull(r, pb.head[:])
	rn += int64(n)
	if err != nil {
		return rn, err
	}

	sz := pb.head.PayloadSize()
	if sz > 0 {
		pb.payload = make([]byte, sz)
		n, err := io.ReadFull(r, pb.payload)
		rn += int64(n)
		if err != nil {
			return rn, err
		}
	}

	return rn, nil
}

func (pb *PacketBufs) SetBase(cmd byte, srcIP, srcPort, distIP, distPort uint16) {
	pb.head[0] = cmd
	binary.BigEndian.PutUint16(pb.head[1:3], srcIP)
	binary.BigEndian.PutUint16(pb.head[3:5], distIP)
	binary.BigEndian.PutUint16(pb.head[5:7], srcPort)
	binary.BigEndian.PutUint16(pb.head[7:9], distPort)
}

func (pb *PacketBufs) SetDistIP(ip HostIP) {
	binary.BigEndian.PutUint16(pb.head[3:5], ip)
}
func (pb *PacketBufs) SetPayload(buf []byte) {
	binary.BigEndian.PutUint16(pb.head[9:11], uint16(len(buf)))
	pb.payload = buf
}
func (pb *PacketBufs) SetACKInfo(ack uint16) {
	binary.BigEndian.PutUint16(pb.head[9:11], ack)
}
