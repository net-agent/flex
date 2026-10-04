package packet

import (
	"reflect"
	"testing"
)

func TestSubscribeRequestEncodeDecode(t *testing.T) {
	req := SubscribeRequest{Op: SubscribeAdd, Domains: []string{"node-1", "node-2", "w"}}
	decoded := DecodeSubscribeRequest(req.Encode())
	if decoded.Op != req.Op {
		t.Errorf("expected op=%v, got %v", req.Op, decoded.Op)
	}
	if !reflect.DeepEqual(decoded.Domains, req.Domains) {
		t.Errorf("expected domains=%v, got %v", req.Domains, decoded.Domains)
	}

	req = SubscribeRequest{Op: SubscribeRemove, Domains: []string{}}
	decoded = DecodeSubscribeRequest(req.Encode())
	if decoded.Op != SubscribeRemove {
		t.Errorf("expected op=%v, got %v", SubscribeRemove, decoded.Op)
	}
	if len(decoded.Domains) != 0 {
		t.Errorf("expected empty domains, got %v", decoded.Domains)
	}
}

func TestDecodeSubscribeRequestMalformed(t *testing.T) {
	// 空 payload
	if req := DecodeSubscribeRequest(nil); req.Op != SubscribeAdd || len(req.Domains) != 0 {
		t.Errorf("unexpected decode result for nil payload: %+v", req)
	}
	// 缺少 count 字节
	if req := DecodeSubscribeRequest([]byte{SubscribeAdd}); len(req.Domains) != 0 {
		t.Errorf("unexpected domains: %v", req.Domains)
	}
	// 缺少 NUL 结尾：剩余内容作为最后一个域名
	req := DecodeSubscribeRequest([]byte{SubscribeAdd, 1, 'a', 'b'})
	if len(req.Domains) != 1 || req.Domains[0] != "ab" {
		t.Errorf("unexpected domains: %v", req.Domains)
	}
	// count 大于实际域名数：容错截断
	req = DecodeSubscribeRequest([]byte{SubscribeAdd, 3, 'a', 0})
	if len(req.Domains) != 1 || req.Domains[0] != "a" {
		t.Errorf("unexpected domains: %v", req.Domains)
	}
}

func TestSubscribeACKEncodeDecode(t *testing.T) {
	ack := SubscribeACK{OK: true, States: []PresenceState{
		{Domain: "node-1", Online: true, IP: 7, Mac: "mac-1", Version: 3},
		{Domain: "node-2", Online: false, Version: 2},
	}}
	decoded := DecodeSubscribeACK(ack.Encode())
	if !decoded.OK {
		t.Fatalf("expected OK ack, got error=%v", decoded.Error)
	}
	if !reflect.DeepEqual(decoded.States, ack.States) {
		t.Errorf("expected states=%+v, got %+v", ack.States, decoded.States)
	}

	// 空快照
	ack = SubscribeACK{OK: true}
	decoded = DecodeSubscribeACK(ack.Encode())
	if !decoded.OK || len(decoded.States) != 0 {
		t.Errorf("unexpected decode result: %+v", decoded)
	}
}

func TestSubscribeACKError(t *testing.T) {
	ack := SubscribeACK{OK: false, Error: "invalid domain"}
	decoded := DecodeSubscribeACK(ack.Encode())
	if decoded.OK {
		t.Error("expected non-OK ack")
	}
	if decoded.Error != "invalid domain" {
		t.Errorf("expected error='invalid domain', got '%v'", decoded.Error)
	}

	// 空 payload 视为错误
	if decoded := DecodeSubscribeACK(nil); decoded.OK {
		t.Error("expected non-OK for empty payload")
	}
}

func TestPresenceEventEncodeDecode(t *testing.T) {
	ev := PresenceEvent{Seq: 42, Version: 5, Domain: "node-1", Online: true, IP: 7, Mac: "mac-1"}
	decoded := DecodePresenceEvent(ev.Encode())
	if !reflect.DeepEqual(decoded, ev) {
		t.Errorf("expected %+v, got %+v", ev, decoded)
	}

	ev = PresenceEvent{Seq: 0, Version: 1, Domain: "node-2", Online: false, IP: 0, Mac: ""}
	decoded = DecodePresenceEvent(ev.Encode())
	if !reflect.DeepEqual(decoded, ev) {
		t.Errorf("expected %+v, got %+v", ev, decoded)
	}
}

func TestDecodePresenceEventMalformed(t *testing.T) {
	if ev := DecodePresenceEvent(nil); ev.Domain != "" || ev.Online {
		t.Errorf("unexpected decode result for nil payload: %+v", ev)
	}
	// 缺少 mac 段：19 字节固定头 + 域名
	payload := make([]byte, 21)
	payload[0] = 1                    // online
	payload[1], payload[2] = 0, 7     // ip
	payload[10] = 5                   // version 低位
	payload[18] = 42                  // seq 低位
	payload[19], payload[20] = 'a', 0 // domain
	ev := DecodePresenceEvent(payload)
	if !ev.Online || ev.IP != 7 || ev.Version != 5 || ev.Seq != 42 || ev.Domain != "a" {
		t.Errorf("unexpected decode result: %+v", ev)
	}
}

func TestPresenceCmdName(t *testing.T) {
	buf := NewBufferWithCmd(CmdSubscribePresence)
	if buf.CmdName() != "subscribe" {
		t.Errorf("unexpected cmd name: %v", buf.CmdName())
	}
	buf.SetCmd(AckSubscribePresence)
	if buf.CmdName() != "subscribe.ack" {
		t.Errorf("unexpected cmd name: %v", buf.CmdName())
	}
	buf.SetCmd(CmdNotifyPresence)
	if buf.CmdName() != "notify" {
		t.Errorf("unexpected cmd name: %v", buf.CmdName())
	}
}
