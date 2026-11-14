package access_test

import (
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// heldActions returns n missing actions on the namespaces ns-000, ns-001
// and so on, with a namespace name of pad extra bytes.
func heldActions(n, pad int) []access.HeldAction {
	out := make([]access.HeldAction, 0, n)

	for i := range n {
		name := namespace.Name("ns-" + strings.Repeat("x", pad) + string(rune('a'+i%26)) + string(rune('a'+i/26)))
		out = append(out, access.HeldAction{
			Action: identity.ActionNamespaceRead, Object: identity.NamespaceObject(name),
		})
	}

	return out
}

// TestMissingActionsReason checks the audit reason of a reach refusal: it
// names each missing action with its object, it names at most
// MaxReachReasonItems actions and then the count of the rest, and it stays
// inside the limit of an audit reason.
func TestMissingActionsReason(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		missing []access.HeldAction
		want    string
		suffix  string
	}{
		{
			name: "two actions",
			missing: []access.HeldAction{
				{Action: identity.ActionNamespaceRead, Object: identity.NamespaceObject("acme")},
				{Action: identity.ActionServerBackup, Object: identity.ServerObject()},
			},
			want:   "outside the reach of the caller: namespace:read on namespace:acme, server:backup on the installation",
			suffix: "",
		},
		{
			name: "an action on a principal",
			missing: []access.HeldAction{
				{Action: identity.ActionPrincipalManageTokens, Object: identity.PrincipalObject("prn-x")},
			},
			want:   "outside the reach of the caller: principal:manage-tokens on principal:prn-x",
			suffix: "",
		},
		{name: "more actions than the item bound", missing: heldActions(25, 0), want: "", suffix: " more"},
		{name: "actions longer than the reason limit", missing: heldActions(15, 60), want: "", suffix: " more"},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := access.MissingActionsReason(tc.missing)
			if len(got) > audit.MaxReasonLen {
				t.Fatalf("reason has %d bytes, want at most %d", len(got), audit.MaxReasonLen)
			}

			if tc.want != "" && got != tc.want {
				t.Fatalf("reason = %q, want %q", got, tc.want)
			}

			if !strings.HasSuffix(got, tc.suffix) {
				t.Fatalf("reason = %q, want the suffix %q", got, tc.suffix)
			}

			if n := strings.Count(got, " on namespace:"); tc.suffix != "" && n > access.MaxReachReasonItems {
				t.Fatalf("reason names %d actions, want at most %d", n, access.MaxReachReasonItems)
			}
		})
	}
}

// TestMissingActionsReasonWithin checks the audit reason of a reach refusal
// inside a small limit: it names fewer actions, grows the count of the rest,
// and stays inside the limit.
func TestMissingActionsReasonWithin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		missing []access.HeldAction
		limit   int
	}{
		{name: "one action", missing: heldActions(15, 60), limit: 200},
		{name: "two actions", missing: heldActions(25, 0), limit: 150},
		{name: "no action", missing: heldActions(25, 0), limit: 60},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := access.MissingActionsReasonWithin(tc.missing, tc.limit)
			if len(got) > tc.limit {
				t.Fatalf("reason has %d bytes, want at most %d", len(got), tc.limit)
			}

			idx := strings.LastIndex(got, ", and ")
			if idx < 0 {
				t.Fatalf("reason = %q, want the count of the rest", got)
			}

			var more int
			if _, err := fmt.Sscanf(got[idx:], ", and %d more", &more); err != nil {
				t.Fatalf("reason = %q, want the count of the rest: %v", got, err)
			}

			named := strings.Count(got[:idx], " on namespace:")
			if named+more != len(tc.missing) {
				t.Fatalf("reason names %d actions and %d more, want %d in all", named, more, len(tc.missing))
			}
		})
	}
}
