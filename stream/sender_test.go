package stream

import (
	"testing"

	"github.com/net-agent/flex/v2/vars"
	"github.com/stretchr/testify/assert"
)

func TestSendCmdDataErr(t *testing.T) {
	sender := &Sender{}
	err := sender.SendCmdData(nil)
	assert.Nil(t, err)

	err = sender.SendCmdData(make([]byte, vars.MaxPayloadSize+1))
	assert.Equal(t, ErrSendDataOversize, err)
}
