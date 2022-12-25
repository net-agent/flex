package stream

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestAddrStruct(t *testing.T) {
	a := &Addr{}
	network := "testnetwork"
	ip := uint16(1234)
	port := uint16(5678)

	a.SetNetwork(network)
	a.SetIPPort(ip, port)

	assert.Equal(t, network, a.Network())
	assert.Equal(t, ip, a.IP)
	assert.Equal(t, port, a.Port)
	assert.Equal(t, "1234:5678", a.String())
}
