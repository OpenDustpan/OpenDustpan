package session_test

import (
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/session"
)

// TestPasswordNeverPrints checks that no format verb shows a password, also
// not inside Credentials.
func TestPasswordNeverPrints(t *testing.T) {
	t.Parallel()

	const secret = "correct horse battery staple"

	p := session.NewPassword(secret)
	if p.Reveal() != secret {
		t.Fatalf("Reveal() = %q, want the password", p.Reveal())
	}

	c := session.Credentials{ProviderID: "ldap", UserName: "alice", Password: p}

	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		for _, v := range []any{p, c} {
			if got := fmt.Sprintf(verb, v); strings.Contains(got, secret) {
				t.Errorf("Sprintf(%q) = %q, which shows the password", verb, got)
			}
		}
	}
}
