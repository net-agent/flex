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
