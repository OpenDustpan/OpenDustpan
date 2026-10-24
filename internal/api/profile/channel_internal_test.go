package profile

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestActorChannel checks the channel of the audit entries of a profile
// change: a request that a session signed in is the console whatever its
// header says, and a token request keeps the channel that it declares.
func TestActorChannel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		session bool
		header  string
		want    audit.Channel
	}{
		{"session without a header", true, "", audit.ChannelConsole},
		{"session that declares api", true, "api", audit.ChannelConsole},
		{"token without a header", false, "", audit.ChannelAPI},
		{"token that declares the console", false, "console", audit.ChannelConsole},
		{"token with an unknown channel", false, "nonsense", audit.ChannelAPI},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodPut, "/x", http.NoBody)
			if tc.session {
				r = r.WithContext(sessionauth.WithSession(r.Context()))
			}

			if tc.header != "" {
				r.Header.Set(channelHeader, tc.header)
			}

			var p identity.Principal

			if got := actorOf(r, p).By.Channel; got != tc.want {
				t.Fatalf("channel = %q, want %q", got, tc.want)
			}
		})
	}
}
