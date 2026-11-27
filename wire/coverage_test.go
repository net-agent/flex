package wire

import (
	"encoding/binary"
	"encoding/hex"
	"testing"

	"github.com/stretchr/testify/require"
)

// This file closes the gaps left by the original contract tests. Every test
// asserts the protocol contract (specific sentinel error, exact wire bytes,
// exact callback count), not just that a call returned.

// decodeHexHeader builds an 11-byte raw header from a hex literal. Using a
// literal instead of EncodeHeader keeps the decode tests independent of the
// encoder under test.
func decodeHexHeader(t *testing.T, s string) []byte {
	t.Helper()
	raw, err := hex.DecodeString(s)
	require.NoError(t, err, "bad test fixture %q", s)
	require.Len(t, raw, HeaderSize, "fixture %q must be one header", s)
	return raw
}

// recoverPanicError runs fn, requires that it panicked with an error, and
// returns that error so the caller can assert which contract was violated.
func recoverPanicError(t *testing.T, fn func()) error {
	t.Helper()
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		fn()
	}()
	require.NotNil(t, recovered, "expected a panic")
	err, ok := recovered.(error)
	require.True(t, ok, "panic value has type %T, want error", recovered)
	return err
}

// payloadReleaseSpy records how often a payload release callback ran and which
// buffer it received. The link uses this callback to return receive buffers to
// its per-link pool, so "exactly once" is the contract that prevents leaks and
// double puts.
type payloadReleaseSpy struct {
	calls   int
	payload []byte
}

func (s *payloadReleaseSpy) release(p []byte) {
	s.calls++
	s.payload = p
}

// TestWireConstantsContract pins the wire constants that both peers depend on.
func TestWireConstantsContract(t *testing.T) {
	require.Equal(t, 11, HeaderSize)
	require.Equal(t, 0xFFFF, MaxPayloadSize)
	require.Equal(t, 0xFFFF, MaxIP)
	require.Equal(t, uint16(MaxIP), SwitcherAddr.IP)
	require.True(t, SwitcherAddr.Port == 0)
	require.True(t, LocalAddr.IsZero())
}

// TestClassStringUnknownValues covers the diagnostic fallback: an unexpected
// class must still render its numeric value in logs.
func TestClassStringUnknownValues(t *testing.T) {
	tests := []struct {
		class Class
		want  string
	}{
		{Class(0), "class(0)"},
		{Class(4), "class(4)"},
		{Class(255), "class(255)"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			require.Equal(t, tc.want, tc.class.String())
		})
	}
}

// TestHeaderStringContract pins the log format of Header, including unknown
// class/command fallbacks.
func TestHeaderStringContract(t *testing.T) {
	tests := []struct {
		name string
		h    Header
		want string
	}{
		{
			name: "control",
			h: Header{
				Class: ClassControl,
				Cmd:   Cmd(CmdOpenStream),
				Src:   Addr{IP: 1, Port: 1000},
				Dst:   Addr{IP: 2, Port: 80},
				Size:  13,
			},
			want: "[control][open][1:1000->2:80][size=13]",
		},
		{
			name: "ack data",
			h: Header{
				Class: ClassData,
				Cmd:   Cmd(AckPushStreamData),
				Src:   Addr{IP: 7, Port: 4000},
				Dst:   Addr{IP: 9, Port: 443},
				Size:  MaxPayloadSize,
			},
			want: "[data][data.ack][7:4000->9:443][size=65535]",
		},
		{
			name: "handshake",
			h: Header{
				Class: ClassHandshake,
				Cmd:   Cmd(CmdAdmit),
				Dst:   SwitcherAddr,
			},
			want: "[handshake][admit][0:0->65535:0][size=0]",
		},
		{
			name: "unknown class and command",
			h: Header{
				Class: Class(7),
				Cmd:   Cmd(0x10),
				Src:   Addr{IP: 3, Port: 4},
				Dst:   Addr{IP: 5, Port: 6},
				Size:  7,
			},
			want: "[class(7)][cmd(0x10)][3:4->5:6][size=7]",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.h.String())
		})
	}
}

// TestCommandTableNamesAndClasses locks down command names, class membership,
// and declaration order. AllCommands is documented as declaration order.
func TestCommandTableNamesAndClasses(t *testing.T) {
	tests := []struct {
		cmd   Cmd
		class Class
		name  string
	}{
		{Cmd(CmdAdmit), ClassHandshake, "admit"},
		{Cmd(CmdOpenStream), ClassControl, "open"},
		{Cmd(AckOpenStream), ClassControl, "open.ack"},
		{Cmd(CmdCloseStream), ClassControl, "close"},
		{Cmd(AckCloseStream), ClassControl, "close.ack"},
		{Cmd(CmdPushStreamData), ClassData, "data"},
		{Cmd(AckPushStreamData), ClassData, "data.ack"},
		{Cmd(CmdPushMessage), ClassControl, "message"},
		{Cmd(AckPushMessage), ClassControl, "message.ack"},
		{Cmd(CmdPingDomain), ClassControl, "ping"},
		{Cmd(AckPingDomain), ClassControl, "ping.ack"},
		{Cmd(CmdSubscribePresence), ClassControl, "subscribe"},
		{Cmd(AckSubscribePresence), ClassControl, "subscribe.ack"},
		{Cmd(CmdNotifyPresence), ClassControl, "notify"},
	}

	all := AllCommands()
	require.Len(t, all, len(tests))
	for i, tc := range tests {
		require.Equal(t, tc.cmd, all[i], "AllCommands declaration order")
		class, ok := ClassOf(tc.cmd)
		require.True(t, ok, "%s must be registered", tc.cmd)
		require.Equal(t, tc.class, class, "%s class", tc.cmd)
		require.Equal(t, tc.name, tc.cmd.String(), "%s name", tc.cmd)
	}
}

// TestCommandClassPredicates covers both class predicates for data, control,
// handshake (excluded from both), and unregistered commands.
func TestCommandClassPredicates(t *testing.T) {
	tests := []struct {
		cmd         Cmd
		wantClass   Class
		wantData    bool
		wantControl bool
	}{
		{Cmd(CmdPushStreamData), ClassData, true, false},
		{Cmd(AckPushStreamData), ClassData, true, false},
		{Cmd(CmdOpenStream), ClassControl, false, true},
		{Cmd(AckOpenStream), ClassControl, false, true},
		// Empty-typed commands still carry the table class: Close is control,
		// AckPushStreamData is data.
		{Cmd(AckCloseStream), ClassControl, false, true},
		// Handshake is intentionally neither routable data nor control.
		{Cmd(CmdAdmit), ClassHandshake, false, false},
		{Cmd(0x10), 0, false, false},
		{Cmd(0x90), 0, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.cmd.String(), func(t *testing.T) {
			class, ok := ClassOf(tc.cmd)
			require.Equal(t, tc.wantClass != 0, ok, "ClassOf registration")
			require.Equal(t, tc.wantClass, class)
			require.Equal(t, tc.wantData, IsDataCommand(tc.cmd))
			require.Equal(t, tc.wantControl, IsControlCommand(tc.cmd))
		})
	}
}

// TestSIDUint64MatchesPackedBigEndianHeader verifies the historical packed SID
// layout: dst ip/port followed by src ip/port, big endian, exactly the eight
// bytes packet.Buffer.SID() reads from Head[1:9]. These must agree or legacy
// packet.Buffer peers and wire.Frame peers would address different streams.
func TestSIDUint64MatchesPackedBigEndianHeader(t *testing.T) {
	tests := []struct {
		name string
		sid  SID
		want uint64
	}{
		{"zero", SID{}, 0},
		{"all max", SID{
			Src: Addr{IP: MaxIP, Port: MaxIP},
			Dst: Addr{IP: MaxIP, Port: MaxIP},
		}, 0xFFFFFFFFFFFFFFFF},
		{"distinct fields", SID{
			Src: Addr{IP: 0x0506, Port: 0x0708},
			Dst: Addr{IP: 0x0102, Port: 0x0304},
		}, 0x0102030405060708},
		{"dst ip in high bits", SID{Dst: Addr{IP: MaxIP}}, 0xFFFF000000000000},
		{"dst port", SID{Dst: Addr{Port: MaxIP}}, 0x0000FFFF00000000},
		{"src ip", SID{Src: Addr{IP: MaxIP}}, 0x00000000FFFF0000},
		{"src port in low bits", SID{Src: Addr{Port: MaxIP}}, 0x000000000000FFFF},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, tc.sid.Uint64())

			// Independent check against the encoded header bytes: the header
			// stores dst then src at offsets 1..9, which is exactly what
			// packet.Buffer.SID() packs into a uint64.
			var raw [HeaderSize]byte
			require.NoError(t, (Codec{}).EncodeHeader(Header{
				Class: ClassData,
				Cmd:   Cmd(CmdPushStreamData),
				Src:   tc.sid.Src,
				Dst:   tc.sid.Dst,
			}, raw[:]))
			require.Equal(t, binary.BigEndian.Uint64(raw[1:9]), tc.sid.Uint64())
		})
	}
}

// TestEncodersRejectInvalidHeaders drives every validateHeader failure through
// both the modern and legacy encoders. A failed encode must leave dst untouched.
func TestEncodersRejectInvalidHeaders(t *testing.T) {
	encoders := []struct {
		name   string
		encode func(Header, []byte) error
	}{
		{"EncodeHeader", Codec{}.EncodeHeader},
		{"EncodeHeaderLegacy", Codec{}.EncodeHeaderLegacy},
	}
	tests := []struct {
		name   string
		h      Header
		target error
		msg    string
	}{
		{
			name:   "invalid class zero",
			h:      Header{Class: Class(0), Cmd: Cmd(CmdOpenStream)},
			target: ErrInvalidClass,
			msg:    "invalid class",
		},
		{
			name:   "invalid class four",
			h:      Header{Class: Class(4), Cmd: Cmd(CmdPushStreamData)},
			target: ErrInvalidClass,
			msg:    "invalid class",
		},
		{
			name:   "class bit set in cmd",
			h:      Header{Class: ClassControl, Cmd: Cmd(0x82)},
			target: ErrUnknownCommand,
			msg:    "class bit set in cmd 0x82",
		},
		{
			name:   "unknown command",
			h:      Header{Class: ClassControl, Cmd: Cmd(0x10)},
			target: ErrUnknownCommand,
			msg:    "0x10",
		},
		{
			name:   "class mismatch control command in data class",
			h:      Header{Class: ClassData, Cmd: Cmd(CmdOpenStream)},
			target: ErrClassMismatch,
			msg:    "want=control",
		},
		{
			name:   "class mismatch handshake command in control class",
			h:      Header{Class: ClassControl, Cmd: Cmd(CmdAdmit)},
			target: ErrClassMismatch,
			msg:    "want=handshake",
		},
		{
			name:   "payload on no-payload close",
			h:      Header{Class: ClassControl, Cmd: Cmd(CmdCloseStream), Size: 1},
			target: ErrPayloadNotAllowed,
			msg:    "size=1",
		},
		{
			name:   "payload on no-payload message ack",
			h:      Header{Class: ClassControl, Cmd: Cmd(AckPushMessage), Size: 1},
			target: ErrPayloadNotAllowed,
			msg:    "size=1",
		},
	}
	for _, enc := range encoders {
		for _, tc := range tests {
			t.Run(enc.name+"/"+tc.name, func(t *testing.T) {
				var dst [HeaderSize]byte
				err := enc.encode(tc.h, dst[:])
				require.ErrorIs(t, err, tc.target)
				require.ErrorContains(t, err, tc.msg)
				require.Equal(t, make([]byte, HeaderSize), dst[:], "failed encode must not write a partial header")
			})
		}
	}
}

// TestEncodeHeaderLegacyStripsClassBit verifies the migration encoder omits bit
// 7 while keeping the exact header layout, and that the output is still readable
// by DecodeHeaderLegacy.
func TestEncodeHeaderLegacyStripsClassBit(t *testing.T) {
	tests := []struct {
		name    string
		h       Header
		wantHex string
	}{
		{
			name: "control",
			h: Header{
				Class: ClassControl, Cmd: Cmd(CmdOpenStream),
				Src: Addr{IP: 1, Port: 1000}, Dst: Addr{IP: 2, Port: 80}, Size: 13,
			},
			wantHex: "0200020050000103e8000d",
		},
		{
			name: "control ack with zero size",
			h: Header{
				Class: ClassControl, Cmd: Cmd(AckOpenStream),
				Src: Addr{IP: 1, Port: 1000}, Dst: Addr{IP: 2, Port: 80},
			},
			wantHex: "0300020050000103e80000",
		},
		{
			name: "data",
			h: Header{
				Class: ClassData, Cmd: Cmd(CmdPushStreamData),
				Src: Addr{IP: 1, Port: 1000}, Dst: Addr{IP: 2, Port: 80}, Size: 0x4000,
			},
			wantHex: "0600020050000103e84000",
		},
		{
			name: "data ack with max ack size",
			h: Header{
				Class: ClassData, Cmd: Cmd(AckPushStreamData),
				Src: Addr{IP: 1, Port: 1000}, Dst: Addr{IP: 2, Port: 80}, Size: MaxPayloadSize,
			},
			wantHex: "0700020050000103e8ffff",
		},
		{
			name: "handshake",
			h: Header{
				Class: ClassHandshake, Cmd: Cmd(CmdAdmit), Size: 2,
			},
			wantHex: "0000000000000000000002",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Extra tail bytes let us prove the encoder writes exactly one
			// header and stays inside HeaderSize.
			dst := make([]byte, HeaderSize+4)
			for i := range dst {
				dst[i] = 0xAA
			}
			require.NoError(t, (Codec{}).EncodeHeaderLegacy(tc.h, dst))
			require.Equal(t, tc.wantHex, hex.EncodeToString(dst[:HeaderSize]))
			require.Equal(t, []byte{0xAA, 0xAA, 0xAA, 0xAA}, dst[HeaderSize:])

			// The legacy bytes differ from the modern encoding only by the
			// class bit.
			var modern [HeaderSize]byte
			require.NoError(t, (Codec{}).EncodeHeader(tc.h, modern[:]))
			wantCmd := dst[0]
			if tc.h.Class.encrypted() {
				wantCmd |= classFlag
			}
			require.Equal(t, wantCmd, modern[0])
			require.Equal(t, dst[1:HeaderSize], modern[1:HeaderSize])

			// And the legacy output is accepted by the migration decoder.
			got, err := (Codec{}).DecodeHeaderLegacy(dst[:HeaderSize])
			require.NoError(t, err)
			require.Equal(t, tc.h, got)
		})
	}
}

// TestDecodeHeaderLegacyAcceptsBothBitFormats covers the migration contract:
// both the old (no class bit) and new (class bit) encodings of the same command
// must decode to the same Header, with the class taken from the command table.
func TestDecodeHeaderLegacyAcceptsBothBitFormats(t *testing.T) {
	tests := []struct {
		name string
		wire string
		want Header
	}{
		{
			name: "control with class bit",
			wire: "8200020050000103e8000d",
			want: Header{
				Class: ClassControl, Cmd: Cmd(CmdOpenStream),
				Src: Addr{IP: 1, Port: 1000}, Dst: Addr{IP: 2, Port: 80}, Size: 13,
			},
		},
		{
			name: "control without class bit",
			wire: "0200020050000103e8000d",
			want: Header{
				Class: ClassControl, Cmd: Cmd(CmdOpenStream),
				Src: Addr{IP: 1, Port: 1000}, Dst: Addr{IP: 2, Port: 80}, Size: 13,
			},
		},
		{
			name: "data",
			wire: "0600020050000103e84000",
			want: Header{
				Class: ClassData, Cmd: Cmd(CmdPushStreamData),
				Src: Addr{IP: 1, Port: 1000}, Dst: Addr{IP: 2, Port: 80}, Size: 0x4000,
			},
		},
		{
			name: "handshake with class bit",
			wire: "8000000000000000000002",
			want: Header{Class: ClassHandshake, Cmd: Cmd(CmdAdmit), Size: 2},
		},
		{
			name: "handshake legacy without class bit",
			wire: "0000000000000000000002",
			want: Header{Class: ClassHandshake, Cmd: Cmd(CmdAdmit), Size: 2},
		},
		{
			name: "ack data max size",
			wire: "0700020050000103e8ffff",
			want: Header{
				Class: ClassData, Cmd: Cmd(AckPushStreamData),
				Src: Addr{IP: 1, Port: 1000}, Dst: Addr{IP: 2, Port: 80}, Size: MaxPayloadSize,
			},
		},
		{
			name: "control ack zero size",
			wire: "0300020050000103e80000",
			want: Header{
				Class: ClassControl, Cmd: Cmd(AckOpenStream),
				Src: Addr{IP: 1, Port: 1000}, Dst: Addr{IP: 2, Port: 80},
			},
		},
		{
			name: "zero-size data frame",
			wire: "0600020050000103e80000",
			want: Header{
				Class: ClassData, Cmd: Cmd(CmdPushStreamData),
				Src: Addr{IP: 1, Port: 1000}, Dst: Addr{IP: 2, Port: 80},
			},
		},
		{
			name: "all max fields",
			wire: "06ffffffffffffffffffff",
			want: Header{
				Class: ClassData, Cmd: Cmd(CmdPushStreamData),
				Src: Addr{IP: MaxIP, Port: MaxIP}, Dst: Addr{IP: MaxIP, Port: MaxIP}, Size: MaxPayloadSize,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := (Codec{}).DecodeHeaderLegacy(decodeHexHeader(t, tc.wire))
			require.NoError(t, err)
			require.Equal(t, tc.want, got)

			wantPayloadLen := int(tc.want.Size)
			wantAckSize := uint16(0)
			if tc.want.Cmd == Cmd(AckPushStreamData) {
				wantPayloadLen = 0
				wantAckSize = tc.want.Size
			}
			require.Equal(t, wantPayloadLen, got.PayloadLen())
			require.Equal(t, wantAckSize, got.AckSize())
		})
	}
}

// TestDecodeHeaderLegacyRejectsMalformed covers every rejection branch:
// truncation, unknown command, data command with the encryption bit, and a
// non-zero size on a no-payload command. AckPushStreamData is the one exception
// that may carry a size (the acknowledged byte count).
func TestDecodeHeaderLegacyRejectsMalformed(t *testing.T) {
	_, err := (Codec{}).DecodeHeaderLegacy(nil)
	require.ErrorIs(t, err, ErrShortHeader)
	_, err = (Codec{}).DecodeHeaderLegacy(make([]byte, HeaderSize-1))
	require.ErrorIs(t, err, ErrShortHeader)

	tests := []struct {
		name   string
		wire   string
		target error
		msg    string
	}{
		{
			name:   "unknown command",
			wire:   "1000020050000103e8000d",
			target: ErrUnknownCommand,
			msg:    "0x10",
		},
		{
			name:   "unknown command with class bit",
			wire:   "9000020050000103e8000d",
			target: ErrUnknownCommand,
			msg:    "0x10",
		},
		{
			name:   "data command with class bit",
			wire:   "8600020050000103e84000",
			target: ErrClassMismatch,
			msg:    "has class bit",
		},
		{
			name:   "ack data with class bit",
			wire:   "8700020050000103e8ffff",
			target: ErrClassMismatch,
			msg:    "has class bit",
		},
		{
			name:   "close with size",
			wire:   "0400020050000103e80001",
			target: ErrPayloadNotAllowed,
			msg:    "size=1",
		},
		{
			name:   "message ack with size",
			wire:   "0900020050000103e80001",
			target: ErrPayloadNotAllowed,
			msg:    "size=1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := (Codec{}).DecodeHeaderLegacy(decodeHexHeader(t, tc.wire))
			require.ErrorIs(t, err, tc.target)
			require.ErrorContains(t, err, tc.msg)
		})
	}
}

// TestPayloadAndAckSizeBoundaries pins the uint16 edges of the wire format.
func TestPayloadAndAckSizeBoundaries(t *testing.T) {
	src := Addr{IP: 1, Port: 1000}
	dst := Addr{IP: 2, Port: 80}

	t.Run("payload exactly MaxPayloadSize", func(t *testing.T) {
		payload := make([]byte, MaxPayloadSize)
		f, err := NewData(src, dst, payload)
		require.NoError(t, err)
		defer f.Release()

		require.Equal(t, uint16(MaxPayloadSize), f.PayloadSize())
		require.Equal(t, uint16(MaxPayloadSize), f.Header().Size)

		var raw [HeaderSize]byte
		require.NoError(t, (Codec{}).EncodeHeader(f.Header(), raw[:]))
		require.Equal(t, []byte{0xFF, 0xFF}, raw[9:11])

		got, err := (Codec{}).DecodeHeader(raw[:])
		require.NoError(t, err)
		require.Equal(t, uint16(MaxPayloadSize), got.Size)
		require.Equal(t, MaxPayloadSize, got.PayloadLen())
	})

	t.Run("payload one over MaxPayloadSize", func(t *testing.T) {
		_, err := NewData(src, dst, make([]byte, MaxPayloadSize+1))
		require.ErrorIs(t, err, ErrPayloadOverflow)
		require.ErrorContains(t, err, "65536")
	})

	t.Run("ack size exactly 0xFFFF", func(t *testing.T) {
		f := NewAckData(MaxPayloadSize, src, dst)
		defer f.Release()

		require.Equal(t, uint16(MaxPayloadSize), f.AckSize())
		require.Equal(t, uint16(MaxPayloadSize), f.Header().Size)
		require.Equal(t, uint16(0), f.PayloadSize())

		var raw [HeaderSize]byte
		require.NoError(t, (Codec{}).EncodeHeader(f.Header(), raw[:]))
		require.Equal(t, []byte{0xFF, 0xFF}, raw[9:11])

		got, err := (Codec{}).DecodeHeader(raw[:])
		require.NoError(t, err)
		require.Equal(t, 0, got.PayloadLen())
		require.Equal(t, uint16(MaxPayloadSize), got.AckSize())
	})

	t.Run("zero-size data frame", func(t *testing.T) {
		f, err := NewData(src, dst, nil)
		require.NoError(t, err)
		defer f.Release()

		require.Equal(t, uint16(0), f.PayloadSize())
		require.Equal(t, uint16(0), f.Header().Size)

		var raw [HeaderSize]byte
		require.NoError(t, (Codec{}).EncodeHeader(f.Header(), raw[:]))
		got, err := (Codec{}).DecodeHeader(raw[:])
		require.NoError(t, err)
		require.Equal(t, uint16(0), got.Size)
		require.Equal(t, 0, got.PayloadLen())
	})

	t.Run("zero-size no-payload command", func(t *testing.T) {
		f, err := NewEmpty(AckCloseStream, src, dst)
		require.NoError(t, err)
		defer f.Release()

		require.Equal(t, uint16(0), f.Header().Size)
		var raw [HeaderSize]byte
		require.NoError(t, (Codec{}).EncodeHeader(f.Header(), raw[:]))
		got, err := (Codec{}).DecodeHeader(raw[:])
		require.NoError(t, err)
		require.Equal(t, uint16(0), got.Size)
	})

	t.Run("ack data size zero via NewFrame", func(t *testing.T) {
		f, err := NewFrame(ClassData, Cmd(AckPushStreamData), src, dst, nil, 0)
		require.NoError(t, err)
		defer f.Release()
		require.Equal(t, uint16(0), f.AckSize())
	})
}

// TestCodecRejectsTruncatedBuffers checks all four codec entry points with
// buffers shorter than the fixed header.
func TestCodecRejectsTruncatedBuffers(t *testing.T) {
	h := Header{
		Class: ClassData, Cmd: Cmd(CmdPushStreamData),
		Src: Addr{IP: 1, Port: 1000}, Dst: Addr{IP: 2, Port: 80}, Size: 1,
	}
	for _, size := range []int{0, HeaderSize - 1} {
		t.Run(hex.EncodeToString([]byte{byte(size)}), func(t *testing.T) {
			buf := make([]byte, size)
			require.ErrorIs(t, (Codec{}).EncodeHeader(h, buf), ErrShortHeader)
			require.ErrorIs(t, (Codec{}).EncodeHeaderLegacy(h, buf), ErrShortHeader)

			_, err := (Codec{}).DecodeHeader(buf)
			require.ErrorIs(t, err, ErrShortHeader)
			_, err = (Codec{}).DecodeHeaderLegacy(buf)
			require.ErrorIs(t, err, ErrShortHeader)
		})
	}
}

// TestNewFrameWithReleaseInvokesCallbackOnce verifies payload ownership
// transfer: the callback fires exactly once, gets the original buffer, and a
// second Release is a no-op that must not double-return the buffer.
func TestNewFrameWithReleaseInvokesCallbackOnce(t *testing.T) {
	src := Addr{IP: 1, Port: 1000}
	dst := Addr{IP: 2, Port: 80}
	payload := []byte("owned-by-frame")
	spy := &payloadReleaseSpy{}

	f, err := NewFrameWithRelease(ClassData, Cmd(CmdPushStreamData), src, dst, payload, 0, spy.release)
	require.NoError(t, err)
	require.NotNil(t, f)

	// Ownership transfers at construction; the callback must not have run yet.
	require.Equal(t, 0, spy.calls)
	require.Equal(t, ClassData, f.Class())
	require.Equal(t, Cmd(CmdPushStreamData), f.Cmd())
	require.Equal(t, src, f.Src())
	require.Equal(t, dst, f.Dst())
	require.Equal(t, payload, f.Payload())
	require.Equal(t, &payload[0], &f.Payload()[0], "frame must take the caller's buffer, not a copy")

	f.Release()
	require.Equal(t, 1, spy.calls)
	require.Equal(t, payload, spy.payload)

	// White-box check of the pool-reset invariant. This is deliberately not a
	// sanctioned frame use; it proves the pooled object cannot leak stale state.
	require.False(t, f.pooled)
	require.Equal(t, Class(0), f.class)
	require.Equal(t, Cmd(0), f.cmd)
	require.Equal(t, Addr{}, f.src)
	require.Equal(t, Addr{}, f.dst)
	require.Equal(t, uint16(0), f.ackSize)
	require.Nil(t, f.payload)
	require.Nil(t, f.release)

	f.Release()
	require.Equal(t, 1, spy.calls, "Release must be idempotent")
}

// TestNewFrameWithReleaseNilPayload verifies the callback is still invoked for a
// nil payload, so a link's buffer accounting stays balanced.
func TestNewFrameWithReleaseNilPayload(t *testing.T) {
	spy := &payloadReleaseSpy{}
	f, err := NewFrameWithRelease(ClassControl, Cmd(CmdPingDomain), Addr{}, Addr{}, nil, 0, spy.release)
	require.NoError(t, err)
	f.Release()
	require.Equal(t, 1, spy.calls)
	require.Nil(t, spy.payload)
}

// TestNewFrameWithReleaseAcceptsMaxPayload verifies the inclusive uint16 bound
// also holds on the release-callback path.
func TestNewFrameWithReleaseAcceptsMaxPayload(t *testing.T) {
	payload := make([]byte, MaxPayloadSize)
	spy := &payloadReleaseSpy{}
	f, err := NewFrameWithRelease(ClassData, Cmd(CmdPushStreamData), Addr{}, Addr{}, payload, 0, spy.release)
	require.NoError(t, err)
	require.Equal(t, uint16(MaxPayloadSize), f.PayloadSize())
	f.Release()
	require.Equal(t, 1, spy.calls)
}

// TestNewFrameWithReleaseReturnsPayloadOnError verifies the failed-construction
// contract: the constructor takes ownership, so every rejected frame must hand
// the payload back exactly once (the caller cannot free it) and must not return
// a frame.
func TestNewFrameWithReleaseReturnsPayloadOnError(t *testing.T) {
	tests := []struct {
		name    string
		class   Class
		cmd     Cmd
		payload []byte
		ackSize uint16
		target  error
		msg     string
	}{
		{
			name: "invalid class", class: Class(0), cmd: Cmd(CmdOpenStream),
			payload: []byte("x"), target: ErrInvalidClass, msg: "invalid class",
		},
		{
			name: "class bit set in cmd", class: ClassControl, cmd: Cmd(0x82),
			payload: []byte("x"), target: ErrUnknownCommand, msg: "class bit set",
		},
		{
			name: "unknown command", class: ClassControl, cmd: Cmd(0x10),
			target: ErrUnknownCommand, msg: "0x10",
		},
		{
			name: "class mismatch", class: ClassData, cmd: Cmd(CmdOpenStream),
			payload: []byte("x"), target: ErrClassMismatch, msg: "want=control",
		},
		{
			name: "payload overflow", class: ClassData, cmd: Cmd(CmdPushStreamData),
			payload: make([]byte, MaxPayloadSize+1), target: ErrPayloadOverflow, msg: "65536",
		},
		{
			name: "ack command with payload", class: ClassData, cmd: Cmd(AckPushStreamData),
			payload: []byte("x"), ackSize: 1, target: ErrPayloadNotAllowed, msg: "data.ack",
		},
		{
			name: "ack size on non-ack command", class: ClassControl, cmd: Cmd(CmdOpenStream),
			ackSize: 1, target: ErrPayloadNotAllowed, msg: "ack size on non-ack command",
		},
		{
			name: "ack size on payload command", class: ClassControl, cmd: Cmd(CmdOpenStream),
			payload: []byte("x"), ackSize: 1, target: ErrPayloadNotAllowed, msg: "ack size on payload command",
		},
		{
			name: "payload on no-payload command", class: ClassControl, cmd: Cmd(CmdCloseStream),
			payload: []byte("x"), target: ErrPayloadNotAllowed, msg: "close",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			spy := &payloadReleaseSpy{}
			f, err := NewFrameWithRelease(tc.class, tc.cmd, Addr{}, Addr{}, tc.payload, tc.ackSize, spy.release)
			require.Nil(t, f, "rejected construction must not return a frame")
			require.ErrorIs(t, err, tc.target)
			require.ErrorContains(t, err, tc.msg)
			require.Equal(t, 1, spy.calls, "failed construction must return the payload exactly once")
			require.Equal(t, tc.payload, spy.payload)
		})
	}

	t.Run("nil release must not panic", func(t *testing.T) {
		f, err := NewFrameWithRelease(ClassControl, Cmd(0x10), Addr{}, Addr{}, nil, 0, nil)
		require.Nil(t, f)
		require.ErrorIs(t, err, ErrUnknownCommand)
	})
}

// TestReleaseWithoutCallbackResetsFrame covers the no-callback release path:
// the payload is resliced for pool reuse without invoking anything.
func TestReleaseWithoutCallbackResetsFrame(t *testing.T) {
	payload := make([]byte, 4, 16)
	copy(payload, "data")
	f, err := NewData(Addr{IP: 1, Port: 2}, Addr{IP: 3, Port: 4}, payload)
	require.NoError(t, err)

	f.Release()
	require.False(t, f.pooled)
	require.Nil(t, f.release)
	require.NotNil(t, f.payload)
	require.Len(t, f.payload, 0)
	require.Equal(t, 16, cap(f.payload), "capacity is retained for pool reuse")
	require.Equal(t, Class(0), f.class)
	require.Equal(t, Cmd(0), f.cmd)

	// Second release is a no-op; it must not invoke anything or repool.
	f.Release()
	require.False(t, f.pooled)
	require.Len(t, f.payload, 0)
}

// TestFrameReleaseIgnoresNonPooledFrame verifies that Release is nil-safe and
// only mutates pool-owned frames, so a zero-value Frame cannot corrupt state.
func TestFrameReleaseIgnoresNonPooledFrame(t *testing.T) {
	var nilFrame *Frame
	require.NotPanics(t, func() { nilFrame.Release() })

	payload := []byte("keep")
	literal := &Frame{class: ClassData, cmd: Cmd(CmdPushStreamData), payload: payload}
	literal.Release()
	require.False(t, literal.pooled)
	require.Equal(t, payload, literal.payload, "non-pooled frame must not be reset")
	require.Equal(t, ClassData, literal.class)
}

// TestFrameAckSizeOnlyForAckData pins AckSize's zero value for ordinary frames
// and covers both Frame.String formats (ack count vs payload size).
func TestFrameAckSizeOnlyForAckData(t *testing.T) {
	src := Addr{IP: 1, Port: 1000}
	dst := Addr{IP: 2, Port: 80}

	dataFrame, err := NewData(src, dst, []byte("abc"))
	require.NoError(t, err)
	defer dataFrame.Release()
	require.Equal(t, uint16(0), dataFrame.AckSize())
	require.Equal(t, "[data][data][1:1000->2:80][size=3]", dataFrame.String())

	controlFrame, err := NewControl(CmdPingDomain, src, dst, []byte("example.com"))
	require.NoError(t, err)
	defer controlFrame.Release()
	require.Equal(t, uint16(0), controlFrame.AckSize())
	require.Equal(t, "[control][ping][1:1000->2:80][size=11]", controlFrame.String())

	ackFrame := NewAckData(4096, src, dst)
	defer ackFrame.Release()
	require.Equal(t, uint16(4096), ackFrame.AckSize())
	require.Equal(t, "[data][data.ack][1:1000->2:80][ack=4096]", ackFrame.String())

	emptyFrame, err := NewEmpty(CmdCloseStream, src, dst)
	require.NoError(t, err)
	defer emptyFrame.Release()
	require.Equal(t, uint16(0), emptyFrame.AckSize())
	require.Equal(t, "[control][close][1:1000->2:80][size=0]", emptyFrame.String())
}

// TestNewEmptyRejectsNonRoutableCommands covers every NewEmpty validation
// branch: unknown bytes, the AckPushStreamData special case, and the handshake
// command, which is only valid on the bootstrap codec.
func TestNewEmptyRejectsNonRoutableCommands(t *testing.T) {
	tests := []struct {
		name   string
		cmd    EmptyCmd
		target error
		msg    string
	}{
		{
			name:   "unknown command",
			cmd:    EmptyCmd(0x10),
			target: ErrUnknownCommand,
			msg:    "0x10",
		},
		{
			name:   "unknown command with class bit",
			cmd:    EmptyCmd(0x90),
			target: ErrUnknownCommand,
			msg:    "0x90",
		},
		{
			name:   "handshake is not routable",
			cmd:    EmptyCmd(CmdAdmit),
			target: ErrClassMismatch,
			msg:    "not routable",
		},
		{
			name:   "ack data requires NewAckData",
			cmd:    EmptyCmd(AckPushStreamData),
			target: ErrPayloadNotAllowed,
			msg:    "use NewAckData",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f, err := NewEmpty(tc.cmd, Addr{}, Addr{})
			require.Nil(t, f)
			require.ErrorIs(t, err, tc.target)
			require.ErrorContains(t, err, tc.msg)
		})
	}

	t.Run("valid control and data empties", func(t *testing.T) {
		f, err := NewEmpty(AckPushMessage, Addr{}, Addr{})
		require.NoError(t, err)
		require.Equal(t, ClassControl, f.Class())
		f.Release()

		ack, err := NewEmpty(AckPushStreamData, Addr{}, Addr{})
		require.ErrorIs(t, err, ErrPayloadNotAllowed)
		require.Nil(t, ack)
	})
}

// TestNewAckDataPanicsWhenCommandTableIsBroken covers the defensive panic in
// NewAckData. The command/class pair is static, so the only way to reach it is
// an inconsistent command table; the constructor deliberately turns that into a
// loud failure instead of a silently wrong packet. The table is restored before
// asserting normal construction again.
func TestNewAckDataPanicsWhenCommandTableIsBroken(t *testing.T) {
	cmd := Cmd(AckPushStreamData)
	saved, registered := commandClass[cmd]
	require.True(t, registered, "AckPushStreamData must be in the command table")

	delete(commandClass, cmd)
	restored := false
	restore := func() {
		if !restored {
			commandClass[cmd] = saved
			restored = true
		}
	}
	defer restore()

	err := recoverPanicError(t, func() { NewAckData(1, Addr{}, Addr{}) })
	require.ErrorIs(t, err, ErrUnknownCommand)
	require.ErrorContains(t, err, "0x07")

	restore()
	f := NewAckData(1, Addr{}, Addr{})
	defer f.Release()
	require.Equal(t, cmd, f.Cmd())
	require.Equal(t, uint16(1), f.AckSize())
}

// TestLaneString covers both lanes and the diagnostic fallback.
func TestLaneString(t *testing.T) {
	tests := []struct {
		lane Lane
		want string
	}{
		{LaneControl, "control"},
		{LaneOrdered, "ordered"},
		{Lane(7), "lane(7)"},
	}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			require.Equal(t, tc.want, tc.lane.String())
		})
	}
}

// TestRegistryMustRegisterDataAndEmpty exercises the Must* data/empty helpers on
// the success path and verifies the stored spec dispatches to the handler.
func TestRegistryMustRegisterDataAndEmpty(t *testing.T) {
	r := NewRegistry()
	require.Empty(t, r.Commands())
	require.Equal(t, 0, r.Len())

	var dispatched []Cmd
	handler := func(f *Frame) error {
		dispatched = append(dispatched, f.Cmd())
		return nil
	}
	r.MustRegisterControl(CmdOpenStream, LaneControl, handler)
	r.MustRegisterData(CmdPushStreamData, LaneOrdered, handler)
	r.MustRegisterEmpty(AckCloseStream, LaneOrdered, handler)

	require.Equal(t, 3, r.Len())
	require.ElementsMatch(t,
		[]Cmd{Cmd(CmdOpenStream), Cmd(CmdPushStreamData), Cmd(AckCloseStream)},
		r.Commands(),
	)

	spec, ok := r.Lookup(Cmd(CmdPushStreamData))
	require.True(t, ok)
	require.Equal(t, ClassData, spec.Class)
	require.Equal(t, LaneOrdered, spec.Lane)

	spec, ok = r.Lookup(Cmd(AckCloseStream))
	require.True(t, ok)
	require.Equal(t, ClassControl, spec.Class)
	require.Equal(t, LaneOrdered, spec.Lane)

	f, err := NewData(Addr{IP: 1}, Addr{IP: 2}, []byte("x"))
	require.NoError(t, err)
	defer f.Release()
	spec, ok = r.Lookup(Cmd(CmdPushStreamData))
	require.True(t, ok)
	require.NoError(t, spec.Handler(f))
	require.Equal(t, []Cmd{Cmd(CmdPushStreamData)}, dispatched)
}

// TestRegistryRegisterErrorsAreSpecific asserts the exact diagnostics for nil
// handlers, bad lanes, unknown commands, and duplicate registration, and that a
// failed registration never mutates the registry.
func TestRegistryRegisterErrorsAreSpecific(t *testing.T) {
	r := NewRegistry()
	h := func(*Frame) error { return nil }

	err := r.RegisterControl(CmdOpenStream, LaneControl, nil)
	require.EqualError(t, err, "wire: nil handler for open")

	err = r.RegisterControl(CmdPingDomain, Lane(9), h)
	require.EqualError(t, err, "wire: invalid lane 9 for ping")

	err = r.RegisterControl(ControlCmd(0x10), LaneControl, h)
	require.ErrorIs(t, err, ErrUnknownCommand)
	require.ErrorContains(t, err, "0x10")

	err = r.RegisterData(DataCmd(0x10), LaneControl, h)
	require.ErrorIs(t, err, ErrUnknownCommand)
	require.ErrorContains(t, err, "0x10")

	err = r.RegisterEmpty(EmptyCmd(0x10), LaneControl, h)
	require.ErrorIs(t, err, ErrUnknownCommand)
	require.ErrorContains(t, err, "0x10")

	require.NoError(t, r.RegisterControl(CmdOpenStream, LaneOrdered, h))
	err = r.RegisterControl(CmdOpenStream, LaneOrdered, h)
	require.EqualError(t, err, "wire: duplicate handler for open")

	require.Equal(t, 1, r.Len(), "failed registrations must not take effect")
	_, ok := r.Lookup(Cmd(CmdPingDomain))
	require.False(t, ok)
}

// TestRegistryMustRegisterPanics verifies the Must* wrappers convert every
// registration error (including nil handlers and duplicates) into a panic, as
// they are only meant for static startup registration.
func TestRegistryMustRegisterPanics(t *testing.T) {
	h := func(*Frame) error { return nil }
	tests := []struct {
		name   string
		target error
		msg    string
		fn     func(*Registry)
	}{
		{
			name:   "data class mismatch",
			target: ErrClassMismatch,
			msg:    "want=data",
			fn: func(r *Registry) {
				r.MustRegisterData(DataCmd(CmdOpenStream), LaneControl, h)
			},
		},
		{
			name:   "empty unknown command",
			target: ErrUnknownCommand,
			msg:    "0x10",
			fn: func(r *Registry) {
				r.MustRegisterEmpty(EmptyCmd(0x10), LaneControl, h)
			},
		},
		{
			name:   "empty handshake command",
			target: ErrClassMismatch,
			msg:    "not routable",
			fn: func(r *Registry) {
				r.MustRegisterEmpty(EmptyCmd(CmdAdmit), LaneControl, h)
			},
		},
		{
			name: "empty nil handler",
			msg:  "nil handler",
			fn: func(r *Registry) {
				r.MustRegisterEmpty(AckCloseStream, LaneControl, nil)
			},
		},
		{
			name: "data duplicate",
			msg:  "duplicate handler",
			fn: func(r *Registry) {
				r.MustRegisterData(CmdPushStreamData, LaneOrdered, h)
				r.MustRegisterData(CmdPushStreamData, LaneOrdered, h)
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := recoverPanicError(t, func() { tc.fn(NewRegistry()) })
			if tc.target != nil {
				require.ErrorIs(t, err, tc.target)
			}
			require.ErrorContains(t, err, tc.msg)
		})
	}
}
