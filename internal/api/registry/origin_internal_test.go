package registry

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// errUnknown is an error that no class of this API knows.
var errUnknown = errors.New("unknown")

// TestCallerChannel checks the channel of the audit entries of a registry
// change: a request that a session signed in is the console whatever its
// header says, and a token request keeps the channel that it declares.
func TestCallerChannel(t *testing.T) {
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

			if got := callerOf(r, p).By.Channel; got != tc.want {
				t.Fatalf("channel = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestEveryClassHasACode checks that each error that this API classifies
// answers with a valid stable code, and that an error it does not know
// passes to the shared classes.
func TestEveryClassHasACode(t *testing.T) {
	t.Parallel()

	for _, rp := range registryProblems {
		p, ok := classifyRegistry(fmt.Errorf("wrapped: %w", rp.err))
		if !ok || !problem.ValidCode(p.Code) || p.Status != rp.status {
			t.Errorf("%v: classified %t with status %d and code %q, want %d and a valid code",
				rp.err, ok, p.Status, p.Code, rp.status)
		}
	}

	if _, ok := classifyRegistry(errUnknown); ok {
		t.Error("an unknown error was classified, want it passed to the shared classes")
	}
}
