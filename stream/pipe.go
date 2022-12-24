package stream

import (
	"log"

	"github.com/net-agent/flex/v2/packet"
)

func Pipe() (*Stream, *Stream) {
	pc1, pc2 := packet.Pipe()

	copybuf := func(s *Stream, src packet.Reader) {
		cmdCh := make(chan *packet.Buffer, 1024)
		ackCh := make(chan *packet.Buffer, 1024)

		go func() {
			for pbuf := range cmdCh {
				switch pbuf.Cmd() {
				case packet.CmdPushStreamData:
					s.HandleCmdPushStreamData(pbuf)
				case packet.CmdCloseStream:
					s.HandleCmdCloseStream(pbuf)
				default:
					log.Println("unexpected pbuf cmd:", pbuf.HeaderString())
				}
			}
		}()

		go func() {
			for pbuf := range ackCh {
				switch pbuf.Cmd() {
				case packet.CmdPushStreamData | packet.CmdACKFlag:
					s.HandleCmdPushStreamDataAck(pbuf)
				case packet.CmdCloseStream | packet.CmdACKFlag:
					s.HandleCmdCloseStreamAck(pbuf)
				default:
					log.Println("unexpected pbuf ack", pbuf.HeaderString())
				}
			}
		}()

		for {
			pbuf, err := src.ReadBuffer()
			if err != nil {
				return
			}
			if pbuf.IsACK() {
				ackCh <- pbuf
			} else {
				cmdCh <- pbuf
			}
		}
	}

	s1 := NewDialStream(pc1, "test1", 1, 1, "test2", 2, 2)
	go copybuf(s1, pc1)

	s2 := NewAcceptStream(pc2, "test2", 2, 2, "test1", 1, 1)
	go copybuf(s2, pc2)

	return s1, s2
}
