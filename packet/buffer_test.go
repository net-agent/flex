package packet

import (
	"bytes"
	"fmt"
	"sync"
	"testing"

	"github.com/net-agent/flex/v2/vars"
	"github.com/stretchr/testify/assert"
)

type testCase struct {
	cmd              byte
	distIP, distPort uint16
	srcIP, srcPort   uint16
	token            byte
	payload          []byte
}

func TestSetGet(t *testing.T) {
	cases := []testCase{
		{1, 2, 3, 4, 5, 6, []byte("hello")},
	}

	for _, c := range cases {
		buf := NewBuffer(nil)
		buf.SetHeader(c.cmd, c.distIP, c.distPort, c.srcIP, c.srcPort)
		buf.SetPayload(c.payload)

		assert.Equal(t, c.cmd, buf.Cmd())
		assert.Equal(t, c.distIP, buf.DistIP())
		assert.Equal(t, c.distPort, buf.DistPort())
		assert.Equal(t, c.srcIP, buf.SrcIP())
		assert.Equal(t, c.srcPort, buf.SrcPort())
		assert.True(t, bytes.Equal(buf.Payload, c.payload))
	}
}

func TestIsACK(t *testing.T) {
	pbuf := NewBufferWithCmd(AckOpenStream)
	assert.True(t, pbuf.IsACK())
	pbuf.SetCmd(CmdOpenStream)
	assert.False(t, pbuf.IsACK())
}

func TestSID(t *testing.T) {
	pbuf := NewBuffer(nil)
	pbuf.SetHeader(0, 0, 0, 0, 0)
	assert.Equal(t, uint64(0), pbuf.SID())

	pbuf.SetHeader(0, 0xffff, 0xffff, 0xffff, 0xffff)
	assert.Equal(t, uint64(0xffffFFFFffffFFFF), pbuf.SID())

	pbuf.SetHeader(0, 12, 34, 56, 78)
	assert.Equal(t, "56:78-12:34", pbuf.SIDStr())

	pbuf.SwapSrcDist()
	assert.Equal(t, "12:34-56:78", pbuf.SIDStr())
}

func TestPayload(t *testing.T) {
	payload := []byte("helloworld")
	pbuf := NewBuffer(nil)

	// test base payload
	pbuf.SetCmd(CmdOpenStream)
	pbuf.SetPayload(payload)
	assert.Equal(t, uint16(len(payload)), pbuf.PayloadSize())
	assert.Equal(t, uint16(0), pbuf.ACKInfo())

	// test base ack
	pbuf.SetCmd(AckPushStreamData)
	pbuf.SetACKInfo(10245)
	assert.Equal(t, uint16(0), pbuf.PayloadSize())
	assert.Equal(t, uint16(10245), pbuf.ACKInfo())

	// test SetOpenAck
	pbuf.SetCmd(CmdOpenStream)
	if pbuf.SetOpenACK("").PayloadSize() != 0 {
		t.Error("not equal")
		return
	}
	if pbuf.SetOpenACK("abcd").PayloadSize() != 4 {
		t.Error("not equal")
		return
	}

	// test payload overflow panic
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer func() {
			defer wg.Done()
			r := recover()
			if r == nil {
				t.Error("unexpected nil recover")
				return
			}
		}()

		pbuf.SetPayload(make([]byte, vars.MaxPayloadSize+1))
	}()

	wg.Wait()
}

func TestBuffer_CmdName(t *testing.T) {
	makebuf := func(cmd uint8) *Buffer {
		buf := NewBuffer(nil)
		buf.SetCmd(cmd)
		return buf
	}
	tests := []struct {
		name string
		buf  *Buffer
		want string
	}{
		{"open", makebuf(CmdOpenStream), "open"},
		{"close", makebuf(CmdCloseStream), "close"},
		{"data", makebuf(CmdPushStreamData), "data"},
		{"push", makebuf(CmdPushMessage), "push"},
		{"ping", makebuf(CmdPingDomain), "ping"},

		{"open", makebuf(AckOpenStream), "open.ack"},
		{"close", makebuf(AckCloseStream), "close.ack"},
		{"data", makebuf(AckPushStreamData), "data.ack"},
		{"push", makebuf(AckPushMessage), "push.ack"},
		{"ping", makebuf(AckPingDomain), "ping.ack"},

		{"default", makebuf(0xfe), fmt.Sprintf("<%v>", 0xfe)},
		{"default", makebuf(0xff), fmt.Sprintf("<%v>.ack", 0xfe)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.buf.CmdName(); got != tt.want {
				t.Errorf("Buffer.CmdName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBuffer_HeaderString(t *testing.T) {
	makebuf := func(cmd uint8, srcip, srcport, distip, distport uint16) *Buffer {
		buf := NewBuffer(nil)
		buf.SetCmd(cmd)
		buf.SetSrc(srcip, srcport)
		buf.SetDist(distip, distport)
		return buf
	}
	tests := []struct {
		name string
		buf  *Buffer
		want string
	}{
		{"case1", makebuf(CmdOpenStream, 1, 2, 3, 4), "[open][src=1:2][dist=3:4][size=0]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.buf.HeaderString(); got != tt.want {
				t.Errorf("Buffer.HeaderString() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestBufferSIDMethods 包含了单元测试，用于验证逻辑的正确性
func TestBufferSIDMethods(t *testing.T) {
	// 定义一个测试用例结构体
	tests := []struct {
		name        string
		head        []byte // 输入的 Head 切片
		wantSID     uint64 // SID() 的期望输出
		wantSIDPeer uint64 // SIDPeer() 的期望输出
	}{
		{
			name: "简单顺序字节",
			//         0  1  2  3  4  5  6  7  8  9 (索引)
			head: []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9},
			// SID() 应该读取 [1:9] -> [1, 2, 3, 4, 5, 6, 7, 8]
			wantSID: 0x0102030405060708,
			// SIDPeer() 应该读取 [5:9] (高位) -> [5, 6, 7, 8]
			// 和 [1:5] (低位) -> [1, 2, 3, 4]
			// 组合为 [5, 6, 7, 8, 1, 2, 3, 4]
			wantSIDPeer: 0x0506070801020304,
		},
		{
			name: "全部为零",
			//         0  1  2  3  4  5  6  7  8  9
			head: []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
			// SID() 应该读取 [0, 0, 0, 0, 0, 0, 0, 0]
			wantSID: 0x00,
			// SIDPeer() 应该读取 [0, 0, 0, 0] (高位) 和 [0, 0, 0, 0] (低位)
			wantSIDPeer: 0x00,
		},
		{
			name: "全部为0xFF",
			//         0    1    2    3    4    5    6    7    8    9
			head: []byte{0, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0},
			// SID() 应该读取 8个 0xFF
			wantSID: 0xFFFFFFFFFFFFFFFF, // ^uint64(0)
			// SIDPeer() 应该读取 4个 0xFF (高位) 和 4个 0xFF (低位)
			wantSIDPeer: 0xFFFFFFFFFFFFFFFF,
		},
		{
			name: "高低位反转",
			//         0    1    2    3    4    5    6    7    8    9
			head: []byte{0, 0xAA, 0xAA, 0xAA, 0xAA, 0xBB, 0xBB, 0xBB, 0xBB, 0},
			// SID() 应该读取 [0xAA, 0xAA, 0xAA, 0xAA, 0xBB, 0xBB, 0xBB, 0xBB]
			wantSID: 0xAAAAAAAABBBBBBBB,
			// SIDPeer() 应该读取 [0xBB, 0xBB, 0xBB, 0xBB] (高位)
			// 和 [0xAA, 0xAA, 0xAA, 0xAA] (低位)
			// 组合为 [0xBB, 0xBB, 0xBB, 0xBB, 0xAA, 0xAA, 0xAA, 0xAA]
			wantSIDPeer: 0xBBBBBBBBAAAAAAAA,
		},
	}

	// 遍历所有测试用例
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := NewBuffer(nil)
			copy(buf.Head[:], tt.head)

			// 测试 SID()
			if got := buf.SID(); got != tt.wantSID {
				// 使用 %#x 以十六进制格式打印，更易于调试
				t.Errorf("SID() = %#x, want %#x", got, tt.wantSID)
			}

			// 测试 SIDPeer()
			if got := buf.SIDPeer(); got != tt.wantSIDPeer {
				t.Errorf("SIDPeer() = %#x, want %#x", got, tt.wantSIDPeer)
			}
		})
	}
}
