package ws

import (
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/net-agent/flex/v3/internal/framing"
	"github.com/net-agent/flex/v3/packet"
	"github.com/net-agent/flex/v3/wire"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// wsTestFrame 构造一个 framing.FrameBytes，便于直接验证 connImpl 的
// framing.Transport 委托方法。
func wsTestFrame(cmd byte, srcPort uint16, payload []byte) framing.FrameBytes {
	var h packet.Header
	h[0] = cmd
	binary.BigEndian.PutUint16(h[7:9], srcPort)
	binary.BigEndian.PutUint16(h[9:11], uint16(len(payload)))
	return framing.FrameBytes{
		Header:  [wire.HeaderSize]byte(h),
		Payload: payload,
	}
}

// connImpl 同时暴露 packet.Conn 与 framing.Transport 两套视图。这里验证
// Transport 委托确实读写同一个 websocket.Conn。
func TestConn_TransportDelegation(t *testing.T) {
	client, server := rawWSPair(t)
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	impl1 := NewConn(client).(*connImpl)
	impl2 := NewConn(server).(*connImpl)

	assert.NotNil(t, impl1.LocalAddr())
	assert.NotNil(t, impl1.RemoteAddr())
	assert.Equal(t, client.UnderlyingConn().LocalAddr(), impl1.LocalAddr())
	assert.Equal(t, client.UnderlyingConn().RemoteAddr(), impl1.RemoteAddr())

	frames := []framing.FrameBytes{
		wsTestFrame(packet.CmdPushStreamData, 7, []byte("hello")),
		wsTestFrame(packet.CmdPingDomain, 9, nil),
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- impl2.WriteFrames(frames)
	}()

	var hdr [wire.HeaderSize]byte
	require.NoError(t, impl1.ReadHeader(hdr[:]))
	assert.Equal(t, byte(packet.CmdPushStreamData), hdr[0])

	payload, err := impl1.ReadPayload(5)
	require.NoError(t, err)
	assert.Equal(t, []byte("hello"), payload)

	require.NoError(t, impl1.ReadHeader(hdr[:]))
	assert.Equal(t, byte(packet.CmdPingDomain), hdr[0])

	payload, err = impl1.ReadPayload(0)
	require.NoError(t, err)
	assert.Nil(t, payload)

	require.NoError(t, <-errCh)
}

func TestConn_ReadPayloadInto(t *testing.T) {
	client, server := rawWSPair(t)
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	impl1 := NewConn(client).(*connImpl)
	impl2 := NewConn(server).(*connImpl)

	errCh := make(chan error, 1)
	go func() {
		errCh <- impl2.WriteFrames([]framing.FrameBytes{
			wsTestFrame(packet.CmdPushStreamData, 3, []byte("copy")),
		})
	}()

	var hdr [wire.HeaderSize]byte
	require.NoError(t, impl1.ReadHeader(hdr[:]))

	dst := make([]byte, 4)
	require.NoError(t, impl1.ReadPayloadInto(dst))
	assert.Equal(t, []byte("copy"), dst)

	require.NoError(t, <-errCh)
}

// plainWSTransport 实现 framing.Transport，但刻意不实现
// framing.PayloadReader，用于覆盖 connImpl.ReadPayloadInto 的通用回退分支。
type plainWSTransport struct {
	payload []byte
	err     error
	gotN    int
}

func (p *plainWSTransport) ReadHeader([]byte) error { return nil }

func (p *plainWSTransport) ReadPayload(n int) ([]byte, error) {
	p.gotN = n
	if p.err != nil {
		return nil, p.err
	}
	return p.payload, nil
}

func (p *plainWSTransport) WriteFrames([]framing.FrameBytes) error { return nil }
func (p *plainWSTransport) SetReadDeadline(time.Time) error        { return nil }
func (p *plainWSTransport) SetWriteDeadline(time.Time) error       { return nil }
func (p *plainWSTransport) LocalAddr() net.Addr                    { return nil }
func (p *plainWSTransport) RemoteAddr() net.Addr                   { return nil }
func (p *plainWSTransport) Close() error                           { return nil }

func TestConn_ReadPayloadIntoFallback(t *testing.T) {
	client, server := rawWSPair(t)
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	impl := NewConn(client).(*connImpl)

	ft := &plainWSTransport{payload: []byte("copy")}
	impl.ft = ft

	dst := make([]byte, 4)
	require.NoError(t, impl.ReadPayloadInto(dst))
	assert.Equal(t, []byte("copy"), dst)
	assert.Equal(t, 4, ft.gotN, "fallback must read exactly len(dst) bytes")

	wantErr := errors.New("payload read failed")
	impl.ft = &plainWSTransport{err: wantErr}
	require.ErrorIs(t, impl.ReadPayloadInto(dst), wantErr)
}

// deadline 委托必须真正作用到底层 websocket 连接。
func TestConn_DeadlineDelegation(t *testing.T) {
	t.Run("read deadline", func(t *testing.T) {
		client, server := rawWSPair(t)
		defer client.Close()
		defer server.Close()
		impl := NewConn(client).(*connImpl)

		require.NoError(t, impl.SetReadDeadline(time.Now().Add(-time.Second)))
		var hdr [wire.HeaderSize]byte
		require.Error(t, impl.ReadHeader(hdr[:]), "expired read deadline should fail")
	})

	t.Run("write deadline", func(t *testing.T) {
		client, server := rawWSPair(t)
		defer client.Close()
		defer server.Close()
		impl := NewConn(client).(*connImpl)

		require.NoError(t, impl.SetWriteDeadline(time.Now().Add(-time.Second)))
		err := impl.WriteFrames([]framing.FrameBytes{
			wsTestFrame(packet.CmdPingDomain, 1, nil),
		})
		require.Error(t, err, "expired write deadline should fail")
	})
}

// wsWriter must surface the nw.Close() flush error on the first write after the
// peer/connection is gone, not only on the next call.
func TestWriter_WriteAfterCloseError(t *testing.T) {
	client, server := rawWSPair(t)
	defer server.Close()
	pc := NewConn(client)
	require.NoError(t, pc.Close())

	buf := packet.NewBufferWithCmd(packet.CmdPingDomain)
	require.Error(t, pc.WriteBuffer(buf), "first write after close must report the flush error")
	require.Error(t, pc.WriteBuffer(buf), "subsequent writes must keep reporting the error")
}

// MaxFrameSize 是合法上限（读限只拒绝 > MaxFrameSize 的帧），必须能正常读取。
func TestReader_ExactMaxFrameSize(t *testing.T) {
	client, server := rawWSPair(t)
	defer client.Close()
	defer server.Close()
	pc := NewConn(client)

	frame := make([]byte, MaxFrameSize)
	frame[0] = packet.CmdPushStreamData
	binary.BigEndian.PutUint16(frame[9:11], packet.MaxPayloadSize)

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.WriteMessage(websocket.BinaryMessage, frame)
	}()

	got, err := pc.ReadBuffer()
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Len(t, got.Payload, packet.MaxPayloadSize)
	assert.Equal(t, uint16(packet.MaxPayloadSize), got.PayloadSize())

	require.NoError(t, <-errCh)
}
