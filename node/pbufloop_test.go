package node

import (
	"testing"

	"github.com/net-agent/flex/v2/packet"
)

func TestPbufRouteLoop(t *testing.T) {
	ch := make(chan *packet.Buffer, 10)
	defer close(ch)

	n := New(nil)
	go n.pbufRouteLoop(ch)

	cmds := []byte{
		packet.CmdOpenStream,
		packet.CmdPushStreamData,
		packet.CmdCloseStream,
		packet.CmdPingDomain,
	}

	for _, cmd := range cmds {
		pbuf1 := packet.NewBuffer(nil)
		pbuf1.SetCmd(cmd)
		ch <- pbuf1

		pbuf2 := packet.NewBuffer(nil)
		pbuf2.SetCmd(cmd | packet.CmdACKFlag)
		ch <- pbuf2
	}
}
