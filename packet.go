package flex

import (
	"encoding/binary"
	"errors"
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
	srcPort  uint16
	distPort uint16
	payload  []byte
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
	binary.BigEndian.PutUint16(buf[1:3], p.srcPort)
	binary.BigEndian.PutUint16(buf[3:5], p.distPort)
	binary.BigEndian.PutUint16(buf[5:7], uint16(payloadSize))
	if payloadSize > 0 {
		copy(buf[packetHeaderSize:], p.payload)
	}
	return (packetHeaderSize + payloadSize), nil
}

// cmd(byte) + srcPort(uint16) + distPort(uint16) + bodyLen(uint16)
const packetHeaderSize = 1 + 2 + 2 + 2

type packetHeader [packetHeaderSize]byte

func (h *packetHeader) Cmd() byte           { return h[0] & 0xFE }
func (h *packetHeader) IsACK() bool         { return (h[0] & 0x01) == 0x01 }
func (h *packetHeader) SrcPort() uint16     { return binary.BigEndian.Uint16(h[1:3]) }
func (h *packetHeader) DistPort() uint16    { return binary.BigEndian.Uint16(h[3:5]) }
func (h *packetHeader) StreamID() uint32    { return binary.BigEndian.Uint32(h[1:5]) }
func (h *packetHeader) PayloadSize() uint16 { return binary.BigEndian.Uint16(h[5:7]) }
