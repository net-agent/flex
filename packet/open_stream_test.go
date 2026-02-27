package packet

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestOpenStreamRequest_Encode_Decode(t *testing.T) {
	tests := []struct {
		name   string
		req    OpenStreamRequest
	}{
		{"basic", OpenStreamRequest{Domain: "example.com", WindowSize: 8192}},
		{"empty domain", OpenStreamRequest{Domain: "", WindowSize: 1024}},
		{"zero window", OpenStreamRequest{Domain: "test.local", WindowSize: 0}},
		{"max window", OpenStreamRequest{Domain: "a", WindowSize: 0xFFFFFFFF}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded := tt.req.Encode()
			decoded := DecodeOpenStreamRequest(encoded)
			assert.Equal(t, tt.req.Domain, decoded.Domain)
			assert.Equal(t, tt.req.WindowSize, decoded.WindowSize)
		})
	}
}

func TestDecodeOpenStreamRequest_NoNullTerminator(t *testing.T) {
	// payload 中没有 0x00 分隔符，整个 payload 作为 domain
	payload := []byte("raw-domain")
	decoded := DecodeOpenStreamRequest(payload)
	assert.Equal(t, "raw-domain", decoded.Domain)
	assert.Equal(t, uint32(0), decoded.WindowSize)
}

func TestDecodeOpenStreamRequest_ShortPayload(t *testing.T) {
	// null 后不足 4 字节，windowSize 应为 0
	payload := []byte("d\x00\x01")
	decoded := DecodeOpenStreamRequest(payload)
	assert.Equal(t, "d", decoded.Domain)
	assert.Equal(t, uint32(0), decoded.WindowSize)
}

func TestOpenStreamACK_Encode_Decode(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		ack := OpenStreamACK{OK: true, WindowSize: 4096}
		encoded := ack.Encode()
		decoded := DecodeOpenStreamACK(encoded)
		assert.True(t, decoded.OK)
		assert.Equal(t, uint32(4096), decoded.WindowSize)
		assert.Empty(t, decoded.Error)
	})

	t.Run("success zero window", func(t *testing.T) {
		ack := OpenStreamACK{OK: true, WindowSize: 0}
		encoded := ack.Encode()
		decoded := DecodeOpenStreamACK(encoded)
		assert.True(t, decoded.OK)
		assert.Equal(t, uint32(0), decoded.WindowSize)
	})

	t.Run("failure", func(t *testing.T) {
		ack := OpenStreamACK{OK: false, Error: "connection refused"}
		encoded := ack.Encode()
		decoded := DecodeOpenStreamACK(encoded)
		assert.False(t, decoded.OK)
		assert.Equal(t, "connection refused", decoded.Error)
	})
}

func TestDecodeOpenStreamACK_EmptyPayload(t *testing.T) {
	decoded := DecodeOpenStreamACK(nil)
	assert.True(t, decoded.OK)
	assert.Equal(t, uint32(0), decoded.WindowSize)
}

func TestDecodeOpenStreamACK_ShortSuccessPayload(t *testing.T) {
	// 0x00 开头但不足 5 字节，windowSize 应为 0
	payload := []byte{0x00, 0x01}
	decoded := DecodeOpenStreamACK(payload)
	assert.True(t, decoded.OK)
	assert.Equal(t, uint32(0), decoded.WindowSize)
}

func TestOpenStreamACK_FailureEmptyError(t *testing.T) {
	// OK=false 但 Error 为空字符串
	ack := OpenStreamACK{OK: false, Error: ""}
	encoded := ack.Encode()
	// 空字符串编码为空 payload，DecodeOpenStreamACK 对空 payload 返回 OK=true
	decoded := DecodeOpenStreamACK(encoded)
	assert.True(t, decoded.OK, "empty error encodes as empty payload, which decodes as OK")
}

func TestDecodeOpenStreamRequest_EmptyPayload(t *testing.T) {
	decoded := DecodeOpenStreamRequest(nil)
	assert.Equal(t, "", decoded.Domain)
	assert.Equal(t, uint32(0), decoded.WindowSize)
}

func TestOpenStreamRequest_DomainWithNullByte(t *testing.T) {
	// domain 中包含 0x00 字节，Encode 会在 domain 末尾追加 0x00
	// Decode 会在第一个 0x00 处截断
	req := OpenStreamRequest{Domain: "ab\x00cd", WindowSize: 100}
	encoded := req.Encode()
	decoded := DecodeOpenStreamRequest(encoded)
	assert.Equal(t, "ab", decoded.Domain, "should truncate at first null byte")
}
