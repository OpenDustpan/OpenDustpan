package registry_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"opendustpan.org/opendustpan/internal/api/registry"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestCallerEntryChannelAndUserAgent checks the audit entry of a registry
// write: a request that a session signed in is the console whatever its
// header says, a token request keeps the channel that it declares, and a
// user agent over the limit with a rune across the limit is cut to valid
// UTF-8.
func TestCallerEntryChannelAndUserAgent(t *testing.T) {
	t.Parallel()

	longAgent := strings.Repeat("a", audit.MaxUserAgentLen-1) + "é" + "tail"

	tests := []struct {
		name    string
		session bool
		header  string
		want    audit.Channel
	}{
		{name: "session without a header", session: true, header: "", want: audit.ChannelConsole},
		{name: "session that declares api", session: true, header: "api", want: audit.ChannelConsole},
		{name: "token without a header", session: false, header: "", want: audit.ChannelAPI},
		{name: "token that declares the console", session: false, header: "console", want: audit.ChannelConsole},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/x", http.NoBody)
			if tt.session {
				r = r.WithContext(sessionauth.WithSession(r.Context()))
			}

			if tt.header != "" {
				r.Header.Set("Dustpan-Channel", tt.header)
			}

			r.Header.Set("User-Agent", longAgent)

			var p identity.Principal

			entry := registry.CallerEntry(r, p)
			if entry.Channel != tt.want {
				t.Fatalf("channel = %q, want %q", entry.Channel, tt.want)
			}

			if len(entry.UserAgent) > audit.MaxUserAgentLen || !utf8.ValidString(entry.UserAgent) {
				t.Fatalf("user agent of %d bytes, valid UTF-8 %t; want at most %d bytes of valid UTF-8",
					len(entry.UserAgent), utf8.ValidString(entry.UserAgent), audit.MaxUserAgentLen)
			}
		})
	}
}
