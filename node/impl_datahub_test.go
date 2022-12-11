package node

import (
	"testing"

	"github.com/net-agent/flex/v2/stream"
	"github.com/stretchr/testify/assert"
)

func TestGetStreamBySID(t *testing.T) {
	hub := &DataHub{}
	var err error

	// 测试分支：stream not found
	_, err = hub.GetStreamBySID(100, false)
	assert.Equal(t, err, errStreamNotFound, "cover test: errStreamNotFound")

	// 测试分支：convert failed
	hub.streams.Store(uint64(100), 1234)
	loadAndDelete := true
	_, err = hub.GetStreamBySID(100, loadAndDelete)
	assert.Equal(t, err, errConvertStreamFailed, "cover test: errConvertStreamFailed")

	_, loaded := hub.streams.Load(uint64(100))
	assert.False(t, loaded, "test loadAndDelete flag")

	// 测试分支：正常通过
	s := stream.New(false)
	hub.streams.Store(uint64(200), s)
	loadAndDelete = false
	retStream, err := hub.GetStreamBySID(uint64(200), loadAndDelete)
	assert.Nil(t, err, "test loadAndDelete flag")
	assert.Equal(t, retStream, s, "retStream should equal to s")

	_, loaded = hub.streams.Load(uint64(200))
	assert.True(t, loaded, "test loadAndDelete flag")
}
