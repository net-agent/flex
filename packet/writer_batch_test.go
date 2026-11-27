package packet

import (
	"io"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// WriteBufferBatch 的空批处理必须是 no-op。
func TestWriter_BatchWrite_EmptySlice(t *testing.T) {
	pc1, pc2 := Pipe()
	defer pc1.Close()
	defer pc2.Close()

	bw := NewConnWriter(pc1.GetRawConn()).(BatchWriter)
	assert.NoError(t, bw.WriteBufferBatch(nil))
	assert.NoError(t, bw.WriteBufferBatch([]*Buffer{}))
}

// A nil element must be skipped, matching WriteBuffer's nil tolerance.
func TestWriter_BatchWrite_NilElement(t *testing.T) {
	pc1, pc2 := Pipe()
	defer pc1.Close()
	defer pc2.Close()

	bw := NewConnWriter(pc1.GetRawConn()).(BatchWriter)
	valid := NewBufferWithCmd(CmdPushStreamData)
	require.NoError(t, valid.SetPayload([]byte("ok")))

	readCh := make(chan *Buffer, 1)
	errCh := make(chan error, 1)
	go func() {
		b, err := pc2.ReadBuffer()
		if err != nil {
			errCh <- err
			return
		}
		readCh <- b
	}()

	require.NoError(t, bw.WriteBufferBatch([]*Buffer{nil, valid}))
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case got := <-readCh:
		assert.Equal(t, CmdPushStreamData, got.Cmd())
		assert.Equal(t, []byte("ok"), got.Payload)
	}
}

// 批次中途失败时错误必须冒泡，而不是被吞掉。
func TestWriter_BatchWrite_PartialFailure(t *testing.T) {
	t.Run("peer already closed", func(t *testing.T) {
		c1, c2 := net.Pipe()
		require.NoError(t, c2.Close())
		defer c1.Close()

		bw := NewConnWriter(c1).(BatchWriter)
		err := bw.WriteBufferBatch([]*Buffer{NewBufferWithCmd(CmdPushStreamData)})
		require.Error(t, err, "writing to a closed peer must fail")
	})

	t.Run("peer closes mid batch", func(t *testing.T) {
		pc1, pc2 := Pipe()
		defer pc1.Close()

		c2 := pc2.GetRawConn()
		readDone := make(chan struct{})
		go func() {
			defer close(readDone)
			// 只消费第一个包 header + 1 字节 payload，随后关闭对端。
			_, _ = io.ReadFull(c2, make([]byte, HeaderSz+1))
			_ = c2.Close()
		}()

		first := NewBufferWithCmd(CmdPushStreamData)
		require.NoError(t, first.SetPayload([]byte("ab")))
		second := NewBufferWithCmd(CmdPingDomain)

		bw := NewConnWriter(pc1.GetRawConn()).(BatchWriter)
		err := bw.WriteBufferBatch([]*Buffer{first, second})
		require.Error(t, err, "mid-batch disconnect must be reported")
		<-readDone
	})
}
