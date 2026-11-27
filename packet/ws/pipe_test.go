package ws

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wsPiper 自身实现 net.Addr/net.Listener 的辅助方法，也用于 Pipe 的
// NetDial 回环地址。
func TestPiperAddrNetworkString(t *testing.T) {
	wp := &wsPiper{}
	wp.init()

	addr := wp.Addr()
	require.NotNil(t, addr)
	assert.Same(t, wp, addr)
	assert.Empty(t, wp.Network())
	assert.Empty(t, wp.String())
}

// Close 之后 Accept 立即返回零值连接，不再阻塞。
func TestPiperCloseUnblocksAccept(t *testing.T) {
	wp := &wsPiper{}
	wp.init()

	require.NoError(t, wp.Close())
	conn, err := wp.Accept()
	require.NoError(t, err)
	assert.Nil(t, conn, "Accept on a closed piper returns the zero net.Conn")
}
