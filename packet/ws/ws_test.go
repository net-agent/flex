package ws

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/net-agent/flex/v3/packet"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rawWSPair 通过 httptest 建立一对原生 websocket 连接，用于需要直接操作
// websocket 帧的用例（如发送非二进制消息）。
func rawWSPair(t *testing.T) (client, server *websocket.Conn) {
	t.Helper()

	upgrader := websocket.Upgrader{}
	serverCh := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		serverCh <- c
	}))
	t.Cleanup(srv.Close)

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)

	return c, <-serverCh
}

func TestPipe_DataTransfer(t *testing.T) {
	pc1, pc2 := Pipe()
	defer pc1.Close()
	defer pc2.Close()

	msg := []byte("hello websocket")
	buf := packet.NewBuffer()
	buf.SetHeader(packet.CmdPushStreamData, 0, 1, 2, 3)
	require.NoError(t, buf.SetPayload(msg))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		require.NoError(t, pc2.WriteBuffer(buf))
	}()

	recvBuf, err := pc1.ReadBuffer()
	require.NoError(t, err)
	assert.Equal(t, buf.Head, recvBuf.Head)
	assert.True(t, bytes.Equal(buf.Payload, recvBuf.Payload))

	wg.Wait()
}

func TestPipe_Bidirectional(t *testing.T) {
	pc1, pc2 := Pipe()
	defer pc1.Close()
	defer pc2.Close()

	ping := packet.NewBufferWithCmd(packet.CmdPingDomain)
	pong := packet.NewBufferWithCmd(packet.AckPingDomain)

	go func() {
		require.NoError(t, pc1.WriteBuffer(ping))
		require.NoError(t, pc2.WriteBuffer(pong))
	}()

	recv1, err := pc2.ReadBuffer()
	require.NoError(t, err)
	assert.Equal(t, packet.CmdPingDomain, recv1.Cmd())

	recv2, err := pc1.ReadBuffer()
	require.NoError(t, err)
	assert.Equal(t, packet.AckPingDomain, recv2.Cmd())
}

func TestConn_GetRawConn(t *testing.T) {
	pc1, pc2 := Pipe()
	defer pc1.Close()
	defer pc2.Close()

	assert.NotNil(t, pc1.GetRawConn())
	assert.NotNil(t, pc2.GetRawConn())
}

func TestConn_Close(t *testing.T) {
	pc1, pc2 := Pipe()

	require.NoError(t, pc1.Close())

	pc2.SetReadTimeout(time.Second)
	_, err := pc2.ReadBuffer()
	assert.NotNil(t, err, "read after peer closed should fail")
}

func TestWriter_NilBuffer(t *testing.T) {
	pc1, pc2 := Pipe()
	defer pc1.Close()
	defer pc2.Close()

	assert.Nil(t, pc1.WriteBuffer(nil), "write nil buffer should be no-op")
}

func TestReader_BadDataType(t *testing.T) {
	client, server := rawWSPair(t)
	defer client.Close()
	defer server.Close()

	pc := NewConn(client)

	go func() {
		// 发送文本帧，ReadBuffer 应返回 ErrBadDataType
		require.NoError(t, server.WriteMessage(websocket.TextMessage, []byte("hi")))
	}()

	_, err := pc.ReadBuffer()
	assert.Equal(t, ErrBadDataType, err)
}

// 回归：长度小于 11 字节的二进制帧曾直接 data[:11] 切片，触发
// "slice bounds out of range" 打崩进程。必须以错误返回而非 panic。
func TestReader_ShortFrame(t *testing.T) {
	for _, size := range []int{0, 1, 3, 10} {
		t.Run(fmt.Sprintf("len=%d", size), func(t *testing.T) {
			client, server := rawWSPair(t)
			defer client.Close()
			defer server.Close()

			pc := NewConn(client)
			go func() {
				_ = server.WriteMessage(websocket.BinaryMessage, make([]byte, size))
			}()

			_, err := pc.ReadBuffer()
			assert.ErrorIs(t, err, ErrShortFrame)
		})
	}
}

// 回归：头部 Size 与实际 payload 不一致时，若放行会在转发到 TCP 对端时
// 造成字节流错位（对端按头部长度读取）。
func TestReader_FrameSizeMismatch(t *testing.T) {
	client, server := rawWSPair(t)
	defer client.Close()
	defer server.Close()

	pc := NewConn(client)

	// 头部声明 payload=5，实际只带 2 字节
	frame := make([]byte, packet.HeaderSz+2)
	copy(frame, []byte("hello"))
	frame[0] = packet.CmdPushStreamData
	binary.BigEndian.PutUint16(frame[9:11], 5)

	go func() {
		_ = server.WriteMessage(websocket.BinaryMessage, frame)
	}()

	_, err := pc.ReadBuffer()
	assert.ErrorIs(t, err, ErrBadFrameSize)
}

// AckPushStreamData 的 [9:11] 是 ACK 计数，帧里不允许再带 payload。
func TestReader_AckDataWithPayload(t *testing.T) {
	client, server := rawWSPair(t)
	defer client.Close()
	defer server.Close()

	pc := NewConn(client)

	frame := make([]byte, packet.HeaderSz+1)
	frame[0] = packet.AckPushStreamData
	binary.BigEndian.PutUint16(frame[9:11], 1234)

	go func() {
		_ = server.WriteMessage(websocket.BinaryMessage, frame)
	}()

	_, err := pc.ReadBuffer()
	assert.ErrorIs(t, err, ErrBadFrameSize)
}

// 超大帧必须被读限截断，而不是无限分配内存。
func TestReader_OversizeFrame(t *testing.T) {
	client, server := rawWSPair(t)
	defer client.Close()
	defer server.Close()

	pc := NewConn(client)

	go func() {
		_ = server.WriteMessage(websocket.BinaryMessage, make([]byte, MaxFrameSize+1))
	}()

	done := make(chan error, 1)
	go func() {
		_, err := pc.ReadBuffer()
		done <- err
	}()

	select {
	case err := <-done:
		assert.Error(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("oversize frame was not rejected")
	}
}

func TestReader_ValidFrameAfterValidation(t *testing.T) {
	client, server := rawWSPair(t)
	defer client.Close()
	defer server.Close()

	pc := NewConn(client)

	src := packet.NewBufferWithCmd(packet.CmdPushStreamData)
	src.SetHeader(packet.CmdPushStreamData, 1, 2, 3, 4)
	require.NoError(t, src.SetPayload([]byte("payload")))

	go func() {
		_ = server.WriteMessage(websocket.BinaryMessage, append(src.Head[:], src.Payload...))
	}()

	got, err := pc.ReadBuffer()
	require.NoError(t, err)
	assert.Equal(t, src.Head, got.Head)
	assert.Equal(t, src.Payload, got.Payload)
}

func TestReader_ReadTimeout(t *testing.T) {
	client, server := rawWSPair(t)
	defer client.Close()
	defer server.Close()

	pc := NewConn(client)
	require.NoError(t, pc.SetReadTimeout(time.Millisecond*100))

	_, err := pc.ReadBuffer()
	assert.NotNil(t, err, "read with no data should timeout")
}

func TestReader_ClearReadTimeout(t *testing.T) {
	pc1, _ := Pipe()
	defer pc1.Close()

	// timeout 为 0 时应清除 deadline，不报错即可
	assert.Nil(t, pc1.SetReadTimeout(0))
}

func TestWriter_SetWriteTimeout(t *testing.T) {
	pc1, pc2 := Pipe()
	defer pc1.Close()
	defer pc2.Close()

	pc1.SetWriteTimeout(time.Second)
	pc1.SetWriteTimeout(0) // 清除 deadline

	buf := packet.NewBufferWithCmd(packet.CmdPingDomain)
	go func() {
		require.NoError(t, pc1.WriteBuffer(buf))
	}()

	_, err := pc2.ReadBuffer()
	assert.Nil(t, err, "write after clearing deadline should ok")
}
