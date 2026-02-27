package stream

import (
	"io"
	"log"

	"github.com/net-agent/flex/v3/packet"
)

func Pipe() (*Stream, *Stream) {
	pc1, pc2 := packet.Pipe()

	copybuf := func(s *Stream, pc packet.Reader) {
		cmdCh := make(chan *packet.Buffer, 1024)
		ackCh := make(chan *packet.Buffer, 1024)

		go routeCmd(s, cmdCh)
		go routeAck(s, ackCh)
		demuxPackets(cmdCh, ackCh, pc)
	}

	s1 := NewDialStream(pc1, "test1", 1, 1, "test2", 2, 2, 0)
	go copybuf(s1, pc1)

	s2 := NewAcceptStream(pc2, "test2", 2, 2, "test1", 1, 1, 0)
	go copybuf(s2, pc2)

	return s1, s2
}

func routeCmd(h Handler, ch chan *packet.Buffer) {
	for pbuf := range ch {
		switch pbuf.Cmd() {
		case packet.CmdPushStreamData:
			h.HandleCmdPushStreamData(pbuf)
		case packet.CmdCloseStream:
			h.HandleCmdCloseStream(pbuf)
		default:
			log.Println("unexpected pbuf cmd:", pbuf.HeaderString())
		}
	}
}
func routeAck(h Handler, ch chan *packet.Buffer) {
	for pbuf := range ch {
		switch pbuf.Cmd() {
		case packet.AckPushStreamData:
			h.HandleAckPushStreamData(pbuf)
		case packet.AckCloseStream:
			h.HandleAckCloseStream(pbuf)
		default:
			log.Println("unexpected pbuf ack", pbuf.HeaderString())
		}
	}
}
func demuxPackets(cmdCh, ackCh chan *packet.Buffer, pc packet.Reader) {
	if cmdCh == nil || ackCh == nil || pc == nil {
		return
	}
	defer close(cmdCh)
	defer close(ackCh)

	for {
		pbuf, err := pc.ReadBuffer()
		if err != nil {
			if err != io.EOF {
				log.Println("demuxPackets: read error:", err)
			}
			return
		}
		if pbuf.IsACK() {
			ackCh <- pbuf
		} else {
			cmdCh <- pbuf
		}
	}
}
