package packet

import (
	"bytes"
	"sync"
	"testing"
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

		if buf.Cmd() != c.cmd {
			t.Error("cmd not equal")
			return
		}
		if buf.DistIP() != c.distIP {
			t.Error("dist ip not equal")
			return
		}
		if buf.DistPort() != c.distPort {
			t.Error("dist port not equal")
			return
		}
		if buf.SrcIP() != c.srcIP {
			t.Error("src ip not equal")
			return
		}
		if buf.SrcPort() != c.srcPort {
			t.Error("src port not equal")
			return
		}
		if !bytes.Equal(buf.Payload, c.payload) {
			t.Error("payload not equal")
			return
		}
	}
}

func TestIsACK(t *testing.T) {
	pbuf := NewBuffer(nil)
	pbuf.SetCmd(CmdOpenStream | CmdACKFlag)
	if !pbuf.IsACK() {
		t.Error("not equal")
		return
	}
	pbuf.SetCmd(CmdOpenStream)
	if pbuf.IsACK() {
		t.Error("not equal")
		return
	}
}

func TestSID(t *testing.T) {
	pbuf := NewBuffer(nil)
	pbuf.SetHeader(0, 0, 0, 0, 0)
	if pbuf.SID() != 0 {
		t.Error("not equal")
		return
	}
	pbuf.SetHeader(0, 0xffff, 0xffff, 0xffff, 0xffff)
	if pbuf.SID() != 0xffffFFFFffffFFFF {
		t.Error("not equal")
		return
	}
	pbuf.SetHeader(0, 12, 34, 56, 78)
	sidstr := pbuf.SIDStr()
	if sidstr != "56:78-12:34" {
		t.Error("not equal", sidstr)
		return
	}
	pbuf.SwapSrcDist()
	if pbuf.SIDStr() != "12:34-56:78" {
		t.Error("not euqal")
		return
	}
}

func TestPayload(t *testing.T) {
	payload := []byte("helloworld")
	pbuf := NewBuffer(nil)

	// test base payload
	pbuf.SetCmd(CmdOpenStream)
	pbuf.SetPayload(payload)
	if pbuf.PayloadSize() != uint16(len(payload)) {
		t.Error("not equal")
		return
	}
	if pbuf.ACKInfo() != 0 {
		t.Error("not equal")
		return
	}

	// test base ack
	pbuf.SetCmd(CmdPushStreamData | CmdACKFlag)
	pbuf.SetACKInfo(10245)
	if pbuf.PayloadSize() != 0 {
		t.Error("not equal")
		return
	}
	if pbuf.ACKInfo() != 10245 {
		t.Error("not equal")
		return
	}

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

		pbuf.SetPayload(make([]byte, 0xfffff))
	}()

	wg.Wait()
}
