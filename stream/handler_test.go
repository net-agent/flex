package stream

import (
	"testing"

	"github.com/net-agent/flex/v2/packet"
	"github.com/stretchr/testify/assert"
)

func TestHandleCmdData(t *testing.T) {
	s1, s2 := Pipe()

	// cover test: log "payload is empty"
	emptyPayloadBuf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
	s1.HandleCmdPushStreamData(emptyPayloadBuf)

	err := s2.Close()
	assert.Nil(t, err)

	// cover test: log "stream closed"
	s1.HandleCmdPushStreamData(packet.NewBufferWithCmd(packet.CmdPushStreamData))
}
