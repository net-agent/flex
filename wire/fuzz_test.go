package wire

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// FuzzDecodeHeaderRoundTrip asserts that any header accepted by the decoder can
// be re-encoded and decoded to exactly the same value. This is the property the
// link relies on for every frame; malformed bytes must never panic.
func FuzzDecodeHeaderRoundTrip(f *testing.F) {
	f.Add([]byte{0x00, 0x00, 0x50, 0x00, 0x01, 0x03, 0xe8, 0x40, 0x00, 0x00, 0x00})
	f.Add([]byte{0x82, 0xff, 0xff, 0x00, 0x50, 0x00, 0x01, 0x03, 0xe8, 0x00, 0x0d})
	f.Add([]byte{0x04, 0x00, 0x01, 0x00, 0x02, 0x00, 0x03, 0x00, 0x04, 0x00, 0x00})
	f.Add([]byte{0x05, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})

	f.Fuzz(func(t *testing.T, b []byte) {
		if len(b) < HeaderSize {
			return
		}
		h, err := (Codec{}).DecodeHeader(b[:HeaderSize])
		if err != nil {
			return
		}
		var out [HeaderSize]byte
		require.NoError(t, (Codec{}).EncodeHeader(h, out[:]))
		got, err := (Codec{}).DecodeHeader(out[:])
		require.NoError(t, err)
		require.Equal(t, h, got)
	})
}
