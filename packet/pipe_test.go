package packet

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPipe(t *testing.T) {
	pc1, pc2 := Pipe()
	require.NotNil(t, pc1)
	require.NotNil(t, pc2)

	assert.NotNil(t, pc1.GetRawConn())
	assert.NotNil(t, pc2.GetRawConn())

	// 验证 timeout 设置不 panic
	assert.NoError(t, pc1.SetReadTimeout(0))
	assert.NoError(t, pc2.SetReadTimeout(time.Second*12))
	pc1.SetWriteTimeout(0)
	pc2.SetWriteTimeout(time.Second * 12)

	// 验证 Close
	assert.NoError(t, pc1.Close())
	assert.NoError(t, pc2.Close())
}
