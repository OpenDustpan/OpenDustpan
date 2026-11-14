package localusersvc_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/localuser/blocklist"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// demoSeedFor builds a demo seed over the store of f. The store claims kind.
func demoSeedFor(t *testing.T, f *fixture, kind installation.Kind) (*localusersvc.DemoSeed, error) {
	t.Helper()

	if _, err := f.store.ClaimKind(t.Context(), kind); err != nil {
		t.Fatalf("claim kind: %v", err)
	}

	return localusersvc.NewDemoSeed(localusersvc.DemoConfig{
		Passwords: f.store, Principals: f.principals, Grants: f.store, Namespaces: f.store, Hasher: f.hasher,
		Blocklist: blocklist.Embedded(), Kind: kind, Now: nil, Random: nil,
	})
}

// makeNamespaces makes the namespaces in the store and answers their names.
func makeNamespaces(t *testing.T, f *fixture, names ...string) []namespace.Name {
	t.Helper()

	svc, err := namespace.NewService(namespace.Config{
		Store: f.store, Now: nil, Installation: namespace.InstallationName("local"),
	})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	out := make([]namespace.Name, 0, len(names))

	for _, n := range names {
		if _, err := svc.Create(t.Context(), namespace.Name(n)); err != nil {
			t.Fatalf("create namespace %s: %v", n, err)
		}

		out = append(out, namespace.Name(n))
	}

	return out
}

// demoGrants answers the current grants of the demo person as
// "role object" texts, sorted.
func demoGrants(ctx context.Context, t *testing.T, f *fixture) (string, []string) {
	t.Helper()

	person, err := f.store.GetPrincipalBySubject(ctx, localuser.DemoSubject)
	if err != nil {
		t.Fatalf("demo principal: %v", err)
	}

	grants, err := f.store.GrantsOf(ctx, access.Subject{Kind: access.SubjectPrincipal, ID: person.ID})
	if err != nil {
		t.Fatalf("grants of the demo person: %v", err)
	}

	got := make([]string, 0, len(grants))

	for i := range grants {
		// A grant that a test adds as "admin" is the extra grant of a test.
		if grants[i].CreatedBy != localusersvc.DemoCreator && grants[i].CreatedBy != "admin" {
			t.Errorf(
				"grant %s has the creator %q, want %q",
				grants[i].ID,
				grants[i].CreatedBy,
				localusersvc.DemoCreator,
			)
		}

		got = append(got, grants[i].Role+" "+grants[i].Object.String())
	}

	slices.Sort(got)

	return person.ID, got
}

// TestDemoSeedGrantsExactlyTheTwoRoles proves the grants of the demo person:
// the admin role on each named namespace and the viewer role on the
// installation, and never the site administrator role.
func TestDemoSeedGrantsExactlyTheTwoRoles(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	seed, err := demoSeedFor(t, f, installation.KindDev)
	if err != nil {
		t.Fatalf("new demo seed: %v", err)
	}

	names := makeNamespaces(t, f, "dev", "demo-platform", "demo-data")

	password, err := seed.Run(t.Context(), names)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if password == "" {
		t.Fatal("run answers no password")
	}

	_, got := demoGrants(t.Context(), t, f)

	want := []string{
		"admin " + identity.NamespaceObject("demo-data").String(),
		"admin " + identity.NamespaceObject("demo-platform").String(),
		"admin " + identity.NamespaceObject("dev").String(),
		"viewer " + identity.ServerObject().String(),
	}
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Fatalf("grants of the demo person = %v, want %v", got, want)
	}

	for _, g := range got {
		if strings.HasPrefix(g, access.RoleSiteAdministrator+" ") {
			t.Errorf("the demo person holds the role %s", access.RoleSiteAdministrator)
		}
	}

	if !personCanSignIn(t, f, password) {
		t.Fatal("the demo person cannot verify the printed password")
	}
}

// personCanSignIn reports whether the stored hash of the demo person matches
// password.
func personCanSignIn(t *testing.T, f *fixture, password string) bool {
	t.Helper()

	cred, err := f.store.UserByName(t.Context(), localuser.DemoUserName)
	if err != nil || !cred.User.Demo {
		t.Fatalf("demo user = %+v, %v", cred.User, err)
	}

	if _, verr := f.hasher.Verify(t.Context(), password, cred.Hash, localuser.DefaultHashParams()); verr != nil {
		return false
	}

	return true
}

// TestDemoSeedSecondStartResetsThePassword proves that a second run sets a new
// password, keeps one person and one set of grants.
func TestDemoSeedSecondStartResetsThePassword(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	seed, err := demoSeedFor(t, f, installation.KindDev)
	if err != nil {
		t.Fatalf("new demo seed: %v", err)
	}

	names := makeNamespaces(t, f, "dev")

	first, err := seed.Run(t.Context(), names)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}

	id1, grants1 := demoGrants(t.Context(), t, f)

	second, err := seed.Run(t.Context(), names)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}

	id2, grants2 := demoGrants(t.Context(), t, f)

	if first == second {
		t.Error("the second run keeps the password")
	}

	if id1 != id2 || !slices.Equal(grants1, grants2) {
		t.Errorf("second run: person %s grants %v, want person %s grants %v", id2, grants2, id1, grants1)
	}

	if personCanSignIn(t, f, first) || !personCanSignIn(t, f, second) {
		t.Error("only the password of the second run signs in")
	}
}

// TestDemoSeedRefusesAProductionKind proves that the seed refuses an
// installation of another kind at both layers: the constructor, and the store
// when a seed that was built for dev meets a database of kind production.
func TestDemoSeedRefusesAProductionKind(t *testing.T) {
	t.Parallel()

	t.Run("constructor", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)

		if _, err := demoSeedFor(t, f, installation.KindProduction); !errors.Is(err, localuser.ErrDemoOnly) {
			t.Fatalf("new demo seed on production = %v, want %v", err, localuser.ErrDemoOnly)
		}
	})

	t.Run("store", func(t *testing.T) {
		t.Parallel()

		f := newFixture(t)

		// Defense in depth: the seed is built for dev, and the database
		// belongs to production. The store reads the kind again.
		if _, err := f.store.ClaimKind(t.Context(), installation.KindProduction); err != nil {
			t.Fatalf("claim kind: %v", err)
		}

		seed, err := localusersvc.NewDemoSeed(localusersvc.DemoConfig{
			Passwords: f.store, Principals: f.principals, Grants: f.store, Namespaces: f.store, Hasher: f.hasher,
			Blocklist: blocklist.Embedded(), Kind: installation.KindDev, Now: nil, Random: nil,
		})
		if err != nil {
			t.Fatalf("new demo seed: %v", err)
		}

		if _, err := seed.Run(t.Context(), nil); !errors.Is(err, localuser.ErrDemoOnly) {
			t.Fatalf("run on a production database = %v, want %v", err, localuser.ErrDemoOnly)
		}
	})
}

// TestDemoPersonSubjectsMatch proves that the subject of the principal package
// and the one of the local user package are the same value, and that the
// principal service makes no other dev subject through the demo path.
func TestDemoPersonSubjectsMatch(t *testing.T) {
	t.Parallel()

	if principal.DemoSubject != localuser.DemoSubject {
		t.Fatalf("principal.DemoSubject = %q, localuser.DemoSubject = %q", principal.DemoSubject, localuser.DemoSubject)
	}
}

// demoPasswordForm is the form of the demo password: 5 groups of 4 lowercase
// letters and digits, joined by hyphens.
var demoPasswordForm = regexp.MustCompile(`^([a-z0-9]{4}-){4}[a-z0-9]{4}$`)

// TestDemoSeedPasswordForm proves the form of the demo password, and that the
// seed makes a new password when a password rule refuses one: the first bytes
// give a password with no digit, which the rules refuse.
func TestDemoSeedPasswordForm(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	if _, err := f.store.ClaimKind(t.Context(), installation.KindDev); err != nil {
		t.Fatalf("claim kind: %v", err)
	}

	// The byte 1 is the letter b: 20 of them make "bbbb-bbbb-bbbb-bbbb-bbbb".
	letters := bytes.Repeat([]byte{1}, localuser.DemoPasswordGroups*localuser.DemoPasswordGroupLength)

	seed, err := localusersvc.NewDemoSeed(localusersvc.DemoConfig{
		Passwords: f.store, Principals: f.principals, Grants: f.store, Namespaces: f.store, Hasher: f.hasher,
		Blocklist: blocklist.Embedded(), Kind: installation.KindDev, Now: nil,
		Random: io.MultiReader(bytes.NewReader(letters), rand.Reader),
	})
	if err != nil {
		t.Fatalf("new demo seed: %v", err)
	}

	password, err := seed.Run(t.Context(), nil)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if !demoPasswordForm.MatchString(password) || password == "bbbb-bbbb-bbbb-bbbb-bbbb" ||
		!strings.ContainsAny(password, "0123456789") {
		t.Fatalf("demo password has the wrong form or is the refused one (length %d)", len(password))
	}

	if len(password) != localuser.DemoPasswordGroups*(localuser.DemoPasswordGroupLength+1)-1 {
		t.Fatalf("demo password length = %d, want 24", len(password))
	}
}

// TestDemoPasswordIsInNoAuditEntry proves that the password is in no audit
// entry of the seed, in the scope of the installation and in each namespace.
func TestDemoPasswordIsInNoAuditEntry(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	seed, err := demoSeedFor(t, f, installation.KindDev)
	if err != nil {
		t.Fatalf("new demo seed: %v", err)
	}

	names := makeNamespaces(t, f, "dev", "demo-platform")

	password, err := seed.Run(t.Context(), names)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	scopes := []namespace.Name{audit.ServerScope, "dev", "demo-platform"}
	seen := 0

	for _, scope := range scopes {
		page, listErr := f.store.List(t.Context(), scope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
		if listErr != nil {
			t.Fatalf("list entries of %s: %v", scope, listErr)
		}

		for i := range page.Entries {
			seen++

			if text := fmt.Sprintf("%+v", page.Entries[i]); strings.Contains(text, password) {
				t.Errorf("an audit entry of %s holds the demo password: action %s", scope, page.Entries[i].Action)
			}

			if page.Entries[i].Subject == localusersvc.DemoCreator &&
				page.Entries[i].Reason != localusersvc.DemoReason {
				t.Errorf("entry %s has the reason %q, want %q", page.Entries[i].Action, page.Entries[i].Reason,
					localusersvc.DemoReason)
			}
		}
	}

	if seen == 0 {
		t.Fatal("the seed wrote no audit entry")
	}
}

// extraSiteAdministrator gives the site-administrator role on the installation
// to the principal id, as a person with the right to do so would do between two
// starts.
func extraSiteAdministrator(t *testing.T, f *fixture, grantID, principalID string) access.Grant {
	t.Helper()

	g := access.Grant{
		CreatedAt: time.Now().UTC().Truncate(time.Millisecond), Condition: nil, Object: identity.ServerObject(),
		ID: grantID, Role: access.RoleSiteAdministrator, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: principalID},
	}
	entry := auditEntryOf(access.GrantRef(g))
	entry.Action = "server:manage-access"

	none := namespace.Fence{Namespace: "", FailoverVersion: 0}

	err := f.store.CreateGrant(t.Context(), g, none, entry, nil)
	if err != nil {
		t.Fatalf("add the grant %s: %v", grantID, err)
	}

	return g
}

// TestDemoSeedRevokesAGrantOutsideTheFixedSet proves that a site-administrator
// grant added between two starts is gone after the second start, with an
// audit entry that names the grant and gives the reason "demo reset".
func TestDemoSeedRevokesAGrantOutsideTheFixedSet(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	seed, err := demoSeedFor(t, f, installation.KindDev)
	if err != nil {
		t.Fatalf("new demo seed: %v", err)
	}

	names := makeNamespaces(t, f, "dev")

	if _, runErr := seed.Run(t.Context(), names); runErr != nil {
		t.Fatalf("first run: %v", runErr)
	}

	personID, before := demoGrants(t.Context(), t, f)

	// A second site administrator, so the revoke does not remove the last one.
	other, err := f.principals.CreatePrincipal(t.Context(), auditEntryOf("principal:other"), principal.Input{
		Subject: "other-admin", DisplayName: "Other", Kind: principal.KindPerson,
	})
	if err != nil {
		t.Fatalf("make the second administrator: %v", err)
	}

	extraSiteAdministrator(t, f, "grt-other-site-admin", other.ID)
	extra := extraSiteAdministrator(t, f, "grt-extra-site-admin", personID)

	if _, runErr := seed.Run(t.Context(), names); runErr != nil {
		t.Fatalf("second run: %v", runErr)
	}

	_, after := demoGrants(t.Context(), t, f)
	if !slices.Equal(before, after) {
		t.Fatalf("grants after the second start = %v, want %v", after, before)
	}

	page, err := f.store.List(t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize})
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	found := false

	for i := range page.Entries {
		e := page.Entries[i]
		if e.Reason == localusersvc.DemoResetReason && strings.Contains(e.Object, extra.ID) &&
			strings.Contains(e.Object, access.RoleSiteAdministrator) {
			found = true
		}
	}

	if !found {
		t.Fatal("no audit entry names the revoked grant with the reason demo reset")
	}
}

// TestDemoSeedFailsClosedOnTheLastSiteAdministrator proves that the seed stops
// with access.ErrLastSiteAdministrator, and changes nothing else, when the
// extra grant is the last site-administrator grant of the installation: the
// grant, the password and the required grants stay as they are.
func TestDemoSeedFailsClosedOnTheLastSiteAdministrator(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	seed, err := demoSeedFor(t, f, installation.KindDev)
	if err != nil {
		t.Fatalf("new demo seed: %v", err)
	}

	first, err := seed.Run(t.Context(), nil)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}

	personID, _ := demoGrants(t.Context(), t, f)
	extraSiteAdministrator(t, f, "grt-last-site-admin", personID)

	// The second run names a namespace, so its admin grant is a required grant
	// that does not exist yet. The refused revoke must leave it missing.
	names := makeNamespaces(t, f, "dev")

	_, before := demoGrants(t.Context(), t, f)

	if _, runErr := seed.Run(t.Context(), names); !errors.Is(runErr, access.ErrLastSiteAdministrator) {
		t.Fatalf("second run = %v, want %v", runErr, access.ErrLastSiteAdministrator)
	}

	if !personCanSignIn(t, f, first) {
		t.Error("the refused start changed the password")
	}

	_, after := demoGrants(t.Context(), t, f)
	if !slices.Equal(before, after) {
		t.Fatalf("grants after the refused start = %v, want %v", after, before)
	}
}

// errRevokeDown is the error of a revoke that the store cannot do.
var errRevokeDown = errors.New("revoke down")

// failingRevokes is the grant store of f that fails each revoke after the
// first one, and records the grant that the first revoke ended.
type failingRevokes struct {
	localusersvc.DemoGrants

	revoked []string
}

func (g *failingRevokes) RevokeGrant(
	ctx context.Context, id string, fence namespace.Fence, e audit.Entry, check access.GrantCheck,
) error {
	if len(g.revoked) > 0 {
		return errRevokeDown
	}

	if err := g.DemoGrants.RevokeGrant(ctx, id, fence, e, check); err != nil {
		return err
	}

	g.revoked = append(g.revoked, id)

	return nil
}

// extraNamespaceAdmin gives the admin role on a new namespace "spare" to the
// principal id, as a person with the right to do so would do between two
// starts.
func extraNamespaceAdmin(t *testing.T, f *fixture, grantID, principalID string) {
	t.Helper()

	spare := makeNamespaces(t, f, "spare")

	ns, err := f.store.GetNamespace(t.Context(), spare[0])
	if err != nil {
		t.Fatalf("read the namespace spare: %v", err)
	}

	g := access.Grant{
		CreatedAt: time.UnixMilli(1700000000000).UTC(), Condition: nil, Object: identity.NamespaceObject(spare[0]),
		ID: grantID, Role: access.RoleAdmin, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: principalID},
	}

	entry := auditEntryOf(access.GrantRef(g))
	entry.Action, entry.Namespace = "server:manage-access", spare[0]

	if err := f.store.CreateGrant(t.Context(), g, ns.Fence(), entry, nil); err != nil {
		t.Fatalf("add the grant %s: %v", grantID, err)
	}
}

// checkRevokedOnce fails unless exactly one of the extra grants
// grt-extra-admin and grt-extra-site-admin was revoked, that grant stays
// revoked, and the other one stays.
func checkRevokedOnce(t *testing.T, f *fixture, personID string, revoked []string) {
	t.Helper()

	a, b := "grt-extra-admin", "grt-extra-site-admin"

	if len(revoked) != 1 {
		t.Fatalf("revoked grants = %v, want one", revoked)
	}

	current, err := f.store.GrantsOf(t.Context(), access.Subject{Kind: access.SubjectPrincipal, ID: personID})
	if err != nil {
		t.Fatalf("grants of the demo person: %v", err)
	}

	got := make([]string, 0, len(current))
	for i := range current {
		got = append(got, current[i].ID)
	}

	kept := a
	if revoked[0] == a {
		kept = b
	}

	if slices.Contains(got, revoked[0]) {
		t.Errorf("grants after the failed start = %v, want %s to stay revoked", got, revoked[0])
	}

	if !slices.Contains(got, kept) {
		t.Errorf("grants after the failed start = %v, want %s to stay", got, kept)
	}
}

// TestDemoSeedFailsClosedOnASecondRevoke proves that a revoke that fails
// after an earlier revoke stops the seed: the earlier extra grant stays
// revoked, the other extra grant stays, and the password and the required
// grants do not change.
func TestDemoSeedFailsClosedOnASecondRevoke(t *testing.T) {
	t.Parallel()

	f := newFixture(t)

	seed, err := demoSeedFor(t, f, installation.KindDev)
	if err != nil {
		t.Fatalf("new demo seed: %v", err)
	}

	first, err := seed.Run(t.Context(), nil)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}

	personID, _ := demoGrants(t.Context(), t, f)

	// A second site administrator, so that no revoke removes the last one.
	other, err := f.principals.CreatePrincipal(t.Context(), auditEntryOf("principal:other"), principal.Input{
		Subject: "other-admin", DisplayName: "Other", Kind: principal.KindPerson,
	})
	if err != nil {
		t.Fatalf("make the second administrator: %v", err)
	}

	extraSiteAdministrator(t, f, "grt-other-site-admin", other.ID)
	extraSiteAdministrator(t, f, "grt-extra-site-admin", personID)

	extraNamespaceAdmin(t, f, "grt-extra-admin", personID)

	// The second run names a namespace, so its admin grant is a required grant
	// that does not exist yet. The failed revoke must leave it missing.
	names := makeNamespaces(t, f, "dev")

	grants := &failingRevokes{DemoGrants: f.store, revoked: nil}

	failing, err := localusersvc.NewDemoSeed(localusersvc.DemoConfig{
		Passwords: f.store, Principals: f.principals, Grants: grants, Namespaces: f.store, Hasher: f.hasher,
		Blocklist: blocklist.Embedded(), Kind: installation.KindDev, Now: nil, Random: nil,
	})
	if err != nil {
		t.Fatalf("new demo seed: %v", err)
	}

	if _, runErr := failing.Run(t.Context(), names); !errors.Is(runErr, errRevokeDown) {
		t.Fatalf("second run = %v, want %v", runErr, errRevokeDown)
	}

	if !personCanSignIn(t, f, first) {
		t.Error("the failed start changed the password")
	}

	checkRevokedOnce(t, f, personID, grants.revoked)

	_, after := demoGrants(t.Context(), t, f)

	required := access.RoleAdmin + " " + identity.NamespaceObject(names[0]).String()
	if slices.Contains(after, required) {
		t.Errorf("grants after the failed start = %v, want no %s", after, required)
	}
}

// auditEntryOf builds a valid audit entry about object for a test change.
func auditEntryOf(object string) audit.Entry {
	return audit.Entry{
		At: time.Now().UTC().Truncate(time.Millisecond), Namespace: audit.ServerScope, Subject: "admin",
		IDPSubject: "", TokenID: "", Action: "server:manage-principals", Object: object, Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange,
		Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}
}
