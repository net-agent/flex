package wire

import (
	"errors"
	"testing"
)

func TestClassOfEveryConstant(t *testing.T) {
	control := []ControlCmd{
		CmdAdmit, CmdOpenStream, AckOpenStream,
		CmdPushMessage,
		CmdPingDomain, AckPingDomain,
		CmdSubscribePresence, AckSubscribePresence,
		CmdNotifyPresence,
	}
	for _, c := range control {
		if _, ok := ClassOf(Cmd(c)); !ok {
			t.Fatalf("control command %s not classified", Cmd(c))
		}
	}
	data := []DataCmd{CmdPushStreamData}
	for _, c := range data {
		if got, ok := ClassOf(Cmd(c)); !ok || got != ClassData {
			t.Fatalf("data command %s class=%v ok=%v", Cmd(c), got, ok)
		}
	}
	empty := []EmptyCmd{
		CmdCloseStream, AckCloseStream,
		AckPushStreamData, AckPushMessage,
	}
	for _, c := range empty {
		if _, ok := ClassOf(Cmd(c)); !ok {
			t.Fatalf("empty command %s not classified", Cmd(c))
		}
	}
	if got := len(AllCommands()); got != len(control)+len(data)+len(empty) {
		t.Fatalf("AllCommands len=%d, want %d", got, len(control)+len(data)+len(empty))
	}
}

func TestCmdFlags(t *testing.T) {
	if !Cmd(AckOpenStream).IsAck() {
		t.Fatal("AckOpenStream should be ACK")
	}
	if Cmd(CmdOpenStream).IsAck() {
		t.Fatal("CmdOpenStream should not be ACK")
	}
	if Cmd(AckOpenStream).Type() != Cmd(CmdOpenStream) {
		t.Fatalf("AckOpenStream type=%s", Cmd(AckOpenStream).Type())
	}
	if Cmd(CmdPushStreamData).Type() != Cmd(CmdPushStreamData) {
		t.Fatal("data type should be stable")
	}
	if got := Cmd(CmdOpenStream).String(); got != "open" {
		t.Fatalf("name=%q", got)
	}
	if got := Cmd(AckOpenStream).String(); got != "open.ack" {
		t.Fatalf("name=%q", got)
	}
	if got := Cmd(0x7E).String(); got != "cmd(0x7e)" {
		t.Fatalf("unknown name=%q", got)
	}
}

func TestAddrAndSID(t *testing.T) {
	a := Addr{IP: 1, Port: 1000}
	if a.String() != "1:1000" {
		t.Fatalf("addr=%q", a.String())
	}
	if !(Addr{}).IsZero() || a.IsZero() {
		t.Fatal("IsZero mismatch")
	}
	sid := SID{Src: a, Dst: Addr{IP: 2, Port: 80}}
	if sid.String() != "1:1000->2:80" {
		t.Fatalf("sid=%q", sid.String())
	}
	if SwitcherAddr.IP != MaxIP {
		t.Fatal("switcher sentinel mismatch")
	}
}

func TestClassString(t *testing.T) {
	for _, tc := range []struct {
		c    Class
		want string
	}{
		{ClassControl, "control"},
		{ClassData, "data"},
		{ClassHandshake, "handshake"},
	} {
		if got := tc.c.String(); got != tc.want {
			t.Fatalf("class %d = %q, want %q", tc.c, got, tc.want)
		}
	}
}

func TestNewFrameRejectsUnknownCommand(t *testing.T) {
	if _, err := NewFrame(ClassControl, Cmd(0x10), Addr{}, Addr{}, nil, 0); !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("err=%v", err)
	}
}

func TestNewFrameRejectsClassMismatch(t *testing.T) {
	if _, err := NewFrame(ClassData, Cmd(CmdOpenStream), Addr{}, Addr{}, []byte("x"), 0); !errors.Is(err, ErrClassMismatch) {
		t.Fatalf("err=%v", err)
	}
	if _, err := NewFrame(ClassControl, Cmd(CmdPushStreamData), Addr{}, Addr{}, []byte("x"), 0); !errors.Is(err, ErrClassMismatch) {
		t.Fatalf("err=%v", err)
	}
}

func TestNewFrameRejectsPayloadOnEmptyCommand(t *testing.T) {
	if _, err := NewFrame(ClassControl, Cmd(CmdCloseStream), Addr{}, Addr{}, []byte("x"), 0); !errors.Is(err, ErrPayloadNotAllowed) {
		t.Fatalf("err=%v", err)
	}
}

func TestNewFrameRejectsAckPayload(t *testing.T) {
	if _, err := NewFrame(ClassData, Cmd(AckPushStreamData), Addr{}, Addr{}, []byte("x"), 1); !errors.Is(err, ErrPayloadNotAllowed) {
		t.Fatalf("err=%v", err)
	}
}

func TestNewControlAdmitRejected(t *testing.T) {
	// CmdAdmit is typed ControlCmd for historical reasons, but its table class
	// is handshake; NewControl must not be able to create a normal control packet.
	if _, err := NewControl(CmdAdmit, Addr{}, Addr{}, []byte("x")); err == nil {
		t.Fatal("expected error")
	}
}
