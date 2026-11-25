package access_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// fixedCallers answers the subjects of one caller with a fixed list, as a
// hostile or broken port does, and every other caller from the store.
type fixedCallers struct {
	access.Callers

	subject  string
	subjects []access.Subject
}

func (c fixedCallers) SubjectsOf(ctx context.Context, subject string) ([]access.Subject, error) {
	if subject == c.subject {
		return c.subjects, nil
	}

	return c.Callers.SubjectsOf(ctx, subject)
}

// foreignGrants answers the grant read of one subject with rows that name
// other subjects, as a hostile port does.
type foreignGrants struct {
	access.GrantLookups

	subject access.Subject
	rows    []access.Grant
}

func (g foreignGrants) GrantsOf(ctx context.Context, subject access.Subject) ([]access.Grant, error) {
	if subject == g.subject {
		return g.rows, nil
	}

	return g.GrantLookups.GrantsOf(ctx, subject)
}

// errRunaway is the error of a stuck port that a loop did not stop: a guard
// that is missing ends the test with this error and not with an endless loop.
var errRunaway = errors.New("the loop read too many pages of a stuck port")

// maxStuckPages is the number of pages that stuckPrincipals answers before it
// fails with errRunaway.
const maxStuckPages = 20

// stuckPrincipals answers every page of the principals of a namespace with
// the same full page, so that the cursor never advances. After maxStuckPages
// pages it fails with errRunaway, so a missing guard fails the test fast.
type stuckPrincipals struct {
	access.PrincipalNamespaces

	pages *atomic.Int64
}

func (s stuckPrincipals) ListNamespacePrincipals(
	_ context.Context, _ namespace.Name, _ principal.Query,
) ([]principal.Principal, int64, error) {
	if s.pages.Add(1) > maxStuckPages {
		return nil, 0, errRunaway
	}

	rows := make([]principal.Principal, principal.MaxLimit)
	for i := range rows {
		rows[i] = principal.Principal{
			CreatedAt: time.Time{}, UpdatedAt: time.Time{}, DisabledAt: time.Time{}, ID: "prn-stuck",
			Subject: "stuck", DisplayName: "", Kind: principal.KindService, Source: principal.SourceLocal,
			Namespace: "acme", GroupCount: 0, BreakGlass: false,
		}
	}

	return rows, 0, nil
}

// hostileSubjectLists are the lists that SubjectsOf must never give for the
// namespace principal ci-acme: each one carries a group of the installation
// that holds the site administrator role, and none has the principal subject
// that the walk and the authorizer can confine by.
func hostileSubjectLists(group string) map[string][]access.Subject {
	g := access.Subject{Kind: access.SubjectGroup, ID: group}

	return map[string][]access.Subject{
		"group with no principal subject":    {g},
		"principal subject of another one":   {{Kind: access.SubjectPrincipal, ID: "prn-other"}, g},
		"two principal subjects":             {prn("ci-acme"), {Kind: access.SubjectPrincipal, ID: "prn-other"}, g},
		"subject of an unknown kind":         {prn("ci-acme"), g, {Kind: "role", ID: "x"}},
		"principal subject with empty ID":    {{Kind: access.SubjectPrincipal, ID: ""}, g},
		"group before an unknown kind only":  {g, {Kind: "role", ID: "x"}},
		"unknown kind with the right person": {{Kind: "robot", ID: "prn-ci-acme"}},
	}
}

// TestReachAndAuthorizerFailClosedOnHostileSubjects proves, at the walker and
// at the authorizer, that a subject list that cannot be confined gives an
// error and no power, and that both sides agree.
func TestReachAndAuthorizerFailClosedOnHostileSubjects(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.namespaceBot(t, "prn-ci-acme", "ci-acme", "acme")

	team := f.group(t, "team")
	f.grant(t, team, access.RoleSiteAdministrator, identity.ServerObject())

	for name, subjects := range hostileSubjectLists(team.ID) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			callers := fixedCallers{Callers: f.store, subject: "ci-acme", subjects: subjects}

			cfg := f.reach()
			cfg.Callers = callers

			reach, reachErr := access.ReachOf(t.Context(), cfg, "prn-ci-acme")
			if !errors.Is(reachErr, access.ErrInvalid) {
				t.Fatalf("reach = %v, want ErrInvalid", reachErr)
			}

			// The reach holds nothing on an error.
			if heldBackup(t, reach) {
				t.Fatal("reach of the failed walk holds server:backup")
			}

			// The authorizer knows only the subject string of the caller, not
			// the principal ID, so it cannot see that the principal subject
			// is another principal. The walker knows the record and does.
			if name == "principal subject of another one" {
				return
			}

			assertAuthorizerRefuses(t, f, callers)
		})
	}
}

// TestReachMatchesAuthorizerOnHostileFixture proves that the reach and the
// authorizer give the same answer for each action on the escalation fixture
// of planted grants and a planted group membership.
func TestReachMatchesAuthorizerOnHostileFixture(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	bot := namespaceEscalation(t, f).bot

	team := f.group(t, "team")
	f.grant(t, team, access.RoleSiteAdministrator, identity.ServerObject())
	f.grant(t, team, access.RoleViewer, identity.NamespaceObject("beta"))

	callers := groupCallers{Callers: f.store, subject: "ci-acme", group: team.ID}

	cfg := f.reach()
	cfg.Callers = callers

	reach, err := access.ReachOf(t.Context(), cfg, "prn-ci-acme")
	if err != nil {
		t.Fatalf("reach: %v", err)
	}

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: f.store, Callers: callers, Collections: f.units, Units: f.store, Principals: f.store,
		Settings: nil,
	})
	if err != nil {
		t.Fatalf("new authorizer: %v", err)
	}

	for _, tc := range []struct {
		action identity.Action
		obj    identity.Object
		want   bool
	}{
		{identity.ActionServerBackup, identity.ServerObject(), false},
		{identity.ActionServerManagePrincipals, identity.ServerObject(), false},
		{identity.ActionNamespaceRead, identity.NamespaceObject("beta"), false},
		{identity.ActionNamespaceRead, identity.NamespaceObject("acme"), true},
	} {
		has, hasErr := reach.Has(t.Context(), tc.action, tc.obj)
		if hasErr != nil {
			t.Fatalf("has %s on %s: %v", tc.action, tc.obj, hasErr)
		}

		if has != tc.want {
			t.Fatalf("%s on %s: reach %v, want %v", tc.action, tc.obj, has, tc.want)
		}

		authErr := authz.Authorize(t.Context(), bot, tc.action, tc.obj)
		if allowed := authErr == nil; allowed != has {
			t.Fatalf("%s on %s: reach %v, authorizer %v (%v)", tc.action, tc.obj, has, allowed, authErr)
		}
	}
}

// TestReachRefusesGrantOfAnotherSubject proves that the walk refuses a grant
// that the port answers for a subject that the read did not ask for: in
// visit, for a principal, and in seedGroup, for a group.
func TestReachRefusesGrantOfAnotherSubject(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.namespaceBot(t, "prn-ci-acme", "ci-acme", "acme")
	f.person(t, "outsider")

	team := f.group(t, "team")

	// The grants are of another subject. The first is of a principal that
	// has no namespace, so only the check of the subject stops it. The
	// second is of a principal of a namespace on the installation.
	foreign := plantedGrant("grt-foreign", prn("outsider"), access.RoleSiteAdministrator, identity.ServerObject())
	planted := plantedGrant("grt-foreign-ns", prn("ci-acme"), access.RoleSiteAdministrator, identity.ServerObject())

	for _, tc := range []struct {
		name string
		read access.Subject
		row  access.Grant
		walk func(context.Context, access.ReachConfig) error
	}{
		{
			name: "principal read with a grant of an outsider", read: prn("ci-acme"), row: foreign,
			walk: func(ctx context.Context, cfg access.ReachConfig) error {
				_, err := access.ReachOf(ctx, cfg, "prn-ci-acme")

				return err
			},
		},
		{
			name: "group read with a grant of an outsider", read: team, row: foreign,
			walk: func(ctx context.Context, cfg access.ReachConfig) error {
				_, err := access.GroupReachOf(ctx, cfg, team.ID)

				return err
			},
		},
		{
			name: "group read with a grant of a namespace principal on the installation", read: team, row: planted,
			walk: func(ctx context.Context, cfg access.ReachConfig) error {
				_, err := access.GroupReachOf(ctx, cfg, team.ID)

				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cfg := f.reach()
			cfg.Grants = foreignGrants{GrantLookups: f.store, subject: tc.read, rows: []access.Grant{tc.row}}

			if err := tc.walk(t.Context(), cfg); !errors.Is(err, access.ErrInvalid) {
				t.Fatalf("walk = %v, want ErrInvalid", err)
			}
		})
	}
}

// TestReachFailsClosedOnAFailedSubjectConfinementRead proves that a failed
// read of the principal during the confinement of the subjects returns the
// error and no reach.
func TestReachFailsClosedOnAFailedSubjectConfinementRead(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.namespaceBot(t, "prn-ci-acme", "ci-acme", "acme")

	team := f.group(t, "team")

	cfg := f.reach()
	cfg.Callers = groupCallers{Callers: f.store, subject: "ci-acme", group: team.ID}
	// The first read is the visit of the principal; the second is the
	// confinement of its subjects.
	cfg.Principals = failAfterPrincipals{PrincipalNamespaces: f.store, reads: new(atomic.Int64), limit: 2}

	_, err := access.ReachOf(t.Context(), cfg, "prn-ci-acme")
	if !errors.Is(err, errReachPrincipal) || !strings.Contains(err.Error(), "confine subjects") {
		t.Fatalf("reach = %v, want the principal read error of the subject confinement", err)
	}
}

// TestReachFailsClosedOnAStuckNamespacePage proves that a port that answers
// each page with the same full page ends the walk with an error and not with
// an endless loop.
func TestReachFailsClosedOnAStuckNamespacePage(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	nsTokens := f.customRole(t, identity.ActionNamespaceManagePrincipalTokens)

	f.person(t, "carol")
	f.grant(t, prn("carol"), nsTokens.ID, identity.NamespaceObject("acme"))

	cfg := f.reach()
	cfg.Principals = stuckPrincipals{PrincipalNamespaces: f.store, pages: new(atomic.Int64)}

	if _, err := access.ReachOf(t.Context(), cfg, "prn-carol"); !errors.Is(err, access.ErrInvalid) {
		t.Fatalf("reach = %v, want ErrInvalid for a page that does not advance", err)
	}
}

// TestAuthorizerFailsClosedOnAStuckNamespacePage proves that ListObjects ends
// with an error, and not with an endless loop or an output that grows, when
// the port answers each page of the principals of a namespace with the same
// full page. The caller holds namespace:manage-principal-tokens on acme.
func TestAuthorizerFailsClosedOnAStuckNamespacePage(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	nsTokens := f.customRole(t, identity.ActionNamespaceManagePrincipalTokens)

	carol := f.person(t, "carol")
	f.grant(t, prn("carol"), nsTokens.ID, identity.NamespaceObject("acme"))

	stuck := stuckPrincipals{PrincipalNamespaces: f.store, pages: new(atomic.Int64)}

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: f.store, Callers: f.store, Collections: f.units, Units: f.store, Principals: stuck,
		Settings: nil,
	})
	if err != nil {
		t.Fatalf("new authorizer: %v", err)
	}

	_, listErr := authz.ListObjects(t.Context(), carol, identity.ActionPrincipalManageTokens)
	if !errors.Is(listErr, access.ErrInvalid) {
		t.Fatalf("list objects = %v, want ErrInvalid for a page that does not advance", listErr)
	}

	if pages := stuck.pages.Load(); pages > 2 {
		t.Fatalf("the loop read %d pages of a stuck port, want at most 2", pages)
	}
}

// TestEmptyPrincipalSubjectIsRefusedOnItsOwn proves that a principal subject
// with an empty ID, listed before the right principal subject and a group,
// gives ErrInvalid at the walker and at the authorizer, and that a grant
// planted for the empty principal gives no power. Only the empty-ID check
// stops this list: it has one valid principal subject and a group.
func TestEmptyPrincipalSubjectIsRefusedOnItsOwn(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	empty := access.Subject{Kind: access.SubjectPrincipal, ID: ""}

	plantGrant(t, f, plantedGrant("grt-empty", empty, access.RoleSiteAdministrator, identity.ServerObject()))
	f.namespaceBot(t, "prn-ci-acme", "ci-acme", "acme")

	team := f.group(t, "team")
	subjects := []access.Subject{empty, prn("ci-acme"), {Kind: access.SubjectGroup, ID: team.ID}}
	callers := fixedCallers{Callers: f.store, subject: "ci-acme", subjects: subjects}

	cfg := f.reach()
	cfg.Callers = callers

	reach, reachErr := access.ReachOf(t.Context(), cfg, "prn-ci-acme")
	if !errors.Is(reachErr, access.ErrInvalid) {
		t.Errorf("reach = %v, want ErrInvalid", reachErr)
	}

	if heldBackup(t, reach) {
		t.Error("reach of the failed walk holds server:backup")
	}

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: f.store, Callers: callers, Collections: f.units, Units: f.store, Principals: f.store,
		Settings: nil,
	})
	if err != nil {
		t.Fatalf("new authorizer: %v", err)
	}

	if authErr := authorizeBackup(t, authz); !errors.Is(authErr, access.ErrInvalid) {
		t.Errorf("authorize = %v, want ErrInvalid", authErr)
	}
}

// TestServiceGrantReachDropsPlantedNamespacePrincipalRows proves, at the
// access service, that a service principal with a namespace-wide act-as edge
// reaches no planted installation grant of a namespace principal, so the
// granter needs no such action; and that a subject list that cannot be
// confined refuses the grant.
func TestServiceGrantReachDropsPlantedNamespacePrincipalRows(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	acct := f.accountAdministrator(t, "acct")
	f.person(t, "bob")

	plantGrant(t, f, plantedGrant(
		"grt-planted-server", prn("ci-acme"), access.RoleSiteAdministrator, identity.ServerObject(),
	))
	f.namespaceBot(t, "prn-ci-acme", "ci-acme", "acme")

	// The service principal may act as each principal of acme.
	f.installationService(t, "prn-svc", "svc")
	nsTokens := f.tokenRole(t, "ns-token")
	f.grant(t, prn("svc"), nsTokens.ID, identity.NamespaceObject("acme"))
	f.grant(t, prn("acct"), nsTokens.ID, identity.NamespaceObject("acme"))

	team := f.group(t, "team")
	hostile := f.reach()
	hostile.Callers = fixedCallers{
		Callers: f.store, subject: "ci-acme",
		subjects: []access.Subject{{Kind: access.SubjectGroup, ID: team.ID}},
	}

	target := identity.PrincipalObject("prn-svc")

	for _, tc := range []struct {
		name    string
		service *access.Service
		wantErr bool
	}{
		{"planted installation grant is dropped by the reach", f.svc, false},
		{"subjects that cannot be confined refuse the grant", f.serviceWith(t, hostile), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := tc.service.Grant(t.Context(), testEntry(), acct, access.GrantInput{
				Object: target, Role: access.RoleTokenManager, Subject: prn("bob"),
			})
			if (err != nil) != tc.wantErr || (tc.wantErr && !errors.Is(err, access.ErrEscalation)) {
				t.Fatalf("grant = %v, want error %v", err, tc.wantErr)
			}
		})
	}
}

// heldBackup reports whether reach holds server:backup on the installation.
func heldBackup(t *testing.T, reach access.Reach) bool {
	t.Helper()

	has, err := reach.Has(t.Context(), identity.ActionServerBackup, identity.ServerObject())

	return err == nil && has
}

// authorizeBackup asks authz whether the namespace principal ci-acme may
// server:backup on the installation.
func authorizeBackup(t *testing.T, authz *access.Authorizer) error {
	t.Helper()

	return authz.Authorize(
		t.Context(), namespacePrincipalCaller(), identity.ActionServerBackup, identity.ServerObject(),
	)
}

// assertAuthorizerRefuses proves that an authorizer over callers refuses
// server:backup to the namespace principal ci-acme.
func assertAuthorizerRefuses(t *testing.T, f *fixture, callers access.Callers) {
	t.Helper()

	authz, err := access.NewAuthorizer(access.AuthorizerConfig{
		Store: f.store, Callers: callers, Collections: f.units, Units: f.store, Principals: f.store,
		Settings: nil,
	})
	if err != nil {
		t.Fatalf("new authorizer: %v", err)
	}

	if authorizeBackup(t, authz) == nil {
		t.Fatal("authorize allowed server:backup on a subject list that cannot be confined")
	}
}
