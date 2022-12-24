package stream

import (
	"testing"
	"time"

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

func TestHandleCmdDataErr_timeout(t *testing.T) {
	s := New(nil)
	pbuf := packet.NewBufferWithCmd(packet.CmdPushStreamData)
	pbuf.SetPayload([]byte("hello"))

	// cover test: append data timeout
	s.bytesChan = make(chan []byte)
	s.appendDataTimeout = time.Millisecond * 200
	s.HandleCmdPushStreamData(pbuf)
}

func TestHandleCmdDataAck(t *testing.T) {
	s1 := New(nil)

	// cover test: normal branch
	pbuf := packet.NewBufferWithCmd(packet.CmdPushStreamData | packet.CmdACKFlag)
	s1.HandleCmdPushStreamDataAck(pbuf)

	// cover test: default branch
	s1.bucketEv = make(chan struct{})
	s1.HandleCmdPushStreamDataAck(pbuf)
}
