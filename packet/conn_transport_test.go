package packet

import (
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/net-agent/flex/v4/internal/framing"
	"github.com/net-agent/flex/v4/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testFrame 构造一个 framing.FrameBytes，便于直接验证 connImpl 的
// framing.Transport 委托方法。
func testFrame(cmd byte, srcPort uint16, payload []byte) framing.FrameBytes {
	var h Header
	h[0] = cmd
	binary.BigEndian.PutUint16(h[7:9], srcPort)
	binary.BigEndian.PutUint16(h[9:11], uint16(len(payload)))
	return framing.FrameBytes{
		Header:  [wire.HeaderSize]byte(h),
		Payload: payload,
	}
}

// connImpl 同时暴露 packet.Conn 与 framing.Transport 两套视图，两者共享同一个
// net.Conn。这里逐项验证 Transport 委托确实作用在底层连接上。
func TestConnImpl_TransportDelegation(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		c1.Close()
		c2.Close()
	})
	impl1 := NewWithConn(c1).(*connImpl)
	impl2 := NewWithConn(c2).(*connImpl)

	assert.NotNil(t, impl1.LocalAddr())
	assert.NotNil(t, impl1.RemoteAddr())
	assert.Equal(t, c1.LocalAddr(), impl1.LocalAddr())
	assert.Equal(t, c1.RemoteAddr(), impl1.RemoteAddr())

	frames := []framing.FrameBytes{
		testFrame(CmdPushStreamData, 7, []byte("hello")),
		testFrame(CmdPingDomain, 9, nil),
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- impl2.WriteFrames(frames)
	}()

	var hdr [wire.HeaderSize]byte
	require.NoError(t, impl1.ReadHeader(hdr[:]))
	assert.Equal(t, byte(CmdPushStreamData), hdr[0])

	payload, err := impl1.ReadPayload(5)
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), payload)

	require.NoError(t, impl1.ReadHeader(hdr[:]))
	assert.Equal(t, byte(CmdPingDomain), hdr[0])

	payload, err = impl1.ReadPayload(0)
	require.NoError(t, err)
	assert.Nil(t, payload)

	require.NoError(t, <-errCh)
}

func TestConnImpl_ReadPayloadInto(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		c1.Close()
		c2.Close()
	})
	impl1 := NewWithConn(c1).(*connImpl)
	impl2 := NewWithConn(c2).(*connImpl)

	errCh := make(chan error, 1)
	go func() {
		errCh <- impl2.WriteFrames([]framing.FrameBytes{
			testFrame(CmdPushStreamData, 3, []byte("copy")),
		})
	}()

	var hdr [wire.HeaderSize]byte
	require.NoError(t, impl1.ReadHeader(hdr[:]))

	dst := make([]byte, 4)
	require.NoError(t, impl1.ReadPayloadInto(dst))
	assert.Equal(t, []byte("copy"), dst)

	require.NoError(t, <-errCh)
}

// plainTransport 实现 framing.Transport，但刻意不实现 framing.PayloadReader，
// 用于覆盖 connImpl.ReadPayloadInto 的通用回退分支。
type plainTransport struct {
	payload []byte
	err     error
	gotN    int
}

func (p *plainTransport) ReadHeader([]byte) error { return nil }

func (p *plainTransport) ReadPayload(n int) ([]byte, error) {
	p.gotN = n
	if p.err != nil {
		return nil, p.err
	}
	return p.payload, nil
}

func (p *plainTransport) WriteFrames([]framing.FrameBytes) error { return nil }
func (p *plainTransport) SetReadDeadline(time.Time) error        { return nil }
func (p *plainTransport) SetWriteDeadline(time.Time) error       { return nil }
func (p *plainTransport) LocalAddr() net.Addr                    { return nil }
func (p *plainTransport) RemoteAddr() net.Addr                   { return nil }
func (p *plainTransport) Close() error                           { return nil }

func TestConnImpl_ReadPayloadIntoFallback(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		c1.Close()
		c2.Close()
	})
	impl := NewWithConn(c1).(*connImpl)

	ft := &plainTransport{payload: []byte("copy")}
	impl.ft = ft

	dst := make([]byte, 4)
	require.NoError(t, impl.ReadPayloadInto(dst))
	assert.Equal(t, []byte("copy"), dst)
	assert.Equal(t, 4, ft.gotN, "fallback must read exactly len(dst) bytes")

	wantErr := errors.New("payload read failed")
	impl.ft = &plainTransport{err: wantErr}
	require.ErrorIs(t, impl.ReadPayloadInto(dst), wantErr)
}

// SetReadDeadline/SetWriteDeadline 必须真正作用到底层连接：过期 deadline
// 会让随后的 I/O 立刻失败，清除后连接可以继续使用。
func TestConnImpl_DeadlineDelegation(t *testing.T) {
	t.Run("read deadline", func(t *testing.T) {
		c1, c2 := net.Pipe()
		defer c1.Close()
		defer c2.Close()
		impl := NewWithConn(c1).(*connImpl)
		peer := NewWithConn(c2).(*connImpl)

		require.NoError(t, impl.SetReadDeadline(time.Now().Add(-time.Second)))
		var hdr [wire.HeaderSize]byte
		require.Error(t, impl.ReadHeader(hdr[:]), "expired read deadline should fail")

		require.NoError(t, impl.SetReadDeadline(time.Time{}))
		errCh := make(chan error, 1)
		go func() {
			errCh <- peer.WriteFrames([]framing.FrameBytes{
				testFrame(CmdPingDomain, 1, nil),
			})
		}()
		require.NoError(t, impl.ReadHeader(hdr[:]), "cleared deadline should allow reads")
		assert.Equal(t, byte(CmdPingDomain), hdr[0])
		require.NoError(t, <-errCh)
	})

	t.Run("write deadline", func(t *testing.T) {
		c1, c2 := net.Pipe()
		defer c1.Close()
		defer c2.Close()
		impl := NewWithConn(c1).(*connImpl)
		peer := NewWithConn(c2).(*connImpl)

		require.NoError(t, impl.SetWriteDeadline(time.Now().Add(-time.Second)))
		err := impl.WriteFrames([]framing.FrameBytes{
			testFrame(CmdPingDomain, 1, nil),
		})
		require.Error(t, err, "expired write deadline should fail")

		require.NoError(t, impl.SetWriteDeadline(time.Time{}))
		errCh := make(chan error, 1)
		var hdr [wire.HeaderSize]byte
		go func() {
			errCh <- peer.ReadHeader(hdr[:])
		}()
		require.NoError(t, impl.WriteFrames([]framing.FrameBytes{
			testFrame(CmdPingDomain, 1, nil),
		}), "cleared deadline should allow writes")
		require.NoError(t, <-errCh)
	})
}

// recordingWriter 只实现 packet.Writer（不实现 BatchWriter），用于覆盖
// connImpl.WriteBufferBatch 的逐包回退路径。
type recordingWriter struct {
	bufs   []*Buffer
	failAt int
	err    error
}

func (w *recordingWriter) WriteBuffer(buf *Buffer) error {
	w.bufs = append(w.bufs, buf)
	if w.failAt > 0 && len(w.bufs) == w.failAt {
		return w.err
	}
	return nil
}

func (w *recordingWriter) SetWriteTimeout(time.Duration) {}

func TestConnImpl_WriteBufferBatchFallback(t *testing.T) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() {
		c1.Close()
		c2.Close()
	})
	impl := NewWithConn(c1).(*connImpl)

	rw := &recordingWriter{}
	impl.Writer = rw
	b1 := NewBufferWithCmd(CmdPushStreamData)
	b2 := NewBufferWithCmd(CmdPingDomain)

	require.NoError(t, impl.WriteBufferBatch([]*Buffer{b1, b2}))
	assert.Equal(t, []*Buffer{b1, b2}, rw.bufs)

	rw.bufs = nil
	require.NoError(t, impl.WriteBufferBatch(nil))
	assert.Empty(t, rw.bufs, "empty batch must not write anything")

	wantErr := errors.New("write failed")
	rw = &recordingWriter{failAt: 1, err: wantErr}
	impl.Writer = rw
	require.ErrorIs(t, impl.WriteBufferBatch([]*Buffer{b1, b2}), wantErr)
	assert.Len(t, rw.bufs, 1, "fallback must stop at the first write error")
	assert.Same(t, b1, rw.bufs[0])
}
