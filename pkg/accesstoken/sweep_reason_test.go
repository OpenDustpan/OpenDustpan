package accesstoken_test

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// sweepReasonCase is one run of the inactivity sweep and the reason that its
// audit entry must hold.
type sweepReasonCase struct {
	name         string
	inactivity   time.Duration // zero keeps the default period
	account      bool          // true: the person account is swept; false: the token
	callerReason string
	wantReason   string
}

// sweepReasons returns the reasons of the audit entries that a token policy
// sweep wrote for object in the scope @server.
func sweepReasons(t *testing.T, f *fixture, object string) []string {
	t.Helper()

	listing, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list @server entries: %v", err)
	}

	var out []string

	for i := range listing.Entries {
		e := &listing.Entries[i]
		if e.Action == string(identity.ActionServerManageTokenPolicy) && e.Object == object {
			out = append(out, e.Reason)
		}
	}

	return out
}

// checkSweepReason runs one sweep under the policy of tc and checks the reason
// of the audit entry of the one record that it disables.
func checkSweepReason(t *testing.T, tc sweepReasonCase) {
	t.Helper()

	f := newFixtureAt(t, nil, sweepStart())
	ctx := t.Context()

	change := accesstoken.PolicyChange{
		MaxLifetime:       nil,
		Inactivity:        nil,
		DisableTokens:     new(!tc.account),
		DisableAccounts:   new(tc.account),
		DisableBreakGlass: nil,
		ExchangeLifetime:  nil,
	}
	if tc.inactivity != 0 {
		change.Inactivity = new(tc.inactivity)
	}

	if _, err := f.svc.SetPolicy(ctx, caller(), change); err != nil {
		t.Fatalf("set policy: %v", err)
	}

	tok, _ := f.createLong(t, f.service, "deploy")

	wantObject := accesstoken.TokenRef(f.service.ID, tok.ID)
	if tc.account {
		wantObject = principal.Ref(f.person.ID)
	}

	// The entry of the scheduled sweep, with the reason of the caller when a
	// case gives one.
	e := accesstoken.SweepEntry()
	e.Reason = tc.callerReason

	res, err := f.svc.SweepInactive(ctx, e)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	if len(res.Tokens)+len(res.Principals) != 1 {
		t.Fatalf("sweep = %+v, want one disabled record", res)
	}

	got := sweepReasons(t, &f, wantObject)
	if len(got) != 1 {
		t.Fatalf("sweep audit entries for %q = %d, want 1", wantObject, len(got))
	}

	if got[0] != tc.wantReason {
		t.Errorf("audit reason = %q, want %q", got[0], tc.wantReason)
	}
}

// TestSweepAuditReasonNamesRuleAndPeriod proves that the audit entry of each
// record that the inactivity sweep disables names the rule and the period of
// the policy that the sweep read, unless the caller gave a reason.
func TestSweepAuditReasonNamesRuleAndPeriod(t *testing.T) {
	t.Parallel()

	tests := []sweepReasonCase{
		{
			name:         "token, default period",
			inactivity:   0,
			account:      false,
			callerReason: "",
			wantReason:   "access token not used for at least 90 days (token policy: inactivity)",
		},
		{
			name:         "token, 2 hour period",
			inactivity:   2 * time.Hour,
			account:      false,
			callerReason: "",
			wantReason:   "access token not used for at least 2 hours (token policy: inactivity)",
		},
		{
			name:         "account, default period",
			inactivity:   0,
			account:      true,
			callerReason: "",
			wantReason:   "account not used for at least 90 days (token policy: inactivity)",
		},
		{
			name:         "caller reason kept",
			inactivity:   0,
			account:      false,
			callerReason: "quarterly access review",
			wantReason:   "quarterly access review",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkSweepReason(t, tc)
		})
	}
}
