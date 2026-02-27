package packet

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriter_WriteBuffer(t *testing.T) {
	t.Run("nil buffer", func(t *testing.T) {
		pc, _ := Pipe()
		assert.NoError(t, pc.WriteBuffer(nil))
	})

	t.Run("header only", func(t *testing.T) {
		pc1, pc2 := Pipe()
		go func() {
			require.NoError(t, pc1.WriteBuffer(NewBufferWithCmd(CmdPushStreamData)))
		}()
		recv, err := pc2.ReadBuffer()
		require.NoError(t, err)
		assert.Equal(t, CmdPushStreamData, recv.Cmd())
	})

	t.Run("with payload", func(t *testing.T) {
		pc1, pc2 := Pipe()
		buf := NewBufferWithCmd(CmdPushStreamData)
		require.NoError(t, buf.SetPayload([]byte("hello")))

		go func() {
			require.NoError(t, pc1.WriteBuffer(buf))
		}()
		recv, err := pc2.ReadBuffer()
		require.NoError(t, err)
		assert.Equal(t, []byte("hello"), recv.Payload)
	})
}

func TestWriter_ErrWriteHeaderFailed(t *testing.T) {
	pc1, pc2 := Pipe()
	c1 := pc1.GetRawConn()
	c2 := pc2.GetRawConn()

	go func() {
		// 读取不完整的 header 后关闭，触发写端错误
		buf := make([]byte, HeaderSz-1)
		io.ReadFull(c2, buf)
		c2.Close()
	}()

	w := NewConnWriter(c1)
	err := w.WriteBuffer(NewBuffer())
	assert.ErrorIs(t, err, ErrWriteHeaderFailed)
}

func TestWriter_ErrWritePayloadFailed(t *testing.T) {
	pc1, pc2 := Pipe()
	c1 := pc1.GetRawConn()
	c2 := pc2.GetRawConn()

	go func() {
		// 读完 header + 1 字节后关闭，触发 payload 写入错误
		buf := make([]byte, HeaderSz+1)
		io.ReadFull(c2, buf)
		c2.Close()
	}()

	w := NewConnWriter(c1)
	pbuf := NewBuffer()
	require.NoError(t, pbuf.SetPayload([]byte("helloworld")))
	err := w.WriteBuffer(pbuf)
	assert.ErrorIs(t, err, ErrWritePayloadFailed)
}

func TestWriter_Timeout(t *testing.T) {
	c, _ := net.Pipe()
	pc := NewWithConn(c)
	pc.SetWriteTimeout(time.Millisecond * 100)
	err := pc.WriteBuffer(NewBufferWithCmd(0))
	assert.Error(t, err)
}

func TestWriter_BatchWrite(t *testing.T) {
	t.Run("empty batch", func(t *testing.T) {
		pc, _ := Pipe()
		w := NewConnWriter(pc.GetRawConn())
		bw := w.(*connWriter)
		assert.NoError(t, bw.WriteBufferBatch(nil))
	})

	t.Run("multiple buffers", func(t *testing.T) {
		pc1, pc2 := Pipe()
		bufs := make([]*Buffer, 5)
		for i := range bufs {
			bufs[i] = NewBufferWithCmd(CmdPushStreamData)
			bufs[i].SetSrcPort(uint16(i))
			bufs[i].SetPayload([]byte("data"))
		}

		go func() {
			w := NewConnWriter(pc1.GetRawConn())
			bw := w.(*connWriter)
			require.NoError(t, bw.WriteBufferBatch(bufs))
		}()

		for i := range bufs {
			recv, err := pc2.ReadBuffer()
			require.NoError(t, err)
			assert.Equal(t, uint16(i), recv.SrcPort(), "packet %d", i)
			assert.Equal(t, []byte("data"), recv.Payload, "packet %d", i)
		}
	})

	t.Run("mixed payload", func(t *testing.T) {
		// 有些 buffer 有 payload，有些没有
		pc1, pc2 := Pipe()
		bufs := []*Buffer{
			NewBufferWithCmd(CmdPushStreamData),                                                  // 无 payload
			func() *Buffer { b := NewBufferWithCmd(CmdPushStreamData); b.SetPayload([]byte("a")); return b }(), // 有 payload
			NewBufferWithCmd(CmdCloseStream),                                                     // 无 payload
			func() *Buffer { b := NewBufferWithCmd(CmdPushStreamData); b.SetPayload([]byte("bc")); return b }(), // 有 payload
		}

		go func() {
			w := NewConnWriter(pc1.GetRawConn())
			bw := w.(*connWriter)
			require.NoError(t, bw.WriteBufferBatch(bufs))
		}()

		for i, sent := range bufs {
			recv, err := pc2.ReadBuffer()
			require.NoError(t, err, "packet %d", i)
			assert.Equal(t, sent.Cmd(), recv.Cmd(), "packet %d cmd", i)
			assert.Equal(t, sent.Payload, recv.Payload, "packet %d payload", i)
		}
	})
}

func TestWriter_WriteAfterClose(t *testing.T) {
	pc1, _ := Pipe()
	pc1.Close()
	err := pc1.WriteBuffer(NewBufferWithCmd(CmdPushStreamData))
	assert.Error(t, err)
}
