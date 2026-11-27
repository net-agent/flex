package framing

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/net-agent/flex/v3/wire"
	"github.com/stretchr/testify/require"
)

var (
	_ Transport     = (*TCPTransport)(nil)
	_ Transport     = (*WSTransport)(nil)
	_ PayloadReader = (*TCPTransport)(nil)
	_ PayloadReader = (*WSTransport)(nil)
)

// headerWithSize builds a header whose only meaningful field for the framing
// layer is the trailing payload-size field. The transports treat the header as
// opaque bytes; only a receiver interprets it.
func headerWithSize(size uint16) [wire.HeaderSize]byte {
	var h [wire.HeaderSize]byte
	binary.BigEndian.PutUint16(h[wire.HeaderSize-2:], size)
	return h
}

func newTCPPair(t *testing.T) (*TCPTransport, net.Conn) {
	t.Helper()

	local, peer := net.Pipe()
	tr := NewTCP(local)
	t.Cleanup(func() {
		_ = tr.Close()
		_ = peer.Close()
	})
	return tr, peer
}

// writePeer writes data from the far side of the pipe in a goroutine, because
// net.Pipe writes block until the transport consumes them.
func writePeer(peer net.Conn, data []byte) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := peer.Write(data)
		done <- err
	}()
	return done
}

// writePeerAndClose simulates a peer that disappears after a partial write.
func writePeerAndClose(peer net.Conn, data []byte) <-chan error {
	done := make(chan error, 1)
	go func() {
		_, err := peer.Write(data)
		_ = peer.Close()
		done <- err
	}()
	return done
}

func newWSPair(t *testing.T, dialer *websocket.Dialer) (*WSTransport, *websocket.Conn) {
	t.Helper()

	peerCh := make(chan *websocket.Conn, 1)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		peerCh <- conn
		<-release
		_ = conn.Close()
	}))

	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })

	if dialer == nil {
		dialer = websocket.DefaultDialer
	}
	client, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	return NewWS(client), <-peerCh
}

func TestTCPTransportReadHeader(t *testing.T) {
	header := headerWithSize(4)

	t.Run("short destination buffer is rejected before reading", func(t *testing.T) {
		for _, n := range []int{0, 1, wire.HeaderSize - 1} {
			t.Run(fmt.Sprintf("dst=%d", n), func(t *testing.T) {
				tr, peer := newTCPPair(t)
				require.ErrorIs(t, tr.ReadHeader(make([]byte, n)), io.ErrShortBuffer)

				// The rejected call must not consume the peer's bytes.
				payload := []byte("body")
				peerDone := writePeer(peer, append(header[:], payload...))
				dst := make([]byte, wire.HeaderSize)
				require.NoError(t, tr.ReadHeader(dst))
				require.Equal(t, header[:], dst)
				got, err := tr.ReadPayload(len(payload))
				require.NoError(t, err)
				require.Equal(t, payload, got)
				require.NoError(t, <-peerDone)
			})
		}
	})

	t.Run("reads exactly one header and leaves the payload", func(t *testing.T) {
		tr, peer := newTCPPair(t)
		payload := []byte("body")
		peerDone := writePeer(peer, append(header[:], payload...))

		dst := make([]byte, wire.HeaderSize+4)
		require.NoError(t, tr.ReadHeader(dst))
		require.Equal(t, header[:], dst[:wire.HeaderSize])
		require.Equal(t, make([]byte, 4), dst[wire.HeaderSize:], "bytes past HeaderSize must not be touched")

		got, err := tr.ReadPayload(len(payload))
		require.NoError(t, err)
		require.Equal(t, payload, got)
		require.NoError(t, <-peerDone)
	})

	t.Run("peer closed before the header", func(t *testing.T) {
		tr, peer := newTCPPair(t)
		require.NoError(t, peer.Close())
		require.ErrorIs(t, tr.ReadHeader(make([]byte, wire.HeaderSize)), io.EOF)
	})

	t.Run("peer closed mid header", func(t *testing.T) {
		tr, peer := newTCPPair(t)
		peerDone := writePeerAndClose(peer, header[:wire.HeaderSize-1])
		require.ErrorIs(t, tr.ReadHeader(make([]byte, wire.HeaderSize)), io.ErrUnexpectedEOF)
		require.NoError(t, <-peerDone)
	})
}

func TestTCPTransportReadPayload(t *testing.T) {
	t.Run("non-positive size returns no payload and consumes nothing", func(t *testing.T) {
		for _, n := range []int{0, -1} {
			t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
				tr, peer := newTCPPair(t)

				got, err := tr.ReadPayload(n)
				require.NoError(t, err)
				require.Nil(t, got)

				payload := []byte("body")
				peerDone := writePeer(peer, payload)
				buf := make([]byte, len(payload))
				require.NoError(t, tr.ReadPayloadInto(buf))
				require.Equal(t, payload, buf)
				require.NoError(t, <-peerDone)
			})
		}
	})

	t.Run("reads exactly the requested bytes", func(t *testing.T) {
		tr, peer := newTCPPair(t)
		peerDone := writePeer(peer, []byte("abcdefgh"))

		first, err := tr.ReadPayload(4)
		require.NoError(t, err)
		require.Equal(t, []byte("abcd"), first)

		second, err := tr.ReadPayload(4)
		require.NoError(t, err)
		require.Equal(t, []byte("efgh"), second)
		require.NoError(t, <-peerDone)
	})

	t.Run("oversized payload is rejected before reading", func(t *testing.T) {
		tr, peer := newTCPPair(t)

		got, err := tr.ReadPayload(wire.MaxPayloadSize + 1)
		require.ErrorIs(t, err, ErrFrameSizeMismatch)
		require.Nil(t, got)

		// Nothing was consumed: the connection is still framed correctly.
		peerDone := writePeer(peer, []byte("body"))
		payload, err := tr.ReadPayload(4)
		require.NoError(t, err)
		require.Equal(t, []byte("body"), payload)
		require.NoError(t, <-peerDone)
	})

	t.Run("peer closed before the payload", func(t *testing.T) {
		tr, peer := newTCPPair(t)
		require.NoError(t, peer.Close())
		_, err := tr.ReadPayload(4)
		require.ErrorIs(t, err, io.EOF)
	})

	t.Run("peer closed mid payload", func(t *testing.T) {
		tr, peer := newTCPPair(t)
		peerDone := writePeerAndClose(peer, []byte("ab"))
		_, err := tr.ReadPayload(4)
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		require.NoError(t, <-peerDone)
	})
}

func TestTCPTransportReadPayloadInto(t *testing.T) {
	t.Run("empty destination reads nothing", func(t *testing.T) {
		tr, peer := newTCPPair(t)

		require.NoError(t, tr.ReadPayloadInto(nil))
		require.NoError(t, tr.ReadPayloadInto(make([]byte, 0)))

		payload := []byte("body")
		peerDone := writePeer(peer, payload)
		buf := make([]byte, len(payload))
		require.NoError(t, tr.ReadPayloadInto(buf))
		require.Equal(t, payload, buf)
		require.NoError(t, <-peerDone)
	})

	t.Run("copies into the caller buffer", func(t *testing.T) {
		tr, peer := newTCPPair(t)
		peerDone := writePeer(peer, []byte("body"))

		buf := bytes.Repeat([]byte{0xff}, 4)
		require.NoError(t, tr.ReadPayloadInto(buf))
		require.Equal(t, []byte("body"), buf)
		require.NoError(t, <-peerDone)
	})

	t.Run("oversized destination is rejected before reading", func(t *testing.T) {
		tr, peer := newTCPPair(t)

		err := tr.ReadPayloadInto(make([]byte, wire.MaxPayloadSize+1))
		require.ErrorIs(t, err, ErrFrameSizeMismatch)

		peerDone := writePeer(peer, []byte("body"))
		buf := make([]byte, 4)
		require.NoError(t, tr.ReadPayloadInto(buf))
		require.Equal(t, []byte("body"), buf)
		require.NoError(t, <-peerDone)
	})

	t.Run("peer closed mid payload", func(t *testing.T) {
		tr, peer := newTCPPair(t)
		peerDone := writePeerAndClose(peer, []byte("ab"))
		err := tr.ReadPayloadInto(make([]byte, 4))
		require.ErrorIs(t, err, io.ErrUnexpectedEOF)
		require.NoError(t, <-peerDone)
	})
}

func TestTCPTransportWriteFrames(t *testing.T) {
	headerWithBody := headerWithSize(4)
	headerOnly := headerWithSize(0)
	headerSecond := headerWithSize(6)

	tests := []struct {
		name   string
		frames []FrameBytes
		want   []byte
	}{
		{
			name:   "single frame with payload",
			frames: []FrameBytes{{Header: headerWithBody, Payload: []byte("body")}},
			want:   append(headerWithBody[:], []byte("body")...),
		},
		{
			name:   "single header-only frame",
			frames: []FrameBytes{{Header: headerOnly}},
			want:   headerOnly[:],
		},
		{
			name: "multiple frames preserve order",
			frames: []FrameBytes{
				{Header: headerWithBody, Payload: []byte("body")},
				{Header: headerOnly},
				{Header: headerSecond, Payload: []byte("second")},
			},
			want: append(
				append(
					append(headerWithBody[:], []byte("body")...),
					headerOnly[:]...,
				),
				append(headerSecond[:], []byte("second")...)...,
			),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr, peer := newTCPPair(t)

			got := make([]byte, len(tt.want))
			peerDone := make(chan error, 1)
			go func() {
				_, err := io.ReadFull(peer, got)
				peerDone <- err
			}()

			require.NoError(t, tr.WriteFrames(tt.frames))
			require.NoError(t, <-peerDone)
			require.Equal(t, tt.want, got)
		})
	}

	t.Run("empty batch writes nothing", func(t *testing.T) {
		tr, peer := newTCPPair(t)

		require.NoError(t, tr.WriteFrames(nil))
		require.NoError(t, tr.WriteFrames([]FrameBytes{}))
		require.NoError(t, peer.SetReadDeadline(time.Unix(1, 0)))
		_, err := peer.Read(make([]byte, 1))
		require.ErrorIs(t, err, os.ErrDeadlineExceeded, "an empty batch must not write any byte")
		require.NoError(t, peer.SetReadDeadline(time.Time{}))

		// The transport is still usable after the empty batches.
		frame := FrameBytes{Header: headerWithBody, Payload: []byte("body")}
		want := append(headerWithBody[:], []byte("body")...)
		got := make([]byte, len(want))
		peerDone := make(chan error, 1)
		go func() {
			_, err := io.ReadFull(peer, got)
			peerDone <- err
		}()
		require.NoError(t, tr.WriteFrames([]FrameBytes{frame}))
		require.NoError(t, <-peerDone)
		require.Equal(t, want, got)
	})

	t.Run("write error is returned", func(t *testing.T) {
		tr, peer := newTCPPair(t)
		require.NoError(t, peer.Close())

		err := tr.WriteFrames([]FrameBytes{{Header: headerOnly}})
		require.ErrorIs(t, err, io.ErrClosedPipe)
	})

	t.Run("expired write deadline is returned", func(t *testing.T) {
		tr, peer := newTCPPair(t)
		require.NoError(t, tr.SetWriteDeadline(time.Unix(1, 0)))

		err := tr.WriteFrames([]FrameBytes{{Header: headerWithBody, Payload: []byte("body")}})
		require.ErrorIs(t, err, os.ErrDeadlineExceeded)

		// Clearing the deadline makes the transport writable again.
		require.NoError(t, tr.SetWriteDeadline(time.Time{}))
		want := append(headerWithBody[:], []byte("body")...)
		got := make([]byte, len(want))
		peerDone := make(chan error, 1)
		go func() {
			_, err := io.ReadFull(peer, got)
			peerDone <- err
		}()
		require.NoError(t, tr.WriteFrames([]FrameBytes{{Header: headerWithBody, Payload: []byte("body")}}))
		require.NoError(t, <-peerDone)
		require.Equal(t, want, got)
	})
}

func TestTCPTransportDeadlinesAndIdentity(t *testing.T) {
	tr, peer := newTCPPair(t)

	require.NoError(t, tr.SetReadDeadline(time.Unix(1, 0)))
	err := tr.ReadHeader(make([]byte, wire.HeaderSize))
	require.ErrorIs(t, err, os.ErrDeadlineExceeded)

	require.NoError(t, tr.SetReadDeadline(time.Time{}))
	header := headerWithSize(4)
	peerDone := writePeer(peer, header[:])
	dst := make([]byte, wire.HeaderSize)
	require.NoError(t, tr.ReadHeader(dst))
	require.Equal(t, header[:], dst)
	require.NoError(t, <-peerDone)

	require.NoError(t, tr.SetWriteDeadline(time.Unix(1, 0)))
	err = tr.WriteFrames([]FrameBytes{{Header: header}})
	require.ErrorIs(t, err, os.ErrDeadlineExceeded)
	require.NoError(t, tr.SetWriteDeadline(time.Time{}))

	require.Equal(t, "pipe", tr.LocalAddr().Network())
	require.Equal(t, "pipe", tr.RemoteAddr().Network())
	require.Equal(t, tr.LocalAddr().String(), tr.RemoteAddr().String())
	require.Equal(t, peer.LocalAddr().String(), tr.RemoteAddr().String())

	require.NoError(t, tr.Close())
	err = tr.ReadHeader(make([]byte, wire.HeaderSize))
	require.ErrorIs(t, err, io.ErrClosedPipe)
}

func TestWSTransportReadHeaderRejectsInvalidFrames(t *testing.T) {
	header := headerWithSize(4)
	body := []byte("body")

	tests := []struct {
		name    string
		dstLen  int
		message []byte
		msgType int
		wantErr error
	}{
		{name: "short destination buffer", dstLen: wire.HeaderSize - 1, wantErr: io.ErrShortBuffer},
		{name: "empty destination buffer", dstLen: 0, wantErr: io.ErrShortBuffer},
		{name: "short binary frame", dstLen: wire.HeaderSize, message: make([]byte, wire.HeaderSize-1), wantErr: ErrShortFrame},
		{name: "empty binary frame", dstLen: wire.HeaderSize, message: []byte{}, wantErr: ErrShortFrame},
		{name: "non-binary message", dstLen: wire.HeaderSize, message: []byte("hello"), msgType: websocket.TextMessage, wantErr: ErrBadMessageType},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr, peer := newWSPair(t, nil)

			if tt.message != nil {
				msgType := tt.msgType
				if msgType == 0 {
					msgType = websocket.BinaryMessage
				}
				require.NoError(t, peer.WriteMessage(msgType, tt.message))
			}
			require.ErrorIs(t, tr.ReadHeader(make([]byte, tt.dstLen)), tt.wantErr)

			// The rejected read must not corrupt the next frame.
			require.NoError(t, peer.WriteMessage(websocket.BinaryMessage, append(header[:], body...)))
			dst := make([]byte, wire.HeaderSize+2)
			require.NoError(t, tr.ReadHeader(dst))
			require.Equal(t, header[:], dst[:wire.HeaderSize])
			require.Equal(t, []byte{0, 0}, dst[wire.HeaderSize:], "bytes past HeaderSize must not be touched")

			got, err := tr.ReadPayload(len(body))
			require.NoError(t, err)
			require.Equal(t, body, got)
		})
	}
}

func TestWSTransportReadHeaderConsumesMessage(t *testing.T) {
	header := headerWithSize(4)
	payload := []byte("body")
	tr, peer := newWSPair(t, nil)

	require.NoError(t, peer.WriteMessage(websocket.BinaryMessage, append(header[:], payload...)))
	dst := make([]byte, wire.HeaderSize+3)
	require.NoError(t, tr.ReadHeader(dst))
	require.Equal(t, header[:], dst[:wire.HeaderSize])
	require.Equal(t, make([]byte, 3), dst[wire.HeaderSize:])

	// Calling ReadHeader again before the body was consumed is a caller bug.
	require.ErrorIs(t, tr.ReadHeader(dst), ErrFrameNotConsumed)

	// The cached payload is still available to either payload reader.
	out := make([]byte, len(payload))
	require.NoError(t, tr.ReadPayloadInto(out))
	require.Equal(t, payload, out)

	// A header-only frame also has to be drained with ReadPayload/Into before
	// the next ReadHeader.
	require.NoError(t, peer.WriteMessage(websocket.BinaryMessage, header[:]))
	require.NoError(t, tr.ReadHeader(dst))
	require.ErrorIs(t, tr.ReadHeader(dst), ErrFrameNotConsumed)
	got, err := tr.ReadPayload(0)
	require.NoError(t, err)
	require.Nil(t, got)

	require.NoError(t, peer.WriteMessage(websocket.BinaryMessage, header[:]))
	require.NoError(t, tr.ReadHeader(dst))
	require.NoError(t, tr.ReadPayloadInto(nil))
}

func TestWSTransportReadHeaderErrors(t *testing.T) {
	t.Run("connection closed", func(t *testing.T) {
		tr, _ := newWSPair(t, nil)
		require.NoError(t, tr.Close())
		require.Error(t, tr.ReadHeader(make([]byte, wire.HeaderSize)))
	})

	t.Run("read deadline", func(t *testing.T) {
		tr, _ := newWSPair(t, nil)
		require.NoError(t, tr.SetReadDeadline(time.Unix(1, 0)))

		err := tr.ReadHeader(make([]byte, wire.HeaderSize))
		require.Error(t, err)
		var netErr net.Error
		require.ErrorAs(t, err, &netErr)
		require.True(t, netErr.Timeout())
	})

	t.Run("message above the read limit", func(t *testing.T) {
		tr, peer := newWSPair(t, nil)
		require.NoError(t, peer.WriteMessage(websocket.BinaryMessage, make([]byte, wire.MaxPayloadSize+wire.HeaderSize+1)))

		err := tr.ReadHeader(make([]byte, wire.HeaderSize))
		require.ErrorIs(t, err, websocket.ErrReadLimit)
	})
}

func TestWSTransportMaxSizedFrame(t *testing.T) {
	header := headerWithSize(wire.MaxPayloadSize)
	body := bytes.Repeat([]byte{0xab}, wire.MaxPayloadSize)
	tr, peer := newWSPair(t, nil)

	require.NoError(t, peer.WriteMessage(websocket.BinaryMessage, append(header[:], body...)))
	require.NoError(t, tr.ReadHeader(make([]byte, wire.HeaderSize)))

	got, err := tr.ReadPayload(len(body))
	require.NoError(t, err)
	require.Equal(t, body, got)
}

func TestWSTransportReadPayload(t *testing.T) {
	header := headerWithSize(4)
	payload := []byte("body")

	tests := []struct {
		name       string
		message    []byte
		readHeader bool
		n          int
		want       []byte
		wantErr    error
	}{
		{name: "matching size", message: append(header[:], payload...), readHeader: true, n: 4, want: payload},
		{name: "zero size with empty body", message: header[:], readHeader: true, n: 0},
		{name: "negative size with empty body", message: header[:], readHeader: true, n: -1},
		{name: "zero size with cached body", message: append(header[:], payload...), readHeader: true, n: 0, wantErr: ErrFrameSizeMismatch},
		{name: "negative size with cached body", message: append(header[:], payload...), readHeader: true, n: -1, wantErr: ErrFrameSizeMismatch},
		{name: "declared size larger than body", message: append(header[:], []byte("ab")...), readHeader: true, n: 4, wantErr: ErrFrameSizeMismatch},
		{name: "declared size smaller than body", message: append(header[:], []byte("abcdef")...), readHeader: true, n: 4, wantErr: ErrFrameSizeMismatch},
		{name: "no header read", readHeader: false, n: 4, wantErr: ErrFrameSizeMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr, peer := newWSPair(t, nil)
			if tt.message != nil {
				require.NoError(t, peer.WriteMessage(websocket.BinaryMessage, tt.message))
			}
			if tt.readHeader {
				require.NoError(t, tr.ReadHeader(make([]byte, wire.HeaderSize)))
			}

			got, err := tr.ReadPayload(tt.n)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Nil(t, got)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestWSTransportReadPayloadInto(t *testing.T) {
	header := headerWithSize(4)
	payload := []byte("body")

	tests := []struct {
		name       string
		message    []byte
		readHeader bool
		dstLen     int
		want       []byte
		wantErr    error
	}{
		{name: "matching size", message: append(header[:], payload...), readHeader: true, dstLen: 4, want: payload},
		{name: "empty destination with empty body", message: header[:], readHeader: true, dstLen: 0, want: []byte{}},
		{name: "empty destination with cached body", message: append(header[:], payload...), readHeader: true, dstLen: 0, wantErr: ErrFrameSizeMismatch},
		{name: "destination smaller than body", message: append(header[:], payload...), readHeader: true, dstLen: 3, wantErr: ErrFrameSizeMismatch},
		{name: "destination larger than body", message: append(header[:], payload...), readHeader: true, dstLen: 5, wantErr: ErrFrameSizeMismatch},
		{name: "no header read", readHeader: false, dstLen: 4, wantErr: ErrFrameSizeMismatch},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr, peer := newWSPair(t, nil)
			if tt.message != nil {
				require.NoError(t, peer.WriteMessage(websocket.BinaryMessage, tt.message))
			}
			if tt.readHeader {
				require.NoError(t, tr.ReadHeader(make([]byte, wire.HeaderSize)))
			}

			dst := make([]byte, tt.dstLen)
			for i := range dst {
				dst[i] = 0xff
			}

			err := tr.ReadPayloadInto(dst)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Equal(t, bytes.Repeat([]byte{0xff}, tt.dstLen), dst, "a rejected read must not modify the destination")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, dst)

			// The cached body is consumed: the next header can be read.
			require.NoError(t, peer.WriteMessage(websocket.BinaryMessage, header[:]))
			require.NoError(t, tr.ReadHeader(make([]byte, wire.HeaderSize)))
		})
	}
}

func TestWSTransportWriteFrames(t *testing.T) {
	headerWithBody := headerWithSize(4)
	headerOnly := headerWithSize(0)
	headerSecond := headerWithSize(6)

	tests := []struct {
		name   string
		frames []FrameBytes
		want   [][]byte
	}{
		{
			name:   "single frame with payload",
			frames: []FrameBytes{{Header: headerWithBody, Payload: []byte("body")}},
			want:   [][]byte{append(headerWithBody[:], []byte("body")...)},
		},
		{
			name:   "single header-only frame",
			frames: []FrameBytes{{Header: headerOnly}},
			want:   [][]byte{headerOnly[:]},
		},
		{
			name: "multiple frames become separate messages",
			frames: []FrameBytes{
				{Header: headerWithBody, Payload: []byte("body")},
				{Header: headerOnly},
				{Header: headerSecond, Payload: []byte("second")},
			},
			want: [][]byte{
				append(headerWithBody[:], []byte("body")...),
				headerOnly[:],
				append(headerSecond[:], []byte("second")...),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tr, peer := newWSPair(t, nil)
			require.NoError(t, tr.WriteFrames(tt.frames))

			for i, want := range tt.want {
				mt, data, err := peer.ReadMessage()
				require.NoError(t, err, "message %d", i)
				require.Equal(t, websocket.BinaryMessage, mt)
				require.Equal(t, want, data)
			}

			require.NoError(t, peer.SetReadDeadline(time.Unix(1, 0)))
			_, _, err := peer.ReadMessage()
			require.Error(t, err, "no extra message may be queued")
		})
	}
}

func TestWSTransportWriteFramesEmpty(t *testing.T) {
	header := headerWithSize(4)
	tr, peer := newWSPair(t, nil)

	require.NoError(t, tr.WriteFrames(nil))
	require.NoError(t, tr.WriteFrames([]FrameBytes{}))

	require.NoError(t, tr.WriteFrames([]FrameBytes{{Header: header, Payload: []byte("body")}}))
	mt, data, err := peer.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, websocket.BinaryMessage, mt)
	require.Equal(t, append(header[:], []byte("body")...), data)

	require.NoError(t, peer.SetReadDeadline(time.Unix(1, 0)))
	_, _, err = peer.ReadMessage()
	require.Error(t, err, "empty batches must not queue a websocket message")
}

func TestWSTransportWriteFramesWriteErrors(t *testing.T) {
	header := headerWithSize(4)

	tests := []struct {
		name     string
		writeBuf int
		payload  []byte
	}{
		// gorilla adds the frame-header size to the configured buffer, so a
		// buffer of 1 leaves no room for the 11-byte header and forces it to
		// flush during Write.
		{name: "header write fails", writeBuf: 1},
		// A small buffer lets the header buffer but forces the payload to flush.
		{name: "payload write fails", writeBuf: 32, payload: bytes.Repeat([]byte{0xab}, 64)},
		// The default buffer holds everything, so the flush happens on Close.
		{name: "flush on close fails", writeBuf: 0, payload: []byte("body")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var dialer *websocket.Dialer
			if tt.writeBuf > 0 {
				dialer = &websocket.Dialer{WriteBufferSize: tt.writeBuf}
			}
			tr, _ := newWSPair(t, dialer)

			require.NoError(t, tr.SetWriteDeadline(time.Unix(1, 0)))
			err := tr.WriteFrames([]FrameBytes{{Header: header, Payload: tt.payload}})
			require.Error(t, err)
			var netErr net.Error
			require.ErrorAs(t, err, &netErr)
			require.True(t, netErr.Timeout(), "the expired deadline must surface as a timeout")
		})
	}
}

func TestWSTransportWriteErrorIsSticky(t *testing.T) {
	tr, _ := newWSPair(t, nil)
	frame := FrameBytes{Header: headerWithSize(0)}

	require.NoError(t, tr.SetWriteDeadline(time.Unix(1, 0)))
	require.Error(t, tr.WriteFrames([]FrameBytes{frame}))

	require.NoError(t, tr.SetWriteDeadline(time.Time{}))
	require.Error(t, tr.WriteFrames([]FrameBytes{frame}),
		"a websocket connection that failed to write must stay failed (NextWriter error)")
}

func TestWSTransportDeadlinesAndIdentity(t *testing.T) {
	tr, peer := newWSPair(t, nil)

	require.Equal(t, "tcp", tr.LocalAddr().Network())
	require.Equal(t, "tcp", tr.RemoteAddr().Network())
	require.Equal(t, peer.RemoteAddr().String(), tr.LocalAddr().String())
	require.Equal(t, peer.LocalAddr().String(), tr.RemoteAddr().String())

	require.NoError(t, tr.SetReadDeadline(time.Unix(1, 0)))
	err := tr.ReadHeader(make([]byte, wire.HeaderSize))
	require.Error(t, err)
	var netErr net.Error
	require.ErrorAs(t, err, &netErr)
	require.True(t, netErr.Timeout())

	require.NoError(t, tr.SetWriteDeadline(time.Time{}))

	require.NoError(t, tr.Close())
	require.Error(t, tr.ReadHeader(make([]byte, wire.HeaderSize)))
}
