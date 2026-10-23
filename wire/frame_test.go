package wire

import (
	"errors"
	"testing"
)

func TestNewControlFrame(t *testing.T) {
	src := Addr{IP: 1, Port: 1000}
	dst := Addr{IP: 2, Port: 80}
	f, err := NewControl(CmdOpenStream, src, dst, []byte("abc"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Release()

	if f.Class() != ClassControl || f.Cmd() != Cmd(CmdOpenStream) {
		t.Fatalf("frame=%s", f)
	}
	if f.Src() != src || f.Dst() != dst {
		t.Fatalf("addr=%s->%s", f.Src(), f.Dst())
	}
	if f.PayloadSize() != 3 || string(f.Payload()) != "abc" {
		t.Fatalf("payload=%q", f.Payload())
	}
	if f.SID() != (SID{Src: src, Dst: dst}) {
		t.Fatalf("sid=%s", f.SID())
	}
	if f.IsAck() {
		t.Fatal("open should not be ACK")
	}
}

func TestNewDataFrame(t *testing.T) {
	f, err := NewData(Addr{IP: 1}, Addr{IP: 2}, []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Release()
	if f.Class() != ClassData || f.Cmd() != Cmd(CmdPushStreamData) {
		t.Fatalf("frame=%s", f)
	}
}

func TestNewEmptyFrame(t *testing.T) {
	f, err := NewEmpty(CmdCloseStream, Addr{IP: 1}, Addr{IP: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer f.Release()
	if f.Class() != ClassControl || f.PayloadSize() != 0 {
		t.Fatalf("frame=%s", f)
	}

	if _, err := NewEmpty(AckPushStreamData, Addr{}, Addr{}); !errors.Is(err, ErrPayloadNotAllowed) {
		t.Fatalf("err=%v", err)
	}
}

func TestNewAckDataFrame(t *testing.T) {
	f := NewAckData(4096, Addr{IP: 1}, Addr{IP: 2})
	defer f.Release()
	if f.Class() != ClassData || f.Cmd() != Cmd(AckPushStreamData) {
		t.Fatalf("frame=%s", f)
	}
	if f.PayloadSize() != 0 || f.AckSize() != 4096 {
		t.Fatalf("size=%d ack=%d", f.PayloadSize(), f.AckSize())
	}
	if f.Header().Size != 4096 {
		t.Fatalf("header size=%d", f.Header().Size)
	}
}

func TestNewHandshakeFrame(t *testing.T) {
	f, err := NewHandshake([]byte("boot"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Release()
	if f.Class() != ClassHandshake || f.Cmd() != Cmd(CmdAdmit) {
		t.Fatalf("frame=%s", f)
	}
}

func TestNewFrameRejectsBadAckSize(t *testing.T) {
	if _, err := NewFrame(ClassControl, Cmd(CmdOpenStream), Addr{}, Addr{}, nil, 1); !errors.Is(err, ErrPayloadNotAllowed) {
		t.Fatalf("err=%v", err)
	}
	if _, err := NewFrame(ClassControl, Cmd(CmdOpenStream), Addr{}, Addr{}, []byte("x"), 1); !errors.Is(err, ErrPayloadNotAllowed) {
		t.Fatalf("err=%v", err)
	}
}

func TestNewFrameRejectsOversizePayload(t *testing.T) {
	if _, err := NewData(Addr{}, Addr{}, make([]byte, MaxPayloadSize+1)); !errors.Is(err, ErrPayloadOverflow) {
		t.Fatalf("err=%v", err)
	}
}

func TestFrameReleaseNil(t *testing.T) {
	var f *Frame
	f.Release() // must not panic
	f2, err := NewControl(CmdPingDomain, Addr{}, Addr{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f2.Release()
	f2.Release() // double release must be a no-op
}
