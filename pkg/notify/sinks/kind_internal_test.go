package sinks

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// TestKindSwitchesKnowAgent proves that the audit kind of a principal knows
// the kind agent, and that a kind that the server does not know is recorded
// as no kind, so an audit append never fails for the kind of a caller.
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

			if got := auditKind(principalOfKind(tc.kind)); got != tc.want {
				t.Fatalf("auditKind(%q) = %q, want %q", tc.kind, got, tc.want)
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
