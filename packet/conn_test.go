package packet

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConn_DataTransfer(t *testing.T) {
	pc1, pc2 := Pipe()

	msg := []byte("hello world")
	buf := NewBuffer()
	buf.SetHeader(CmdCloseStream, 0, 1, 2, 3)
	require.NoError(t, buf.SetPayload(msg))

	errCh := make(chan error, 1)
	go func() { errCh <- pc2.WriteBuffer(buf) }()

	recvBuf, err := pc1.ReadBuffer()
	require.NoError(t, err)
	assert.Equal(t, buf.Head, recvBuf.Head)
	assert.True(t, bytes.Equal(buf.Payload, recvBuf.Payload))
	require.NoError(t, <-errCh)
}

func TestConn_GetRawConn(t *testing.T) {
	pc1, _ := Pipe()
	assert.NotNil(t, pc1.GetRawConn())
}

func TestConn_WriteBufferBatch(t *testing.T) {
	pc1, pc2 := Pipe()

	bufs := make([]*Buffer, 3)
	for i := range bufs {
		bufs[i] = NewBufferWithCmd(CmdPushStreamData)
		bufs[i].SetSrcPort(uint16(i))
	}

	errCh := make(chan error, 1)
	go func() {
		impl := pc2.(*connImpl)
		errCh <- impl.WriteBufferBatch(bufs)
	}()

	for i := range bufs {
		recv, err := pc1.ReadBuffer()
		require.NoError(t, err)
		assert.Equal(t, uint16(i), recv.SrcPort(), "packet %d", i)
	}
	require.NoError(t, <-errCh)
}
