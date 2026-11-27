package wire

import (
	"errors"
	"testing"
)

func TestRegistryControlDataEmpty(t *testing.T) {
	r := NewRegistry()
	ctl := func(*Frame) error { return nil }
	dat := func(*Frame) error { return nil }
	emp := func(*Frame) error { return nil }

	if err := r.RegisterControl(CmdOpenStream, LaneOrdered, ctl); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterData(CmdPushStreamData, LaneOrdered, dat); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterEmpty(AckPushStreamData, LaneControl, emp); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterEmpty(CmdCloseStream, LaneOrdered, emp); err != nil {
		t.Fatal(err)
	}
	if r.Len() != 4 {
		t.Fatalf("len=%d", r.Len())
	}

	spec, ok := r.Lookup(Cmd(CmdOpenStream))
	if !ok || spec.Class != ClassControl || spec.Lane != LaneOrdered {
		t.Fatalf("open spec=%+v ok=%v", spec, ok)
	}
	spec, ok = r.Lookup(Cmd(AckPushStreamData))
	if !ok || spec.Class != ClassData {
		t.Fatalf("ack spec=%+v ok=%v", spec, ok)
	}
	if _, ok := r.Lookup(Cmd(CmdPingDomain)); ok {
		t.Fatal("unexpected ping handler")
	}
}

func TestRegistryRejectsClassMismatch(t *testing.T) {
	r := NewRegistry()
	h := func(*Frame) error { return nil }

	if err := r.RegisterControl(ControlCmd(CmdPushStreamData), LaneControl, h); !errors.Is(err, ErrClassMismatch) {
		t.Fatalf("err=%v", err)
	}
	if err := r.RegisterData(DataCmd(CmdOpenStream), LaneControl, h); !errors.Is(err, ErrClassMismatch) {
		t.Fatalf("err=%v", err)
	}
	if err := r.RegisterEmpty(EmptyCmd(CmdAdmit), LaneControl, h); !errors.Is(err, ErrClassMismatch) {
		t.Fatalf("err=%v", err)
	}
}

func TestRegistryRejectsDuplicateNilAndBadLane(t *testing.T) {
	r := NewRegistry()
	h := func(*Frame) error { return nil }
	if err := r.RegisterControl(CmdOpenStream, LaneOrdered, h); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterControl(CmdOpenStream, LaneOrdered, h); err == nil {
		t.Fatal("expected duplicate error")
	}
	if err := r.RegisterControl(CmdPingDomain, LaneOrdered, nil); err == nil {
		t.Fatal("expected nil handler error")
	}
	if err := r.RegisterControl(CmdPingDomain, Lane(9), h); err == nil {
		t.Fatal("expected lane error")
	}
}

func TestRegistryMustPanicsOnMismatch(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	r := NewRegistry()
	r.MustRegisterControl(ControlCmd(CmdPushStreamData), LaneControl, func(*Frame) error { return nil })
}
