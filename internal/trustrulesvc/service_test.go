package trustrulesvc_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// fakeStore records the writes that reach it.
type fakeStore struct {
	created []trustrule.Rule
	entries []audit.Entry
	deleted []trustrule.ID
}

func (f *fakeStore) Create(_ context.Context, r trustrule.Rule, _ namespace.Fence, e audit.Entry) error {
	f.created = append(f.created, r)
	f.entries = append(f.entries, e)

	return nil
}

func (f *fakeStore) Get(context.Context, namespace.Name, trustrule.ID) (trustrule.Rule, error) {
	return trustrule.Rule{}, trustrule.ErrNotFound
}

func (f *fakeStore) List(context.Context, namespace.Name) ([]trustrule.Rule, error) {
	return nil, nil
}

func (f *fakeStore) Delete(
	_ context.Context,
	_ namespace.Name,
	id trustrule.ID,
	_ namespace.Fence,
	e audit.Entry,
) error {
	f.deleted = append(f.deleted, id)
	f.entries = append(f.entries, e)

	return nil
}

// fakeNamespaces gives every namespace with failover version 1.
type fakeNamespaces struct{}

func (f fakeNamespaces) Get(ctx context.Context, name namespace.Name) (namespace.Namespace, error) {
	return f.WritableHere(ctx, name)
}

func (fakeNamespaces) WritableHere(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	return namespace.Namespace{
		ID: "ns-1", CreatedAt: time.Time{}, Name: name, PrimaryInstallation: "eu-west", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}, nil
}

// fakePrincipals holds the principals by ID.
type fakePrincipals map[string]principal.Principal

func (f fakePrincipals) GetPrincipal(_ context.Context, id string) (principal.Principal, error) {
	p, ok := f[id]
	if !ok {
		return principal.Principal{}, principal.ErrNotFound
	}

	return p, nil
}

// grants allows the actions it lists, and records each check. A non-nil err
// answers every check.
type grants struct {
	allow   []identity.Action
	checked []string
	err     error
}

func (g *grants) Authorize(_ context.Context, _ identity.Principal, a identity.Action, obj identity.Object) error {
	g.checked = append(g.checked, string(a)+" "+obj.String())

	if g.err != nil {
		return g.err
	}

	if slices.Contains(g.allow, a) {
		return nil
	}

	return identity.ErrDenied
}

func (g *grants) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil, All: false}, nil
}

// fakeReach records the reach checks of a create and answers err.
type fakeReach struct {
	checked []string
	err     error
}

func (f *fakeReach) CheckReach(_ context.Context, caller identity.Principal, principalID string) error {
	f.checked = append(f.checked, caller.Subject+" "+principalID)

	return f.err
}

// reachRefusalError is a refusal of the reach check that names the actions
// of the principal that the caller does not hold.
type reachRefusalError struct {
	missing []access.HeldAction
	cause   error
}

func (r *reachRefusalError) Error() string {
	return "the reach of the principal is not inside the reach of the caller"
}

func (r *reachRefusalError) MissingActions() []access.HeldAction { return r.missing }

func (r *reachRefusalError) Unwrap() error { return r.cause }

// errStoreUnavailable is the read error of the cases that cannot run a
// check.
var errStoreUnavailable = errors.New("the store is not available")

// fixture is a service over fakes, with the principals of the cases.
type fixture struct {
	svc   *trustrulesvc.Service
	store *fakeStore
	authz *grants
	reach *fakeReach
}

func newFixture(t *testing.T) fixture {
	t.Helper()

	store := &fakeStore{created: nil, entries: nil, deleted: nil}
	authz := &grants{allow: []identity.Action{identity.ActionPrincipalManageTokens}, checked: nil, err: nil}
	reach := &fakeReach{checked: nil, err: nil}

	disabled := principalOf("prn-disabled", principal.KindService, "acme")
	disabled.DisabledAt = time.Unix(1, 0)

	people := fakePrincipals{
		"prn-worker":   principalOf("prn-worker", principal.KindService, "acme"),
		"prn-disabled": disabled,
		"prn-person":   principalOf("prn-person", principal.KindPerson, ""),
		"prn-server":   principalOf("prn-server", principal.KindService, ""),
		"prn-stranger": principalOf("prn-stranger", principal.KindService, "other"),
		"prn-member":   principalOf("prn-member", principal.KindPerson, "acme"),
		"prn-workload": principalOf("prn-workload", principal.KindWorkload, "acme"),
		"prn-robot":    principalOf("prn-robot", principal.Kind("robot"), "acme"),
	}

	svc, err := trustrulesvc.NewService(trustrulesvc.Config{
		Store: store, Namespaces: fakeNamespaces{}, Principals: people, Authorizer: authz, Reach: reach,
		InstallationURLs: []string{"https://dustpan.example.com", "https://issuer.example.com/base/"}, OwnIssuer: nil,
		CheckURL: nil,
		Now:      nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return fixture{svc: svc, store: store, authz: authz, reach: reach}
}

// principalOf returns a principal record with the given ID, kind and
// namespace.
func principalOf(id string, kind principal.Kind, ns namespace.Name) principal.Principal {
	return principal.Principal{
		CreatedAt: time.Time{}, UpdatedAt: time.Time{}, DisabledAt: time.Time{}, ID: id, Subject: id,
		DisplayName: id, Kind: kind, Source: principal.SourceLocal, Namespace: ns, GroupCount: 0, BreakGlass: false,
	}
}

// person is a caller that signed in to the console.
func person() identity.Principal {
	return identity.Principal{
		Subject: "ada", DisplayName: "ada", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson,
		Credential: identity.CredentialSession, JobID: "",
	}
}

// input is a valid rule for the principal prn-worker.
func input() trustrulesvc.Input {
	return trustrulesvc.Input{
		Name: "deploy", Issuer: "https://token.actions.example.com", Audience: "https://dustpan.example.com",
		Keys: trustrule.KeySourceDiscovery, StaticKeys: "", PrincipalID: "prn-worker",
		Conditions: []trustrule.Condition{
			{Claim: "sub", Values: []string{"repo:acme/infra:ref:refs/heads/main"}},
			{Claim: "repository", Values: []string{"acme/infra"}},
		},
	}
}

func emptyEntry() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "ada", IDPSubject: "", TokenID: "", Action: "", Object: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "",
		Channel: audit.ChannelAPI, Kind: "", Outcome: "", RequestID: "", ObjectID: "", PrevHash: "", Hash: "",
		Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}
}

func TestCreateStoresTheRuleWithItsEntry(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	r, err := f.svc.Create(t.Context(), person(), emptyEntry(), "acme", input())
	if err != nil {
		t.Fatalf("Create = %v", err)
	}

	if len(f.store.created) != 1 || f.store.created[0].ID != r.ID || r.CreatedBy != "ada" {
		t.Fatalf("store got %+v, want the rule %s by ada", f.store.created, r.ID)
	}

	if r.Conditions[0].Claim != "repository" {
		t.Errorf("conditions = %+v, want sorted by claim", r.Conditions)
	}

	e := f.store.entries[0]
	if e.Action != trustrulesvc.ActionCreate || e.Namespace != "acme" || e.ObjectID != string(r.ID) ||
		len(e.Changes) == 0 {
		t.Errorf("entry = %+v", e)
	}

	if want := "principal:manage-tokens principal:prn-worker"; len(f.authz.checked) != 1 || f.authz.checked[0] != want {
		t.Errorf("checks = %v, want %q", f.authz.checked, want)
	}
}

// TestCreateRefusesATokenCaller sends a create as if every other step had
// failed: the caller authenticated with a stored token, or with a token of a
// trust rule. No check of the rule runs, and nothing reaches the store.
func TestCreateRefusesATokenCaller(t *testing.T) {
	t.Parallel()

	stored := person()
	stored.TokenID = "tok-1"

	trust := person()
	trust.Credential = identity.CredentialTrustToken
	trust.Kind = identity.KindService

	for name, caller := range map[string]identity.Principal{"stored token": stored, "trust token": trust} {
		f := newFixture(t)

		_, err := f.svc.Create(t.Context(), caller, emptyEntry(), "acme", input())
		if !errors.Is(err, trustrulesvc.ErrSignInRequired) {
			t.Errorf("%s: Create = %v, want ErrSignInRequired", name, err)
		}

		if len(f.store.created) != 0 || len(f.authz.checked) != 0 {
			t.Errorf("%s: the refused create reached the store or the authorizer", name)
		}
	}
}

// TestCreateRefusesAnIssuerOfThisInstallation checks the refusal of each
// issuer at or under an installation URL, with the scheme and the host in
// any case. Issuers on other hosts, other schemes, or beside a base path
// pass.
func TestCreateRefusesAnIssuerOfThisInstallation(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	for _, issuer := range []string{
		"https://dustpan.example.com", "https://dustpan.example.com/", "https://DUSTPAN.Example.com/oidc/ns-1",
		"HTTPS://dustpan.example.com/oidc/ns-1", "https://issuer.example.com/base", "https://issuer.example.com/base/ns",
	} {
		if err := f.svc.CheckIssuer(issuer); !errors.Is(err, trustrule.ErrIssuerRefused) {
			t.Errorf("CheckIssuer(%q) = %v, want ErrIssuerRefused", issuer, err)
		}
	}

	for _, issuer := range []string{
		"https://token.actions.example.com", "https://issuer.example.com/basement", "https://issuer.example.com",
		"https://dustpan.example.com.evil.example", "ci-cluster-7",
	} {
		if err := f.svc.CheckIssuer(issuer); err != nil {
			t.Errorf("CheckIssuer(%q) = %v, want nil", issuer, err)
		}
	}

	in := input()
	in.Issuer = "https://dustpan.example.com/oidc/ns-1"

	_, err := f.svc.Create(t.Context(), person(), emptyEntry(), "acme", in)
	if !errors.Is(err, trustrule.ErrIssuerRefused) {
		t.Fatalf("Create = %v, want ErrIssuerRefused", err)
	}

	if len(f.store.created) != 0 {
		t.Fatal("the refused create reached the store")
	}
}

// TestCreateRefusesAPrincipalOutsideTheNamespace checks the principal of a
// rule at the service. Defense in depth: the store checks it again.
func TestCreateRefusesAPrincipalOutsideTheNamespace(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"prn-unknown", "prn-disabled", "prn-person", "prn-server", "prn-stranger"} {
		f := newFixture(t)

		in := input()
		in.PrincipalID = id

		_, err := f.svc.Create(t.Context(), person(), emptyEntry(), "acme", in)
		if !errors.Is(err, trustrule.ErrPrincipal) {
			t.Errorf("principal %s: Create = %v, want ErrPrincipal", id, err)
		}

		if len(f.store.created) != 0 {
			t.Errorf("principal %s: the refused create reached the store", id)
		}
	}
}

// TestCreateNeedsAServicePrincipal checks the kind check alone: each
// principal is of the namespace and not disabled, so only its kind refuses
// it.
func TestCreateNeedsAServicePrincipal(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"prn-member", "prn-workload", "prn-robot"} {
		f := newFixture(t)

		in := input()
		in.PrincipalID = id

		_, err := f.svc.Create(t.Context(), person(), emptyEntry(), "acme", in)
		if !errors.Is(err, trustrule.ErrPrincipal) || !strings.Contains(err.Error(), "not a service principal") {
			t.Errorf("principal %s: Create = %v, want ErrPrincipal of the kind check", id, err)
		}

		if len(f.store.created) != 0 {
			t.Errorf("principal %s: the refused create reached the store", id)
		}
	}
}

// TestCreateDeniesBeforeItReadsThePrincipal checks that a caller without
// principal:manage-tokens gets the same denial for each principal ID: it
// never learns whether the ID names a principal of the namespace, or its
// kind.
func TestCreateDeniesBeforeItReadsThePrincipal(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"prn-unknown", "prn-member", "prn-workload", "prn-worker", "prn-stranger"} {
		f := newFixture(t)
		f.authz.allow = nil

		in := input()
		in.PrincipalID = id

		_, err := f.svc.Create(t.Context(), person(), emptyEntry(), "acme", in)
		if !errors.Is(err, identity.ErrDenied) || errors.Is(err, trustrule.ErrPrincipal) {
			t.Errorf("principal %s: Create = %v, want ErrDenied alone", id, err)
		}
	}
}

// TestCreateNeedsManageTokensOnThePrincipal checks the authorization at the
// point of use: the route allowed the create, and the caller still cannot
// make a rule for a principal that it cannot act as.
func TestCreateNeedsManageTokensOnThePrincipal(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.authz.allow = nil

	_, err := f.svc.Create(t.Context(), person(), emptyEntry(), "acme", input())
	if !errors.Is(err, identity.ErrDenied) || !errors.Is(err, trustrulesvc.ErrManageTokensDenied) {
		t.Fatalf("Create = %v, want ErrDenied and ErrManageTokensDenied", err)
	}

	if len(f.store.created) != 0 {
		t.Fatal("the denied create reached the store")
	}
}

// TestCreateNeedsTheReachOfThePrincipal checks that a create runs the reach
// check of a token change for the principal of the rule: a refusal stores
// nothing, and it wraps the refusal of the reach check.
func TestCreateNeedsTheReachOfThePrincipal(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.reach.err = fmt.Errorf("%w: %w", identity.ErrDenied, accesstoken.ErrReachExceeded)

	_, err := f.svc.Create(t.Context(), person(), emptyEntry(), "acme", input())
	if !errors.Is(err, identity.ErrDenied) || !errors.Is(err, accesstoken.ErrReachExceeded) {
		t.Fatalf("Create = %v, want ErrDenied and ErrReachExceeded", err)
	}

	if errors.Is(err, trustrulesvc.ErrManageTokensDenied) {
		t.Fatalf("Create = %v, want no ErrManageTokensDenied for a reach refusal", err)
	}

	if len(f.store.created) != 0 {
		t.Fatal("the refused create reached the store")
	}

	if want := "ada prn-worker"; len(f.reach.checked) != 1 || f.reach.checked[0] != want {
		t.Errorf("reach checks = %v, want %q", f.reach.checked, want)
	}
}

// TestCreatePassesWhenTheReachCoversThePrincipal checks the pass path: a
// reach check that answers nil stores the rule, and it sees the caller and
// the principal of the rule.
func TestCreatePassesWhenTheReachCoversThePrincipal(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	r, err := f.svc.Create(t.Context(), person(), emptyEntry(), "acme", input())
	if err != nil {
		t.Fatalf("Create = %v", err)
	}

	if len(f.store.created) != 1 || f.store.created[0].ID != r.ID {
		t.Fatalf("store got %+v, want the rule %s", f.store.created, r.ID)
	}

	if want := "ada prn-worker"; len(f.reach.checked) != 1 || f.reach.checked[0] != want {
		t.Errorf("reach checks = %v, want %q", f.reach.checked, want)
	}
}

// TestCreateChecksTheReachBeforeItReadsThePrincipal checks the order: the
// reach check runs after the action check and before the checks of the
// principal, so a caller whose reach is refused never learns whether the ID
// names a principal, or its kind.
func TestCreateChecksTheReachBeforeItReadsThePrincipal(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.reach.err = fmt.Errorf("%w: %w", identity.ErrDenied, accesstoken.ErrReachExceeded)

	in := input()
	in.PrincipalID = "prn-person"

	_, err := f.svc.Create(t.Context(), person(), emptyEntry(), "acme", in)
	if !errors.Is(err, accesstoken.ErrReachExceeded) || errors.Is(err, trustrule.ErrPrincipal) {
		t.Fatalf("Create = %v, want the reach refusal alone", err)
	}
}

// TestCreateMarksACheckThatCouldNotRun checks the mark of a create whose
// access check or reach check could not run. A reach refusal that names
// missing actions stays unmarked.
func TestCreateMarksACheckThatCouldNotRun(t *testing.T) {
	t.Parallel()

	refused := &reachRefusalError{
		missing: []access.HeldAction{{
			Action: identity.ActionPrincipalManageTokens,
			Object: identity.PrincipalObject("prn-worker"),
		}},
		cause: accesstoken.ErrReachExceeded,
	}

	tests := []struct {
		name    string
		prepare func(*fixture)
		want    []error
		notWant []error
	}{
		{
			name:    "an access check that could not run",
			prepare: func(f *fixture) { f.authz.err = errStoreUnavailable },
			want:    []error{trustrulesvc.ErrAccessUnchecked, errStoreUnavailable},
			notWant: []error{trustrulesvc.ErrManageTokensDenied},
		},
		{
			name:    "a reach check that could not run",
			prepare: func(f *fixture) { f.reach.err = errStoreUnavailable },
			want:    []error{trustrulesvc.ErrReachUnchecked, errStoreUnavailable},
			notWant: []error{trustrulesvc.ErrManageTokensDenied},
		},
		{
			name:    "a reach refusal with missing actions",
			prepare: func(f *fixture) { f.reach.err = refused },
			want:    []error{accesstoken.ErrReachExceeded},
			notWant: []error{trustrulesvc.ErrReachUnchecked},
		},
	}

	for i := range tests {
		tc := &tests[i]
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			tc.prepare(&f)

			_, err := f.svc.Create(t.Context(), person(), emptyEntry(), "acme", input())
			checkCreateError(t, err, tc.want, tc.notWant)

			if len(f.store.created) != 0 {
				t.Error("the refused create reached the store")
			}
		})
	}
}

// checkCreateError checks that err matches each error of want and none of
// notWant.
func checkCreateError(t *testing.T, err error, want, notWant []error) {
	t.Helper()

	if err == nil {
		t.Fatal("Create = nil, want an error")
	}

	for _, target := range want {
		if !errors.Is(err, target) {
			t.Errorf("Create = %v, want it to match %v", err, target)
		}
	}

	for _, target := range notWant {
		if errors.Is(err, target) {
			t.Errorf("Create = %v, want it not to match %v", err, target)
		}
	}
}

// TestCreateRefusesWithNoReachChecker checks the fail-closed rule: a
// service built without a reach checker refuses every create.
func TestCreateRefusesWithNoReachChecker(t *testing.T) {
	t.Parallel()

	store := &fakeStore{created: nil, entries: nil, deleted: nil}
	authz := &grants{allow: []identity.Action{identity.ActionPrincipalManageTokens}, checked: nil, err: nil}
	people := fakePrincipals{"prn-worker": principalOf("prn-worker", principal.KindService, "acme")}

	svc, err := trustrulesvc.NewService(trustrulesvc.Config{
		Store: store, Namespaces: fakeNamespaces{}, Principals: people, Authorizer: authz, Reach: nil,
		InstallationURLs: nil, OwnIssuer: nil, CheckURL: nil, Now: nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	_, err = svc.Create(t.Context(), person(), emptyEntry(), "acme", input())
	if !errors.Is(err, identity.ErrDenied) || !errors.Is(err, trustrulesvc.ErrReachUnchecked) ||
		errors.Is(err, trustrulesvc.ErrManageTokensDenied) {
		t.Fatalf(
			"Create with no reach checker = %v, want ErrDenied and ErrReachUnchecked without ErrManageTokensDenied",
			err,
		)
	}

	if len(store.created) != 0 {
		t.Fatal("the refused create reached the store")
	}
}

func TestCreateRefusesAnInvalidRule(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	in := input()
	in.Conditions = in.Conditions[1:]

	_, err := f.svc.Create(t.Context(), person(), emptyEntry(), "acme", in)
	if !errors.Is(err, trustrule.ErrInvalid) {
		t.Fatalf("Create = %v, want ErrInvalid", err)
	}

	if len(f.store.created) != 0 {
		t.Fatal("the refused create reached the store")
	}
}

func TestDeleteOfAnInvalidIDIsNotFound(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if err := f.svc.Delete(t.Context(), emptyEntry(), "acme", "trr-a/b"); !errors.Is(err, trustrule.ErrNotFound) {
		t.Fatalf("Delete = %v, want ErrNotFound", err)
	}

	id := trustrule.NewID()
	if err := f.svc.Delete(t.Context(), emptyEntry(), "acme", id); err != nil {
		t.Fatalf("Delete = %v", err)
	}

	if len(f.store.deleted) != 1 || f.store.entries[0].Action != trustrulesvc.ActionDelete {
		t.Fatalf("store got %v %+v", f.store.deleted, f.store.entries)
	}
}

func TestNewServiceNeedsItsPorts(t *testing.T) {
	t.Parallel()

	full := func() trustrulesvc.Config {
		return trustrulesvc.Config{
			Store:            &fakeStore{created: nil, entries: nil, deleted: nil},
			Namespaces:       fakeNamespaces{},
			Principals:       fakePrincipals{},
			Authorizer:       &grants{allow: nil, checked: nil, err: nil},
			Reach:            &fakeReach{checked: nil, err: nil},
			InstallationURLs: nil, OwnIssuer: nil, CheckURL: nil,
			Now: nil,
		}
	}

	for name, change := range map[string]func(c *trustrulesvc.Config){
		"store":      func(c *trustrulesvc.Config) { c.Store = nil },
		"namespaces": func(c *trustrulesvc.Config) { c.Namespaces = nil },
		"principals": func(c *trustrulesvc.Config) { c.Principals = nil },
		"authorizer": func(c *trustrulesvc.Config) { c.Authorizer = nil },
	} {
		cfg := full()
		change(&cfg)

		if _, err := trustrulesvc.NewService(cfg); !errors.Is(err, trustrule.ErrMissingDependency) {
			t.Errorf("no %s: NewService = %v, want ErrMissingDependency", name, err)
		}
	}

	cfg := full()
	cfg.InstallationURLs = []string{"/relative"}

	if _, err := trustrulesvc.NewService(cfg); !errors.Is(err, trustrule.ErrInvalid) {
		t.Errorf("a relative installation URL: NewService = %v, want ErrInvalid", err)
	}
}

// TestCreateRefusesAnIssuerThatTheEgressRulesRefuse checks a discovery
// issuer against the egress rules when a person saves the rule. Defense in
// depth: the fetcher and the dial guard check each address again.
func TestCreateRefusesAnIssuerThatTheEgressRulesRefuse(t *testing.T) {
	t.Parallel()

	for _, issuer := range []string{
		"https://127.0.0.1", "https://localhost", "https://169.254.169.254/latest", "https://10.0.0.1",
		"https://[::1]", "https://2130706433", "https://0x7f000001",
	} {
		f := newFixture(t)

		in := input()
		in.Issuer = issuer

		_, err := f.svc.Create(t.Context(), person(), emptyEntry(), "acme", in)
		if !errors.Is(err, trustrule.ErrInvalid) {
			t.Errorf("issuer %s: Create = %v, want ErrInvalid", issuer, err)
		}

		if len(f.store.created) != 0 {
			t.Errorf("issuer %s: the refused create reached the store", issuer)
		}
	}

	// A static rule makes no outbound request, so its issuer is only a
	// name, and the egress rules do not apply to it.
	f := newFixture(t)

	in := input()
	in.Keys, in.Issuer, in.StaticKeys = trustrule.KeySourceStatic, "https://10.0.0.1", staticKeys(t)

	if _, err := f.svc.Create(t.Context(), person(), emptyEntry(), "acme", in); err != nil {
		t.Fatalf("a static rule with a private issuer name: Create = %v", err)
	}
}

// staticKeys returns a JWKS with one new Ed25519 key.
func staticKeys(t *testing.T) string {
	t.Helper()

	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	return `{"keys":[{"kty":"OKP","crv":"Ed25519","kid":"k1","x":"` + base64.RawURLEncoding.EncodeToString(pub) + `"}]}`
}
