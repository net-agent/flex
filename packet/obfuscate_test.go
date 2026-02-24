package packet

import (
	"bytes"
	"testing"
)

func TestObfuscateHeaderSymmetry(t *testing.T) {
	s := NewObfuscateState([]byte("test-key"))
	mask := s.MaskForRead()

	var original Header
	for i := range original {
		original[i] = byte(i * 17)
	}
	head := original

	ObfuscateHeader(&head, &mask)
	if bytes.Equal(head[:9], original[:9]) {
		t.Error("header[0:9] should be changed after XOR")
	}
	if head[9] != original[9] || head[10] != original[10] {
		t.Error("header[9:11] should not be changed")
	}

	ObfuscateHeader(&head, &mask)
	if head != original {
		t.Error("double XOR should restore original header")
	}
}

func TestObfuscateStateCounters(t *testing.T) {
	s := NewObfuscateState([]byte("test-key"))

	s.writeMu.Lock()
	m0 := s.maskForWrite()
	m1 := s.maskForWrite()
	s.writeMu.Unlock()
	if m0 == m1 {
		t.Error("consecutive writes should produce different masks")
	}

	r0 := s.MaskForRead()
	r1 := s.MaskForRead()
	if r0 == r1 {
		t.Error("consecutive reads should produce different masks")
	}

	// 读写计数器独立：counter=0 的 mask 应相同
	s2 := NewObfuscateState([]byte("test-key"))
	s2.writeMu.Lock()
	w := s2.maskForWrite()
	s2.writeMu.Unlock()
	r := s2.MaskForRead()
	if w != r {
		t.Error("write(0) and read(0) should produce same mask for same key")
	}
}

func TestObfuscatedConnRoundTrip(t *testing.T) {
	key := []byte("shared-secret-key")
	c1, c2 := Pipe()

	oc1 := NewObfuscatedConn(c1, key)
	oc2 := NewObfuscatedConn(c2, key)

	go func() {
		buf := NewBufferWithCmd(CmdPushStreamData)
		buf.SetDist(100, 200)
		buf.SetSrc(300, 400)
		buf.SetPayload([]byte("hello"))
		oc1.WriteBuffer(buf)

		if buf.Cmd() != CmdPushStreamData {
			panic("header not restored after write")
		}
	}()

	buf, err := oc2.ReadBuffer()
	if err != nil {
		t.Fatalf("ReadBuffer failed: %v", err)
	}

	if buf.Cmd() != CmdPushStreamData {
		t.Errorf("cmd mismatch: got %v", buf.Cmd())
	}
	if buf.DistIP() != 100 || buf.DistPort() != 200 {
		t.Errorf("dist mismatch: %v:%v", buf.DistIP(), buf.DistPort())
	}
	if buf.SrcIP() != 300 || buf.SrcPort() != 400 {
		t.Errorf("src mismatch: %v:%v", buf.SrcIP(), buf.SrcPort())
	}
	if !bytes.Equal(buf.Payload, []byte("hello")) {
		t.Errorf("payload mismatch: %s", buf.Payload)
	}
}

func TestObfuscatedConnMultiplePackets(t *testing.T) {
	key := []byte("key")
	c1, c2 := Pipe()
	oc1 := NewObfuscatedConn(c1, key)
	oc2 := NewObfuscatedConn(c2, key)

	const n = 10
	go func() {
		for i := range n {
			buf := NewBufferWithCmd(CmdPushStreamData)
			buf.SetSrcPort(uint16(i))
			oc1.WriteBuffer(buf)
		}
	}()

	for i := range n {
		buf, err := oc2.ReadBuffer()
		if err != nil {
			t.Fatalf("packet %d: ReadBuffer failed: %v", i, err)
		}
		if buf.SrcPort() != uint16(i) {
			t.Errorf("packet %d: expected SrcPort=%d, got %d", i, i, buf.SrcPort())
		}
	}
}
