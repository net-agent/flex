package stream

import (
	"github.com/net-agent/flex/v2/packet"
)

func Pipe() (*Stream, *Stream) {
	pc1, pc2 := packet.Pipe()

	copybuf := func(s *Stream, src packet.Reader) {
		for {
			pbuf, err := src.ReadBuffer()
			if err != nil {
				return
			}

			switch pbuf.Cmd() {
			case packet.CmdPushStreamData:
				s.HandleCmdPushStreamData(pbuf)

			case packet.CmdPushStreamData | packet.CmdACKFlag:
				s.HandleCmdPushStreamDataAck(pbuf)

			case packet.CmdCloseStream:
				s.HandleCmdCloseStream(pbuf)

			case packet.CmdCloseStream | packet.CmdACKFlag:
				s.HandleCmdCloseStreamAck(pbuf)
			}
		}
	}

	s1 := New(pc1, false)
	go copybuf(s1, pc1)

	s2 := New(pc2, false)
	go copybuf(s2, pc2)

	return s1, s2
}
