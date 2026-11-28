package accesstoken_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/freshstep"
	"opendustpan.org/opendustpan/pkg/identity"
)

// policyConfig returns the config of a token service over the store of f
// with the fresh step guard g.
func policyConfig(f *fixture, g freshstep.Guard) accesstoken.Config {
	return accesstoken.Config{
		Makers:     nil,
		TempAdmins: nil,
		Store:      f.store, Principals: f.store, Audit: f.store, Guard: g, Authorizer: nil, Reach: access.ReachConfig{
			Grants: nil, Roles: nil, Callers: nil, Principals: nil, Collections: nil, Units: nil,
		},
		Logger: nil, Now: f.clock.Now, NewID: nil,
	}
}

// newPolicyService makes a token service over the store of f with the fresh
// step guard g.
func newPolicyService(t *testing.T, f *fixture, g freshstep.Guard) *accesstoken.Service {
	t.Helper()

	svc, err := accesstoken.NewService(policyConfig(f, g))
	if err != nil {
		t.Fatalf("token service: %v", err)
	}

	return svc
}

func TestFreshPolicyGatesTheStep(t *testing.T) {
	t.Parallel()

	off := freshstep.PolicyFunc(func(context.Context) bool { return false })
	on := freshstep.PolicyFunc(func(context.Context) bool { return true })

	cases := []struct {
		name    string
		policy  freshstep.Policy
		wantErr error
	}{
		{name: "setting off passes", policy: off, wantErr: nil},
		{name: "setting on refuses", policy: on, wantErr: errStale},
		{name: "no policy refuses", policy: freshstep.PolicyFunc(nil), wantErr: errStale},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)
			svc := newPolicyService(t, &f, *freshGuard(t, tc.policy))

			// Create with the service principal, as an administrator.
			_, _, err := svc.Create(t.Context(), admin(), caller(), accesstoken.ScopeService,
				accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: f.service.ID, Name: "x"})
			checkFreshErr(t, "create", err, tc.wantErr)

			tok, _ := f.create(t, f.service, "seed")

			_, _, err = svc.Rotate(t.Context(), admin(), caller(), accesstoken.RotateInput{
				OldEndsAt: nil, ExpiresAt: time.Time{}, ID: tok.ID, Scope: accesstoken.ScopeService,
			})
			checkFreshErr(t, "rotate", err, tc.wantErr)
		})
	}
}

// checkFreshErr fails the test when err of the operation op is not want. A
// nil want means no error.
func checkFreshErr(t *testing.T, op string, err, want error) {
	t.Helper()

	if want == nil && err != nil {
		t.Fatalf("%s: err = %v, want no error", op, err)
	}

	if want != nil && !errors.Is(err, want) {
		t.Fatalf("%s: err = %v, want the freshness error %v", op, err, want)
	}
}

// NewGuard refuses a nil policy, so a service never holds a guard that skips
// the step for want of a policy.
func TestFreshGuardRefusesNilPolicy(t *testing.T) {
	t.Parallel()

	_, err := freshstep.NewGuard(nil, staleMFA{}.RequireFresh)
	if !errors.Is(err, freshstep.ErrNilPolicy) {
		t.Fatalf("new guard: err = %v, want ErrNilPolicy", err)
	}
}

// NewService refuses a zero guard: a service never runs without the fresh
// step guard.
func TestNewServiceRefusesZeroFreshGuard(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)

	svc, err := accesstoken.NewService(policyConfig(&f, freshstep.Guard{}))
	if !errors.Is(err, accesstoken.ErrMissingDependency) {
		t.Fatalf("new service: err = %v, want ErrMissingDependency", err)
	}

	if svc != nil {
		t.Fatal("new service: got a service, want nil")
	}
}

// NewService refuses a nil audit store: a refused change must be recorded.
func TestNewServiceRefusesNilAudit(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)

	cfg := policyConfig(&f, *freshGuard(t, freshstep.PolicyFunc(nil)))
	cfg.Audit = nil

	svc, err := accesstoken.NewService(cfg)
	if !errors.Is(err, accesstoken.ErrMissingDependency) {
		t.Fatalf("new service: err = %v, want ErrMissingDependency", err)
	}

	if svc != nil {
		t.Fatal("new service: got a service, want nil")
	}
}

// errAuditDown is the write error of refusingAudit.
var errAuditDown = errors.New("audit store is down")

// refusingAudit is an audit store whose appends all fail.
type refusingAudit struct {
	*sqlite.Store
}

// AppendServer fails every append, so the denial entry cannot be written.
func (refusingAudit) AppendServer(context.Context, audit.Entry) (audit.Entry, error) {
	return audit.Entry{}, errAuditDown
}

// serverEntries returns the entries of the server scope of st.
func serverEntries(t *testing.T, st *sqlite.Store) []audit.Entry {
	t.Helper()

	listing, err := st.List(t.Context(), audit.ServerScope, audit.Page{Size: 0, Before: 0})
	if err != nil {
		t.Fatalf("list server entries: %v", err)
	}

	return listing.Entries
}

// countOutcome returns the number of entries of es with the outcome o.
func countOutcome(es []audit.Entry, o audit.Outcome) int {
	n := 0

	for i := range es {
		if es[i].Outcome == o {
			n++
		}
	}

	return n
}

// checkDenied fails the test when the refused operation op does not return
// the freshness error, or when auditErr is set and the error does not wrap it.
func checkDenied(t *testing.T, op string, err, auditErr error) {
	t.Helper()

	checkFreshErr(t, op, err, errStale)

	if auditErr != nil && !errors.Is(err, auditErr) {
		t.Fatalf("%s: err = %v, want the audit error %v", op, err, auditErr)
	}
}

// denialWant is what checkDenials expects in the server scope: the objects of
// the create and of the rotation, the success entries before the refusals and
// the number of denial entries.
type denialWant struct {
	service   string
	tok       string
	successes int
	entries   int
}

// checkDenials fails the test unless the server scope of st holds the denial
// entries of want, and no change that committed after the seed. The entries
// are not in the order of the changes, so they are matched by object.
func checkDenials(t *testing.T, st *sqlite.Store, want denialWant) {
	t.Helper()

	all := serverEntries(t, st)
	if got := countOutcome(all, audit.OutcomeSuccess); got != want.successes {
		t.Fatalf("success entries = %d, want %d: a refused change must not commit", got, want.successes)
	}

	var denials []audit.Entry

	for i := range all {
		if all[i].Outcome == audit.OutcomeDenied {
			denials = append(denials, all[i])
		}
	}

	if len(denials) != want.entries {
		t.Fatalf("denial entries = %d, want %d", len(denials), want.entries)
	}

	wantObjects := map[string]bool{want.service: true, want.tok: true}
	for i := range denials {
		e := denials[i]
		if e.Kind != audit.KindChange || e.Namespace != audit.ServerScope ||
			e.Subject != caller().Subject || e.Action != string(identity.ActionServerManageTokens) ||
			!wantObjects[e.Object] {
			t.Fatalf("denial entry = %+v, want a denied change of the server scope by %q", e, caller().Subject)
		}

		delete(wantObjects, e.Object)
	}
}

// With the step required and missing, requireFresh refuses the create and
// the rotation and writes one denial entry for each, with the subject, the
// action and the object of the change. When the denial entry cannot be
// written, the refusal still returns, and no token is made or rotated.
func TestRequireFreshRefusesWithDenialRecord(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		// auditFor returns the audit store of the service over f.
		auditFor func(f *fixture) audit.ServerStore
		// auditErr is the error that the call must wrap, nil when the denial
		// entry is written.
		auditErr error
		// wantEntries is the number of entries that the two refusals append.
		wantEntries int
	}{
		{
			name:        "denial entry is written",
			auditFor:    func(f *fixture) audit.ServerStore { return f.store },
			auditErr:    nil,
			wantEntries: 2,
		},
		{
			name:        "denial write fails",
			auditFor:    func(f *fixture) audit.ServerStore { return refusingAudit{Store: f.store} },
			auditErr:    errAuditDown,
			wantEntries: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)

			cfg := policyConfig(&f, *freshGuard(t, freshstep.PolicyFunc(nil)))
			cfg.Audit = tc.auditFor(&f)

			svc, err := accesstoken.NewService(cfg)
			if err != nil {
				t.Fatalf("token service: %v", err)
			}

			tok, _ := f.create(t, f.service, "seed")
			successes := countOutcome(serverEntries(t, f.store), audit.OutcomeSuccess)

			_, _, err = svc.Create(t.Context(), admin(), caller(), accesstoken.ScopeService,
				accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: f.service.ID, Name: "x"})
			checkDenied(t, "create", err, tc.auditErr)

			_, _, err = svc.Rotate(t.Context(), admin(), caller(), accesstoken.RotateInput{
				OldEndsAt: nil, ExpiresAt: time.Time{}, ID: tok.ID, Scope: accesstoken.ScopeService,
			})
			checkDenied(t, "rotate", err, tc.auditErr)

			checkDenials(t, f.store, denialWant{
				service: f.service.ID, tok: tok.ID, successes: successes, entries: tc.wantEntries,
			})

			got, err := svc.Get(t.Context(), tok.ID)
			if err != nil {
				t.Fatalf("get: %v", err)
			}

			if got.ReplacedBy != "" {
				t.Fatalf("replaced by = %q, want no rotation", got.ReplacedBy)
			}
		})
	}
}
