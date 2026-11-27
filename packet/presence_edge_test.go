package packet

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 空/过短的 OK 应答必须返回空快照，而不是越界读取。
func TestDecodeSubscribeACK_ShortSuccessPayload(t *testing.T) {
	for _, payload := range [][]byte{
		{0},
		{0, 0x00},
		{0, 0x00, 0x01}, // count=1 但没有任何域名数据
	} {
		ack := DecodeSubscribeACK(payload)
		assert.True(t, ack.OK, "payload=%v", payload)
		assert.Empty(t, ack.Error, "payload=%v", payload)
		assert.Empty(t, ack.States, "payload=%v", payload)
	}
}

// 域名缺失 NUL 分隔符：count 尚未满足也要停止解析。
func TestDecodeSubscribeACK_DomainWithoutTerminator(t *testing.T) {
	ack := DecodeSubscribeACK([]byte{0, 0x00, 0x01, 'a', 'b'})
	require.True(t, ack.OK)
	assert.Empty(t, ack.States, "unterminated domain must be dropped")
}

// 域名之后固定字段不足 11 字节：该条状态必须被丢弃。
func TestDecodeSubscribeACK_TruncatedState(t *testing.T) {
	ack := DecodeSubscribeACK([]byte{0, 0x00, 0x01, 'a', 0x00, 'x', 'y'})
	require.True(t, ack.OK)
	assert.Empty(t, ack.States, "state with truncated fixed fields must be dropped")
}

// mac 缺失末尾 NUL 时，剩余字节整体作为 mac。
func TestDecodeSubscribeACK_MacWithoutTerminator(t *testing.T) {
	payload := []byte{0, 0x00, 0x01} // OK，count=1
	payload = append(payload, []byte("dom\x00")...)

	fixed := make([]byte, 11)
	fixed[0] = 1 // online
	binary.BigEndian.PutUint16(fixed[1:3], 7)
	binary.BigEndian.PutUint64(fixed[3:11], 5)
	payload = append(payload, fixed...)
	payload = append(payload, []byte("mac")...) // 没有结尾 NUL

	ack := DecodeSubscribeACK(payload)
	require.True(t, ack.OK)
	require.Len(t, ack.States, 1)
	assert.Equal(t, PresenceState{
		Domain:  "dom",
		Online:  true,
		IP:      7,
		Version: 5,
		Mac:     "mac",
	}, ack.States[0])
}

// 事件 payload 的域名字段缺失 NUL 时，剩余内容全部作为域名，且不再解析 mac。
func TestDecodePresenceEvent_DomainWithoutTerminator(t *testing.T) {
	payload := make([]byte, 19)
	payload[0] = 1 // online
	binary.BigEndian.PutUint16(payload[1:3], 9)
	binary.BigEndian.PutUint64(payload[3:11], 4)
	binary.BigEndian.PutUint64(payload[11:19], 2)
	payload = append(payload, []byte("dom")...)

	ev := DecodePresenceEvent(payload)
	assert.Equal(t, PresenceEvent{
		Seq:     2,
		Version: 4,
		Domain:  "dom",
		Online:  true,
		IP:      9,
	}, ev)
}
