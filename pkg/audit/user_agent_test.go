package audit_test

import (
	"strings"
	"testing"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/audit"
)

// TestTruncateUserAgentKeepsRunes checks that a user agent over the limit is
// cut to at most the limit on a rune boundary, so the audit entry keeps valid
// UTF-8 for every rune width, and that a short user agent stays as it is.
func TestTruncateUserAgentKeepsRunes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
	}{
		{name: "ascii", in: strings.Repeat("a", audit.MaxUserAgentLen+5)},
		{name: "two-byte runes", in: strings.Repeat("é", audit.MaxUserAgentLen)},
		{name: "three-byte runes", in: strings.Repeat("€", audit.MaxUserAgentLen)},
		{name: "four-byte runes", in: strings.Repeat("\U0001F600", audit.MaxUserAgentLen)},
		{name: "offset four-byte runes", in: "a" + strings.Repeat("\U0001F600", audit.MaxUserAgentLen)},
		{
			name: "two-byte rune across the limit",
			in:   strings.Repeat("a", audit.MaxUserAgentLen-1) + "é" + "tail",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := audit.TruncateUserAgent(tt.in)

			if len(got) > audit.MaxUserAgentLen || len(got) < audit.MaxUserAgentLen-3 {
				t.Fatalf("cut to %d bytes, want between %d and %d",
					len(got), audit.MaxUserAgentLen-3, audit.MaxUserAgentLen)
			}

			if !utf8.ValidString(got) || !strings.HasPrefix(tt.in, got) {
				t.Fatal("cut is not a valid UTF-8 prefix of the input")
			}
		})
	}

	if short := "curl/8.0"; audit.TruncateUserAgent(short) != short {
		t.Fatalf("TruncateUserAgent(%q) changed a short user agent", short)
	}
}
