package link

import (
	"bytes"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/net-agent/flex/v3/wire"
)

func newWSPair(t *testing.T) (*websocket.Conn, *websocket.Conn) {
	t.Helper()
	upgrader := websocket.Upgrader{}
	serverCh := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		serverCh <- c
	}))
	t.Cleanup(srv.Close)

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	client, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	server := <-serverCh
	t.Cleanup(func() {
		client.Close()
		server.Close()
	})
	return client, server
}

func encodeHeader(t *testing.T, h wire.Header) [wire.HeaderSize]byte {
	t.Helper()
	var out [wire.HeaderSize]byte
	if err := (wire.Codec{}).EncodeHeader(h, out[:]); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestTCPTransportRoundTrip(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	t1, t2 := NewTCP(c1), NewTCP(c2)

	hdr := encodeHeader(t, wire.Header{
		Class: wire.ClassData,
		Cmd:   wire.Cmd(wire.CmdPushStreamData),
		Src:   wire.Addr{IP: 1, Port: 1000},
		Dst:   wire.Addr{IP: 2, Port: 80},
		Size:  3,
	})
	errc := make(chan error, 1)
	go func() {
		errc <- t1.WriteFrames([]FrameBytes{{Header: hdr, Payload: []byte("abc")}})
	}()

	var got [wire.HeaderSize]byte
	if err := t2.ReadHeader(got[:]); err != nil {
		t.Fatal(err)
	}
	p, err := t2.ReadPayload(3)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p, []byte("abc")) || got != hdr {
		t.Fatalf("header=%x payload=%q", got, p)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func TestTCPTransportShortHeader(t *testing.T) {
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	t2 := NewTCP(c2)

	go func() {
		c1.Write([]byte{1, 2, 3})
		c1.Close()
	}()
	var hdr [wire.HeaderSize]byte
	if err := t2.ReadHeader(hdr[:]); err == nil {
		t.Fatal("expected error")
	}
}

func TestWSTransportRoundTrip(t *testing.T) {
	client, server := newWSPair(t)
	ct, st := NewWS(client), NewWS(server)

	hdr := encodeHeader(t, wire.Header{
		Class: wire.ClassControl,
		Cmd:   wire.Cmd(wire.CmdPingDomain),
		Src:   wire.Addr{IP: 7, Port: 4000},
		Dst:   wire.Addr{IP: wire.MaxIP},
		Size:  4,
	})
	if err := ct.WriteFrames([]FrameBytes{{Header: hdr, Payload: []byte("ping")}}); err != nil {
		t.Fatal(err)
	}

	var got [wire.HeaderSize]byte
	if err := st.ReadHeader(got[:]); err != nil {
		t.Fatal(err)
	}
	p, err := st.ReadPayload(4)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p, []byte("ping")) || got != hdr {
		t.Fatalf("header=%x payload=%q", got, p)
	}
}

func TestWSTransportShortFrame(t *testing.T) {
	client, server := newWSPair(t)
	st := NewWS(server)
	if err := client.WriteMessage(websocket.BinaryMessage, []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	var hdr [wire.HeaderSize]byte
	if err := st.ReadHeader(hdr[:]); !errors.Is(err, ErrShortFrame) {
		t.Fatalf("err=%v", err)
	}
}

func TestWSTransportSizeMismatch(t *testing.T) {
	client, server := newWSPair(t)
	st := NewWS(server)

	hdr := encodeHeader(t, wire.Header{
		Class: wire.ClassControl,
		Cmd:   wire.Cmd(wire.CmdOpenStream),
		Size:  5,
	})
	msg := append(hdr[:], 1, 2) // header says 5, body has 2
	if err := client.WriteMessage(websocket.BinaryMessage, msg); err != nil {
		t.Fatal(err)
	}
	var got [wire.HeaderSize]byte
	if err := st.ReadHeader(got[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReadPayload(5); !errors.Is(err, ErrFrameSizeMismatch) {
		t.Fatalf("err=%v", err)
	}
}

func TestWSTransportBadMessageType(t *testing.T) {
	client, server := newWSPair(t)
	st := NewWS(server)
	if err := client.WriteMessage(websocket.TextMessage, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	var hdr [wire.HeaderSize]byte
	if err := st.ReadHeader(hdr[:]); !errors.Is(err, ErrBadMessageType) {
		t.Fatalf("err=%v", err)
	}
}

func TestWSTransportZeroPayloadThenNext(t *testing.T) {
	client, server := newWSPair(t)
	ct, st := NewWS(client), NewWS(server)

	ack := encodeHeader(t, wire.Header{
		Class: wire.ClassData,
		Cmd:   wire.Cmd(wire.AckPushStreamData),
		Size:  1234,
	})
	if err := ct.WriteFrames([]FrameBytes{{Header: ack}}); err != nil {
		t.Fatal(err)
	}
	var got [wire.HeaderSize]byte
	if err := st.ReadHeader(got[:]); err != nil {
		t.Fatal(err)
	}
	if p, err := st.ReadPayload(0); err != nil || p != nil {
		t.Fatalf("p=%v err=%v", p, err)
	}

	// A following frame must still be readable.
	ping := encodeHeader(t, wire.Header{
		Class: wire.ClassControl,
		Cmd:   wire.Cmd(wire.CmdPingDomain),
		Size:  0,
	})
	if err := ct.WriteFrames([]FrameBytes{{Header: ping}}); err != nil {
		t.Fatal(err)
	}
	if err := st.ReadHeader(got[:]); err != nil {
		t.Fatal(err)
	}
	if p, err := st.ReadPayload(0); err != nil || p != nil {
		t.Fatalf("p=%v err=%v", p, err)
	}
}

func TestWSTransportDeadlines(t *testing.T) {
	_, server := newWSPair(t)
	st := NewWS(server)
	if err := st.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var hdr [wire.HeaderSize]byte
	if err := st.ReadHeader(hdr[:]); err == nil {
		t.Fatal("expected timeout")
	}
}

func TestWSTransportFrameNotConsumed(t *testing.T) {
	client, server := newWSPair(t)
	ct, st := NewWS(client), NewWS(server)
	hdr := encodeHeader(t, wire.Header{
		Class: wire.ClassControl,
		Cmd:   wire.Cmd(wire.CmdPingDomain),
		Size:  1,
	})
	if err := ct.WriteFrames([]FrameBytes{{Header: hdr, Payload: []byte("x")}}); err != nil {
		t.Fatal(err)
	}
	var got [wire.HeaderSize]byte
	if err := st.ReadHeader(got[:]); err != nil {
		t.Fatal(err)
	}
	// Do not consume the body; the next header read must fail loudly rather
	// than silently dropping the pending bytes.
	if err := st.ReadHeader(got[:]); !errors.Is(err, ErrFrameNotConsumed) {
		t.Fatalf("err=%v", err)
	}
}
