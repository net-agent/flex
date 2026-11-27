package domain

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalize(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr error
	}{
		{name: "empty", in: "", wantErr: ErrInvalidDomain},
		{name: "only spaces", in: "   ", wantErr: ErrInvalidDomain},
		{name: "only unicode whitespace", in: "\t\r\n\u00a0", wantErr: ErrInvalidDomain},
		{name: "single char", in: "a", want: "a"},
		{name: "digits only", in: "12345", want: "12345"},
		{name: "lowercase passthrough", in: "node-01", want: "node-01"},
		{name: "uppercase is lowercased", in: "MyHost", want: "myhost"},
		{name: "mixed case with separators", in: "HOST_A-01", want: "host_a-01"},
		{name: "surrounding whitespace is trimmed", in: "  Node_A  ", want: "node_a"},
		{name: "surrounding newline is trimmed", in: "\nabc\r\n", want: "abc"},
		{name: "surrounding nbsp is trimmed", in: "\u00a0abc\u00a0", want: "abc"},
		{name: "63 bytes is allowed", in: strings.Repeat("a", 63), want: strings.Repeat("a", 63)},
		{name: "64 bytes is rejected", in: strings.Repeat("a", 64), wantErr: ErrInvalidDomain},
		{name: "uppercase 63 bytes is allowed", in: strings.Repeat("A", 63), want: strings.Repeat("a", 63)},
		{name: "byte length is checked before trimming", in: strings.Repeat("a", 63) + " ", want: strings.Repeat("a", 63)},
		{name: "leading hyphen", in: "-node", wantErr: ErrInvalidDomain},
		{name: "leading underscore", in: "_node", wantErr: ErrInvalidDomain},
		{name: "trailing hyphen", in: "node-", wantErr: ErrInvalidDomain},
		{name: "trailing underscore", in: "node_", wantErr: ErrInvalidDomain},
		{name: "single hyphen", in: "-", wantErr: ErrInvalidDomain},
		{name: "single underscore", in: "_", wantErr: ErrInvalidDomain},
		{name: "inner hyphen is allowed", in: "a-b", want: "a-b"},
		{name: "inner underscore is allowed", in: "a_b", want: "a_b"},
		{name: "repeated separators are allowed", in: "a--b__c", want: "a--b__c"},
		{name: "dot is rejected", in: "a.b", wantErr: ErrInvalidDomain},
		{name: "inner space is rejected", in: "a b", wantErr: ErrInvalidDomain},
		{name: "slash is rejected", in: "a/b", wantErr: ErrInvalidDomain},
		{name: "at sign is rejected", in: "a@b", wantErr: ErrInvalidDomain},
		{name: "colon is rejected", in: "a:b", wantErr: ErrInvalidDomain},
		{name: "nul byte is rejected", in: "a\x00b", wantErr: ErrInvalidDomain},
		{name: "invalid utf8 byte is rejected", in: "a\xffb", wantErr: ErrInvalidDomain},
		{name: "local is reserved", in: "local", wantErr: ErrInvalidDomain},
		{name: "localhost is reserved", in: "localhost", wantErr: ErrInvalidDomain},
		{name: "reserved names are case insensitive", in: "LOCALHOST", wantErr: ErrInvalidDomain},
		{name: "reserved names are trimmed first", in: "  Local  ", wantErr: ErrInvalidDomain},
		{name: "local with suffix is allowed", in: "local1", want: "local1"},
		{name: "localhost with suffix is allowed", in: "localhost2", want: "localhost2"},
		{name: "multibyte accent is rejected", in: "café", wantErr: ErrInvalidDomain},
		{name: "multibyte cjk is rejected", in: "节点", wantErr: ErrInvalidDomain},
		{name: "multibyte increases the byte length", in: strings.Repeat("a", 62) + "é", wantErr: ErrInvalidDomain},
		{name: "unicode lowercasing can yield ascii", in: "\u212a", want: "k"},
		{name: "newline injection is rejected", in: "evil\nlocalhost", wantErr: ErrInvalidDomain},
		{name: "trailing newline injection is trimmed, not executed", in: "abc\n", want: "abc"},
		{name: "carriage return injection is rejected", in: "abc\rdef", wantErr: ErrInvalidDomain},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Normalize(tt.in)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.Empty(t, got, "a rejected name must not be returned")
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, got)
		})
	}
}

// FuzzNormalize checks the two properties every caller relies on: normalizing
// an accepted name is idempotent, and an accepted name only contains the
// lowercase ASCII charset [a-z0-9_-] with a length of at most 63 bytes.
func FuzzNormalize(f *testing.F) {
	seeds := []string{
		"", "   ", "a", "node-01", "MyHost", "HOST_A-01", "  Node_A  ",
		"-node", "_node", "node-", "node_", "local", "localhost", "LOCALHOST",
		"café", "节点", "\u212a", "evil\nlocalhost", "abc\n", "abc\rdef",
		strings.Repeat("a", 63), strings.Repeat("a", 64),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, name string) {
		got, err := Normalize(name)
		if err != nil {
			require.ErrorIs(t, err, ErrInvalidDomain)
			require.Empty(t, got)
			return
		}

		require.NotEmpty(t, got)
		require.LessOrEqual(t, len(got), 63)
		require.Equal(t, strings.TrimSpace(strings.ToLower(name)), got)
		require.NotEqual(t, "local", got)
		require.NotEqual(t, "localhost", got)
		for i := 0; i < len(got); i++ {
			c := got[i]
			ok := (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_'
			require.True(t, ok, "byte %q is outside the allowed charset in %q", c, got)
		}

		again, err := Normalize(got)
		require.NoError(t, err)
		require.Equal(t, got, again, "Normalize must be idempotent")
	})
}
