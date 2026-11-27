package packet

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// PutBuffer(nil) must be a safe no-op: callers on error paths often have a nil
// buffer and should not need a separate guard.
func TestBuffer_PutBufferNilIsNoop(t *testing.T) {
	require.NotPanics(t, func() { PutBuffer(nil) })
}

// 池复用契约：PutBuffer 清零 Head；GetBuffer 负责把 Payload 长度归零，
// 因此复用底层数组时旧内容不得通过新 Buffer 的 len 暴露出来。
func TestBuffer_PoolReuseContract(t *testing.T) {
	for i := 0; i < 4; i++ {
		buf := GetBuffer()
		buf.SetHeader(CmdPushStreamData, 1, 2, 3, 4)
		require.NoError(t, buf.SetPayload(bytes.Repeat([]byte("stale"), 8)))
		PutBuffer(buf)
		assert.Equal(t, Header{}, buf.Head, "PutBuffer must clear the header")

		reused := GetBuffer()
		assert.Equal(t, Header{}, reused.Head, "GetBuffer must return a zeroed header")
		assert.Zero(t, len(reused.Payload), "GetBuffer must reset the payload length")

		reused.Payload = append(reused.Payload, 'o', 'k')
		assert.Equal(t, []byte("ok"), reused.Payload,
			"payload written after reuse must not expose stale bytes")

		PutBuffer(reused)
	}
}
