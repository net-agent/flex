package stream

import (
	"testing"

	"github.com/net-agent/flex/v3/packet"
	"github.com/stretchr/testify/assert"
)

func TestSendCmdDataErr(t *testing.T) {
	sender := &Sender{}
	err := sender.SendCmdData(nil)
	assert.Nil(t, err)

	err = sender.SendCmdData(make([]byte, packet.MaxPayloadSize+1))
	assert.Equal(t, ErrSendDataOversize, err)
}
