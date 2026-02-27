package packet

import (
	"bytes"
	"fmt"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuffer_SetGetHeader(t *testing.T) {
	tests := []struct {
		name                 string
		cmd                  byte
		distIP, distPort     uint16
		srcIP, srcPort       uint16
		payload              []byte
	}{
		{"basic", 1, 2, 3, 4, 5, []byte("hello")},
		{"zero values", 0, 0, 0, 0, 0, nil},
		{"max values", 0xFF, MaxIP, 0xFFFF, MaxIP, 0xFFFF, []byte("x")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := NewBuffer()
			buf.SetHeader(tt.cmd, tt.distIP, tt.distPort, tt.srcIP, tt.srcPort)
			if tt.payload != nil {
				require.NoError(t, buf.SetPayload(tt.payload))
			}

			assert.Equal(t, tt.cmd, buf.Cmd())
			assert.Equal(t, tt.distIP, buf.DistIP())
			assert.Equal(t, tt.distPort, buf.DistPort())
			assert.Equal(t, tt.srcIP, buf.SrcIP())
			assert.Equal(t, tt.srcPort, buf.SrcPort())
			assert.Equal(t, tt.payload, buf.Payload)
		})
	}
}

func TestBuffer_IsACK(t *testing.T) {
	tests := []struct {
		cmd  byte
		want bool
	}{
		{AckOpenStream, true},
		{AckCloseStream, true},
		{AckPushStreamData, true},
		{CmdOpenStream, false},
		{CmdCloseStream, false},
		{CmdPushStreamData, false},
	}
	for _, tt := range tests {
		buf := NewBufferWithCmd(tt.cmd)
		assert.Equal(t, tt.want, buf.IsACK(), "cmd=0x%02x", tt.cmd)
	}
}

func TestBuffer_SID(t *testing.T) {
	t.Run("all zeros", func(t *testing.T) {
		buf := NewBuffer()
		buf.SetHeader(0, 0, 0, 0, 0)
		assert.Equal(t, uint64(0), buf.SID())
	})

	t.Run("all max", func(t *testing.T) {
		buf := NewBuffer()
		buf.SetHeader(0, 0xFFFF, 0xFFFF, 0xFFFF, 0xFFFF)
		assert.Equal(t, uint64(0xFFFFFFFFFFFFFFFF), buf.SID())
	})

	t.Run("SIDStr format", func(t *testing.T) {
		buf := NewBuffer()
		buf.SetHeader(0, 12, 34, 56, 78)
		assert.Equal(t, "56:78-12:34", buf.SIDStr())
	})
}

func TestBuffer_SwapSrcDist(t *testing.T) {
	buf := NewBuffer()
	buf.SetHeader(0, 12, 34, 56, 78)

	buf.SwapSrcDist()

	assert.Equal(t, uint16(56), buf.DistIP())
	assert.Equal(t, uint16(78), buf.DistPort())
	assert.Equal(t, uint16(12), buf.SrcIP())
	assert.Equal(t, uint16(34), buf.SrcPort())
}

func TestBuffer_Payload(t *testing.T) {
	t.Run("normal payload", func(t *testing.T) {
		buf := NewBufferWithCmd(CmdOpenStream)
		require.NoError(t, buf.SetPayload([]byte("helloworld")))
		assert.Equal(t, uint16(10), buf.PayloadSize())
		assert.Equal(t, uint16(0), buf.DataACKSize())
	})

	t.Run("data ACK size", func(t *testing.T) {
		buf := NewBufferWithCmd(AckPushStreamData)
		buf.SetDataACKSize(10245)
		assert.Equal(t, uint16(0), buf.PayloadSize())
		assert.Equal(t, uint16(10245), buf.DataACKSize())
		assert.Nil(t, buf.Payload)
	})

	t.Run("overflow", func(t *testing.T) {
		buf := NewBuffer()
		err := buf.SetPayload(make([]byte, MaxPayloadSize+1))
		assert.ErrorIs(t, err, ErrPayloadOverflow)
	})

	t.Run("max payload", func(t *testing.T) {
		buf := NewBuffer()
		require.NoError(t, buf.SetPayload(make([]byte, MaxPayloadSize)))
		assert.Equal(t, uint16(MaxPayloadSize), buf.PayloadSize())
	})

	t.Run("empty payload", func(t *testing.T) {
		buf := NewBufferWithCmd(CmdPushStreamData)
		require.NoError(t, buf.SetPayload(nil))
		assert.Equal(t, uint16(0), buf.PayloadSize())
	})

	t.Run("empty slice vs nil", func(t *testing.T) {
		buf := NewBufferWithCmd(CmdPushStreamData)
		require.NoError(t, buf.SetPayload([]byte{}))
		assert.Equal(t, uint16(0), buf.PayloadSize())
		assert.NotNil(t, buf.Payload) // 空切片不是 nil
	})

	t.Run("union field: SetPayload then switch to ACK", func(t *testing.T) {
		// 先设置 payload，再切换 cmd 为 AckPushStreamData
		// PayloadSize 应返回 0（因为 cmd 是 ACK），DataACKSize 应读到之前写入的值
		buf := NewBufferWithCmd(CmdPushStreamData)
		require.NoError(t, buf.SetPayload([]byte("12345")))
		assert.Equal(t, uint16(5), buf.PayloadSize())

		buf.SetCmd(AckPushStreamData)
		assert.Equal(t, uint16(0), buf.PayloadSize(), "ACK cmd should mask PayloadSize to 0")
		assert.Equal(t, uint16(5), buf.DataACKSize(), "DataACKSize should read the raw value")
	})

	t.Run("CmdAdmit is not ACK", func(t *testing.T) {
		buf := NewBufferWithCmd(CmdAdmit)
		assert.False(t, buf.IsACK())
		require.NoError(t, buf.SetPayload([]byte("x")))
		assert.Equal(t, uint16(1), buf.PayloadSize())
	})
}

func TestBuffer_CmdName(t *testing.T) {
	tests := []struct {
		cmd  byte
		want string
	}{
		{CmdOpenStream, "open"},
		{CmdCloseStream, "close"},
		{CmdPushStreamData, "data"},
		{CmdPushMessage, "push"},
		{CmdPingDomain, "ping"},
		{AckOpenStream, "open.ack"},
		{AckCloseStream, "close.ack"},
		{AckPushStreamData, "data.ack"},
		{AckPushMessage, "push.ack"},
		{AckPingDomain, "ping.ack"},
		{0xfe, fmt.Sprintf("<%v>", 0xfe)},
		{0xff, fmt.Sprintf("<%v>.ack", 0xfe)},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			buf := NewBufferWithCmd(tt.cmd)
			assert.Equal(t, tt.want, buf.CmdName())
		})
	}
}

func TestBuffer_HeaderString(t *testing.T) {
	buf := NewBuffer()
	buf.SetCmd(CmdOpenStream)
	buf.SetSrc(1, 2)
	buf.SetDist(3, 4)
	assert.Equal(t, "[open][src=1:2][dist=3:4][size=0]", buf.HeaderString())
}

func TestBuffer_WriteTo(t *testing.T) {
	t.Run("header only", func(t *testing.T) {
		buf := NewBufferWithCmd(CmdPushStreamData)
		var w bytes.Buffer
		n, err := buf.WriteTo(&w)
		require.NoError(t, err)
		assert.Equal(t, int64(HeaderSz), n)
		assert.Equal(t, buf.Head[:], w.Bytes())
	})

	t.Run("header + payload", func(t *testing.T) {
		buf := NewBufferWithCmd(CmdPushStreamData)
		require.NoError(t, buf.SetPayload([]byte("hello")))
		var w bytes.Buffer
		n, err := buf.WriteTo(&w)
		require.NoError(t, err)
		assert.Equal(t, int64(HeaderSz+5), n)
		assert.Equal(t, buf.Head[:], w.Bytes()[:HeaderSz])
		assert.Equal(t, []byte("hello"), w.Bytes()[HeaderSz:])
	})

	t.Run("header write fails", func(t *testing.T) {
		buf := NewBufferWithCmd(CmdPushStreamData)
		w := &limitWriter{limit: 0}
		_, err := buf.WriteTo(w)
		assert.ErrorIs(t, err, ErrWriteHeaderFailed)
	})

	t.Run("payload write fails", func(t *testing.T) {
		buf := NewBufferWithCmd(CmdPushStreamData)
		require.NoError(t, buf.SetPayload([]byte("hello")))
		// 允许写完 header，但 payload 写入失败
		w := &limitWriter{limit: HeaderSz}
		_, err := buf.WriteTo(w)
		assert.ErrorIs(t, err, ErrWritePayloadFailed)
	})
}

// limitWriter 在写入 limit 字节后返回错误
type limitWriter struct {
	written int
	limit   int
}

func (w *limitWriter) Write(p []byte) (int, error) {
	remaining := w.limit - w.written
	if remaining <= 0 {
		return 0, io.ErrShortWrite
	}
	if len(p) > remaining {
		w.written += remaining
		return remaining, io.ErrShortWrite
	}
	w.written += len(p)
	return len(p), nil
}

func TestBuffer_Pool(t *testing.T) {
	buf := GetBuffer()
	require.NotNil(t, buf)

	buf.SetCmd(CmdPushStreamData)
	buf.SetPayload([]byte("data"))

	PutBuffer(buf)

	// 归还后 buf 应被重置
	assert.Equal(t, Header{}, buf.Head)
	assert.Nil(t, buf.Payload)

	// 再次获取应正常
	buf2 := GetBuffer()
	assert.NotNil(t, buf2)
}
