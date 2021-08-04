package stream

import (
	"github.com/net-agent/flex/v2/packet"
)

func Pipe() (*Conn, *Conn) {
	pc1, pc2 := packet.Pipe()

	copybuf := func(s *Conn, src packet.Conn) {
		for {
			pbuf, err := src.ReadBuffer()
			if err != nil {
				return
			}

			switch pbuf.Cmd() {
			case packet.CmdPushStreamData:
				s.AppendData(pbuf.Payload)
			case packet.CmdPushStreamData | packet.CmdACKFlag:
				s.IncreaseBucket(pbuf.ACKInfo())
			case packet.CmdCloseStream:
				s.AppendEOF()
				s.CloseWrite(true)
			case packet.CmdCloseStream | packet.CmdACKFlag:
				s.AppendEOF()
			}
		}
	}

	s1 := New(false)
	s1.InitWriter(pc1)
	go copybuf(s1, pc1)

	s2 := New(false)
	s2.InitWriter(pc2)
	go copybuf(s2, pc2)

	return s1, s2
}
