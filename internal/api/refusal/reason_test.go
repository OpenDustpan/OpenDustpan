package refusal_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// missingNamespaceActions returns n missing namespace:read actions with long
// namespace names.
func missingNamespaceActions(n int) []access.HeldAction {
	out := make([]access.HeldAction, 0, n)

	for i := range n {
		name := namespace.Name(fmt.Sprintf("ns-%s-%02d", strings.Repeat("x", 60), i))
		out = append(out, access.HeldAction{
			Action: identity.ActionNamespaceRead, Object: identity.NamespaceObject(name),
		})
	}

	return out
}

// TestReasonOfKeepsTheRouteAndTheCount checks that the cap cuts the action
// list of a reach reason only: the route, the count of suppressed refusals
// and the count of the missing actions stay. A long plain reason is cut too,
// and the route stays.
func TestReasonOfKeepsTheRouteAndTheCount(t *testing.T) {
	t.Parallel()

	missing := missingNamespaceActions(20)

	tests := []struct {
		name    string
		denial  refusal.Denial
		missing []access.HeldAction
	}{
		{
			name: "a reach reason",
			denial: refusal.Denial{
				Action: identity.ActionNamespaceRead,
				Object: identity.NamespaceObject("team"),
				Reason: access.MissingActionsReason(missing),
				ReasonWithin: func(limit int) string {
					return access.MissingActionsReasonWithin(missing, limit)
				},
				Window: 0,
			},
			missing: missing,
		},
		{
			name: "a plain reason",
			denial: refusal.Denial{
				Action:       identity.ActionNamespaceRead,
				Object:       identity.NamespaceObject("team"),
				Reason:       strings.Repeat("x", 1000),
				ReasonWithin: nil,
				Window:       0,
			},
			missing: nil,
		},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkReasonOf(t, tc.denial, tc.missing)
		})
	}
}

// checkReasonOf writes the refusals of one row and checks the reason of the
// entry that carries the count of the suppressed refusals. A nil missing
// checks a plain reason, with no count of the missing actions.
func checkReasonOf(t *testing.T, denial refusal.Denial, missing []access.HeldAction) {
	t.Helper()

	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	lim := denylimit.New(denylimit.Config{
		Window: time.Minute, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0,
		Now: func() time.Time { return now },
	})
	store, failures := newFakeStore(nil), 0
	rec := newRecorder(t, store, lim, &failures)

	req, p := request(t, "/api/x"), person("bo@example.com")

	for range 3 {
		rec.Record(t.Context(), req, p, denial)
	}

	now = now.Add(time.Minute)

	rec.Record(t.Context(), req, p, denial)

	if len(store.entries) != 2 {
		t.Fatalf("wrote %d entries, want one entry and one with the count", len(store.entries))
	}

	got := store.entries[len(store.entries)-1].Reason

	if len(got) > audit.MaxReasonLen {
		t.Fatalf("reason has %d bytes, want at most %d", len(got), audit.MaxReasonLen)
	}

	for _, want := range []string{" on PUT /api/x", "; 2 further refusals since the last entry"} {
		if !strings.Contains(got, want) {
			t.Fatalf("reason = %q, want %q", got, want)
		}
	}

	if missing == nil {
		return
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
	if named+more != len(missing) {
		t.Fatalf("reason names %d actions and %d more, want %d in all", named, more, len(missing))
	}
}
