package wire

import (
	"bytes"
	"encoding/hex"
	"errors"
	"testing"
)

func TestEncodeGoldenDataHeader(t *testing.T) {
	src := Addr{IP: 1, Port: 1000}
	dst := Addr{IP: 2, Port: 80}
	f, err := NewData(src, dst, make([]byte, 0x4000))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Release()

	var hdr [HeaderSize]byte
	if err := (Codec{}).EncodeHeader(f.Header(), hdr[:]); err != nil {
		t.Fatal(err)
	}
	const want = "0600020050000103e84000"
	if got := hex.EncodeToString(hdr[:]); got != want {
		t.Fatalf("header=%s, want %s", got, want)
	}
}

func TestEncodeGoldenControlHeader(t *testing.T) {
	src := Addr{IP: 1, Port: 1000}
	dst := Addr{IP: MaxIP, Port: 80}
	f, err := NewControl(CmdOpenStream, src, dst, make([]byte, 13))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Release()

	var hdr [HeaderSize]byte
	if err := (Codec{}).EncodeHeader(f.Header(), hdr[:]); err != nil {
		t.Fatal(err)
	}
	const want = "82ffff0050000103e8000d"
	if got := hex.EncodeToString(hdr[:]); got != want {
		t.Fatalf("header=%s, want %s", got, want)
	}
}

func TestCodecRoundTripAllCommands(t *testing.T) {
	src := Addr{IP: 7, Port: 4000}
	dst := Addr{IP: 9, Port: 443}

	for _, cmd := range AllCommands() {
		t.Run(Cmd(cmd).String(), func(t *testing.T) {
			var (
				f   *Frame
				err error
			)
			switch {
			case cmd == Cmd(CmdAdmit):
				f, err = NewHandshake([]byte("hs"))
			case cmd == Cmd(AckPushStreamData):
				f = NewAckData(1234, src, dst)
			case cmd == Cmd(CmdPushStreamData):
				f, err = NewData(src, dst, []byte("payload"))
			case noPayload(cmd):
				class, _ := ClassOf(cmd)
				f, err = NewFrame(class, cmd, src, dst, nil, 0)
			default:
				f, err = NewControl(ControlCmd(cmd), src, dst, []byte("payload"))
			}
			if err != nil {
				t.Fatal(err)
			}
			defer f.Release()

			hdr := f.Header()
			var buf [HeaderSize]byte
			if err := (Codec{}).EncodeHeader(hdr, buf[:]); err != nil {
				t.Fatal(err)
			}
			got, err := (Codec{}).DecodeHeader(buf[:])
			if err != nil {
				t.Fatal(err)
			}
			if got != hdr {
				t.Fatalf("round trip=%+v, want %+v", got, hdr)
			}
			if got.Class == ClassHandshake {
				if got.PayloadLen() != len("hs") {
					t.Fatalf("handshake payload len=%d", got.PayloadLen())
				}
			}
			if got.Cmd == Cmd(AckPushStreamData) {
				if got.PayloadLen() != 0 || got.AckSize() != 1234 {
					t.Fatalf("ack payload=%d ack=%d", got.PayloadLen(), got.AckSize())
				}
			}
		})
	}
}

func TestDecodeRejectsClassBitMismatch(t *testing.T) {
	// OpenStream is a control command; clear the class bit -> must be rejected.
	var dataClass [HeaderSize]byte
	dataClass[0] = byte(CmdOpenStream)
	if _, err := (Codec{}).DecodeHeader(dataClass[:]); !errors.Is(err, ErrClassMismatch) {
		t.Fatalf("err=%v", err)
	}

	// PushStreamData is a data command; set the class bit -> must be rejected.
	var controlClass [HeaderSize]byte
	controlClass[0] = byte(CmdPushStreamData) | classFlag
	if _, err := (Codec{}).DecodeHeader(controlClass[:]); !errors.Is(err, ErrClassMismatch) {
		t.Fatalf("err=%v", err)
	}
}

func TestDecodeRejectsUnknownCommand(t *testing.T) {
	var hdr [HeaderSize]byte
	hdr[0] = 0x10
	if _, err := (Codec{}).DecodeHeader(hdr[:]); !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("err=%v", err)
	}
}

func TestDecodeRejectsShortHeader(t *testing.T) {
	if _, err := (Codec{}).DecodeHeader(make([]byte, HeaderSize-1)); !errors.Is(err, ErrShortHeader) {
		t.Fatalf("err=%v", err)
	}
}

func TestDecodeRejectsSizeOnEmptyCommand(t *testing.T) {
	var hdr [HeaderSize]byte
	hdr[0] = byte(CmdCloseStream) | classFlag
	hdr[9] = 0
	hdr[10] = 1
	if _, err := (Codec{}).DecodeHeader(hdr[:]); !errors.Is(err, ErrPayloadNotAllowed) {
		t.Fatalf("err=%v", err)
	}
}

func TestEncodeRejectsBadHeaders(t *testing.T) {
	var buf [HeaderSize]byte
	codec := Codec{}

	if err := codec.EncodeHeader(Header{Class: ClassData, Cmd: Cmd(CmdOpenStream)}, buf[:]); !errors.Is(err, ErrClassMismatch) {
		t.Fatalf("class mismatch err=%v", err)
	}
	if err := codec.EncodeHeader(Header{Class: ClassControl, Cmd: Cmd(CmdCloseStream), Size: 1}, buf[:]); !errors.Is(err, ErrPayloadNotAllowed) {
		t.Fatalf("payload err=%v", err)
	}
	if err := codec.EncodeHeader(Header{Class: ClassControl, Cmd: Cmd(CmdOpenStream), Size: 1}, buf[:HeaderSize-1]); !errors.Is(err, ErrShortHeader) {
		t.Fatalf("short err=%v", err)
	}
}

func TestHeaderPayloadLen(t *testing.T) {
	h := Header{Cmd: Cmd(CmdOpenStream), Size: 13}
	if h.PayloadLen() != 13 || h.AckSize() != 0 {
		t.Fatalf("open payload=%d ack=%d", h.PayloadLen(), h.AckSize())
	}
	h = Header{Cmd: Cmd(AckPushStreamData), Size: 4096}
	if h.PayloadLen() != 0 || h.AckSize() != 4096 {
		t.Fatalf("ack payload=%d ack=%d", h.PayloadLen(), h.AckSize())
	}
}

func TestEncodeFrame(t *testing.T) {
	f, err := NewControl(CmdPingDomain, Addr{IP: 1, Port: 2}, Addr{IP: MaxIP}, []byte("example.com"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Release()

	var buf [HeaderSize]byte
	if err := (Codec{}).EncodeFrame(f, buf[:]); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf[:1], []byte{byte(CmdPingDomain) | classFlag}) {
		t.Fatalf("cmd byte=%x", buf[0])
	}
}
