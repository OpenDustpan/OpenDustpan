package registry_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
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

// TestKindSwitchesKnowAgent proves that the audit entry template of a
// registry caller knows the kind agent, and that a kind that the server does
// not know is recorded as no kind, so a caller cannot make the audit append
// fail.
func TestKindSwitchesKnowAgent(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		kind identity.Kind
		want string
	}{
		{"a person", identity.KindPerson, audit.PrincipalKindPerson},
		{"a service", identity.KindService, audit.PrincipalKindService},
		{"a workload", identity.KindWorkload, audit.PrincipalKindWorkload},
		{"an agent", identity.KindAgent, audit.PrincipalKindAgent},
		{"an unknown kind", "robot", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", http.NoBody)

			entry := registry.CallerEntry(r, principalOfKind(tc.kind))
			if entry.PrincipalKind != tc.want {
				t.Fatalf("principal kind = %q, want %q", entry.PrincipalKind, tc.want)
			}
		})
	}
}

// principalOfKind builds a principal of one kind, for the switch under test.
func principalOfKind(kind identity.Kind) identity.Principal {
	return identity.Principal{
		Subject: "", DisplayName: "", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: kind, Credential: "", JobID: "",
	}
}
