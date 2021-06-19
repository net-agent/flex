package flex

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	CmdACKFlag = byte(1 << iota)
	CmdOpenStream
	CmdCloseStream
	CmdPushStreamData
	CmdAlive
)

type Packet struct {
	cmd      byte
	srcHost  HostIP
	distHost HostIP
	srcPort  uint16
	distPort uint16
	payload  []byte
	ackInfo  uint16
	done     chan struct{}
}

// Read 把packet的数据写入缓冲区中，缓冲区过大或过小都会失败
func (p *Packet) Read(buf []byte) (int, error) {
	payloadSize := len(p.payload)
	if payloadSize > 0xFFFF {
		return 0, errors.New("payload is too large")
	}
	if len(buf) < packetHeaderSize+payloadSize {
		return 0, errors.New("buf is too small")
	}

	buf[0] = p.cmd
	binary.BigEndian.PutUint16(buf[1:3], p.srcHost)
	binary.BigEndian.PutUint16(buf[3:5], p.distHost)
	binary.BigEndian.PutUint16(buf[5:7], p.srcPort)
	binary.BigEndian.PutUint16(buf[7:9], p.distPort)

	if (p.cmd & CmdACKFlag) > 0 {
		binary.BigEndian.PutUint16(buf[9:11], p.ackInfo)
		return packetHeaderSize, nil
	} else {
		binary.BigEndian.PutUint16(buf[9:11], uint16(payloadSize))
		if payloadSize > 0 {
			copy(buf[packetHeaderSize:], p.payload)
		}
		return (packetHeaderSize + payloadSize), nil
	}
}

func (p *Packet) CmdStr() string {
	return cmdStr(p.cmd)
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
func (h *packetHeader) CmdStr() string       { return cmdStr(h[0]) }

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

func cmdStr(b byte) string {
	strs := []string{"[ack]", "[open]", "[close]", "[push]", "[alive]"}
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

type PacketBufs struct {
	head    packetHeader
	payload []byte
}

func NewPacketBufs() *PacketBufs {
	return &PacketBufs{}
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
