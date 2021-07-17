package packet

import (
	"encoding/binary"
	"fmt"
	"runtime"
)

const (
	CmdACKFlag = byte(1 << iota)
	CmdOpenStream
	CmdCloseStream
	CmdPushStreamData
	CmdAlive
	CmdPushMessage
)

// cmd(byte) +
// srcHost(uint16) + distHost(uint16) +
// srcPort(uint16) + distPort(uint16) +
// bodyLen(uint16)
const HeaderSz = 1 + 4 + 4 + 2

type Header [HeaderSz]byte

func (h *Header) Cmd() byte            { return h[0] & 0xFE }
func (h *Header) IsACK() bool          { return (h[0] & 0x01) == 0x01 }
func (h *Header) SrcIP() uint16        { return binary.BigEndian.Uint16(h[1:3]) }
func (h *Header) DistIP() uint16       { return binary.BigEndian.Uint16(h[3:5]) }
func (h *Header) SrcPort() uint16      { return binary.BigEndian.Uint16(h[5:7]) }
func (h *Header) DistPort() uint16     { return binary.BigEndian.Uint16(h[7:9]) }
func (h *Header) Src() string          { return fmt.Sprintf("%v:%v", h.SrcIP(), h.SrcPort()) }
func (h *Header) Dist() string         { return fmt.Sprintf("%v:%v", h.DistIP(), h.DistPort()) }
func (h *Header) StreamDataID() uint64 { return binary.BigEndian.Uint64(h[1:9]) }

// CPUID 数值不超过runtime.NumCPU()
func (h *Header) CPUID() int {
	return int(h[1]^h[2]^h[3]^h[4]^h[5]^h[6]^h[7]^h[8]) % runtime.NumCPU()
}
func (h *Header) CmdStr() string {
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

func (h *Header) PayloadSize() uint16 {
	if (h[0] & 0x01) == 1 {
		return 0
	}
	return binary.BigEndian.Uint16(h[9:11])
}

func (h *Header) ACKInfo() uint16 {
	if (h[0] & 0x01) == 0 {
		return 0
	}
	return binary.BigEndian.Uint16(h[9:11])
}
