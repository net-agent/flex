package packet

import (
	"crypto/rand"
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReader_ReadBuffer(t *testing.T) {
	tests := []struct {
		name    string
		payload []byte
	}{
		{"empty payload", nil},
		{"small payload", []byte("hello world")},
		{"max payload", make([]byte, MaxPayloadSize)},
	}

	// 填充 max payload 随机数据
	rand.Read(tests[2].payload)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c1, c2 := Pipe()

			sent := NewBufferWithCmd(CmdPushStreamData)
			if tt.payload != nil {
				require.NoError(t, sent.SetPayload(tt.payload))
			}

			go func() {
				require.NoError(t, c1.WriteBuffer(sent))
			}()

			recv, err := c2.ReadBuffer()
			require.NoError(t, err)
			assert.Equal(t, sent.Head, recv.Head)
			assert.Equal(t, sent.Payload, recv.Payload)
		})
	}
}

func TestReader_SetReadTimeout(t *testing.T) {
	t.Run("clear timeout", func(t *testing.T) {
		c1, _ := net.Pipe()
		r := NewConnReader(c1)
		assert.NoError(t, r.SetReadTimeout(0))
	})

	t.Run("set timeout", func(t *testing.T) {
		c1, _ := net.Pipe()
		r := NewConnReader(c1)
		assert.NoError(t, r.SetReadTimeout(time.Second))
	})

	t.Run("error after close", func(t *testing.T) {
		c1, c2 := net.Pipe()
		r := NewConnReader(c1)

		go func() {
			NewConnWriter(c2).WriteBuffer(NewBuffer())
		}()

		c1.Close()
		assert.Error(t, r.SetReadTimeout(0))
	})
}

func TestReader_ErrReadHeaderFailed(t *testing.T) {
	c1, c2 := net.Pipe()
	r := NewConnReader(c1)

	go func() {
		// 写一个完整包，再写不完整的 header
		NewConnWriter(c2).WriteBuffer(NewBuffer())
		c2.Write([]byte{0x01})
	}()

	r.SetReadTimeout(time.Millisecond * 200)

	_, err := r.ReadBuffer()
	require.NoError(t, err, "first read should succeed")

	_, err = r.ReadBuffer()
	assert.ErrorIs(t, err, ErrReadHeaderFailed)
}

func TestReader_ErrReadPayloadFailed(t *testing.T) {
	c1, c2 := net.Pipe()
	r := NewConnReader(c1)

	go func() {
		// 写一个完整包，再写一个 header 声称有 10 字节 payload 但不发送
		NewConnWriter(c2).WriteBuffer(NewBuffer())

		var h Header
		binary.BigEndian.PutUint16(h[9:11], 10)
		c2.Write(h[:])
	}()

	r.SetReadTimeout(time.Millisecond * 200)

	_, err := r.ReadBuffer()
	require.NoError(t, err, "first read should succeed")

	_, err = r.ReadBuffer()
	assert.ErrorIs(t, err, ErrReadPayloadFailed)
}

func TestReader_ReadTimeout(t *testing.T) {
	// 设置短超时，不写入任何数据，ReadBuffer 应超时返回错误
	c1, _ := net.Pipe()
	r := NewConnReader(c1)
	require.NoError(t, r.SetReadTimeout(time.Millisecond*50))

	_, err := r.ReadBuffer()
	assert.ErrorIs(t, err, ErrReadHeaderFailed)
}

func TestReader_ReadAfterClose(t *testing.T) {
	c1, _ := net.Pipe()
	r := NewConnReader(c1)
	c1.Close()

	_, err := r.ReadBuffer()
	assert.ErrorIs(t, err, ErrReadHeaderFailed)
}

func TestReader_MultipleSequentialReads(t *testing.T) {
	c1, c2 := Pipe()
	const n = 10

	go func() {
		for i := range n {
			buf := NewBufferWithCmd(CmdPushStreamData)
			buf.SetSrcPort(uint16(i))
			require.NoError(t, buf.SetPayload([]byte("data")))
			require.NoError(t, c1.WriteBuffer(buf))
		}
	}()

	for i := range n {
		recv, err := c2.ReadBuffer()
		require.NoError(t, err)
		assert.Equal(t, uint16(i), recv.SrcPort(), "packet %d", i)
		assert.Equal(t, []byte("data"), recv.Payload, "packet %d", i)
	}
}
