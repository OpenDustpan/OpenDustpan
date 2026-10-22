package registry

import (
	"strings"
	"testing"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/audit"
)

// TestTruncateUserAgentKeepsRunes checks that a user agent over the limit is
// cut to at most the limit on a rune boundary, so the audit entry keeps valid
// UTF-8 for every rune width.
func TestTruncateUserAgentKeepsRunes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
	}{
		{"ascii", strings.Repeat("a", audit.MaxUserAgentLen+5)},
		{"two-byte runes", strings.Repeat("é", audit.MaxUserAgentLen)},
		{"three-byte runes", strings.Repeat("€", audit.MaxUserAgentLen)},
		{"four-byte runes", strings.Repeat("😀", audit.MaxUserAgentLen)},
		{"offset four-byte runes", "a" + strings.Repeat("😀", audit.MaxUserAgentLen)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := truncateUserAgent(tc.in)

			if len(got) > audit.MaxUserAgentLen || len(got) < audit.MaxUserAgentLen-3 {
				t.Fatalf(
					"cut to %d bytes, want between %d and %d",
					len(got),
					audit.MaxUserAgentLen-3,
					audit.MaxUserAgentLen,
				)
			}

			if !utf8.ValidString(got) || !strings.HasPrefix(tc.in, got) {
				t.Fatal("cut is not a valid UTF-8 prefix of the input")
			}
		})
	}

	if short := "curl/8.0"; truncateUserAgent(short) != short {
		t.Fatalf("truncateUserAgent(%q) changed a short user agent", short)
	}
}
