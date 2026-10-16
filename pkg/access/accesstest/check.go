package accesstest

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// errCheck is the error of a check that refuses its write.
var errCheck = errors.New("check refused the write")

// refuse is a check that refuses every write.
func refuse(context.Context, access.GrantReads) error {
	return errCheck
}

// checkCases are the cases of the check that a grant write and a role write
// run in their own transaction.
func checkCases() []struct {
	test func(t *testing.T, s access.Store)
	name string
} {
	return []struct {
		test func(t *testing.T, s access.Store)
		name string
	}{
		{name: "a grant that its check refuses changes nothing", test: testGrantCheckRefuses},
		{name: "a role update that its check refuses changes nothing", test: testRoleCheckRefuses},
		{name: "a revoke that its check refuses changes nothing", test: testRevokeCheckRefuses},
		{name: "a grant check reads the current grants, roles and subjects", test: testGrantCheckReads},
		{name: "a role check reads where the role is granted", test: testRoleCheckReads},
	}
}

// testGrantCheckRefuses checks that a grant whose check fails stores
// nothing and returns the error of the check, on the installation and
// inside a namespace.
func testGrantCheckRefuses(t *testing.T, s access.Store) {
	t.Helper()

	ctx := t.Context()
	objects := []identity.Object{identity.ServerObject(), identity.NamespaceObject("acme")}

	for i := range objects {
		obj := objects[i]
		ns := obj.Namespace()
		g := newGrant("grt-"+obj.String(), principalSubject("prn-a"), access.RoleViewer, obj)

		err := s.CreateGrant(ctx, g, fenceOf(t, s, ns), scoped(entry(), ns), refuse)
		if !errors.Is(err, errCheck) {
			t.Fatalf("grant on %q with a refusing check: err = %v, want the error of the check", obj.String(), err)
		}

		if _, err := s.GetGrant(ctx, g.ID); !errors.Is(err, access.ErrNotFound) {
			t.Fatalf("get refused grant on %q: err = %v, want ErrNotFound", obj.String(), err)
		}

		if err := s.CreateGrant(ctx, g, fenceOf(t, s, ns), scoped(entry(), ns), nil); err != nil {
			t.Fatalf("grant on %q after the refused grant: %v", obj.String(), err)
		}
	}
}

// testRevokeCheckRefuses checks that a revoke whose check fails keeps the
// grant current and returns the error of the check, on the installation
// and inside a namespace.
func testRevokeCheckRefuses(t *testing.T, s access.Store) {
	t.Helper()

	ctx := t.Context()
	objects := []identity.Object{identity.ServerObject(), identity.NamespaceObject("acme")}

	for i := range objects {
		obj := objects[i]
		ns := obj.Namespace()
		g := newGrant("grt-"+obj.String(), principalSubject("prn-a"), access.RoleViewer, obj)

		if err := createGrant(t, s, g, entry()); err != nil {
			t.Fatalf("create grant on %q: %v", obj.String(), err)
		}

		err := s.RevokeGrant(ctx, g.ID, fenceOf(t, s, ns), scoped(entry(), ns), refuse)
		if !errors.Is(err, errCheck) {
			t.Fatalf("revoke on %q with a refusing check: err = %v, want the error of the check", obj.String(), err)
		}

		if _, err := s.GetGrant(ctx, g.ID); err != nil {
			t.Fatalf("get grant on %q after a refused revoke: %v", obj.String(), err)
		}
	}
}

// testRoleCheckRefuses checks that a role update whose check fails keeps
// the role as it was and returns the error of the check.
func testRoleCheckRefuses(t *testing.T, s access.Store) {
	t.Helper()

	ctx := t.Context()
	before := newRole("rol-1", "Deployer", identity.ActionUnitRead)
	mustCreateRole(t, s, before)

	after := before
	after.Name = "Releaser"
	after.Actions = []identity.Action{identity.ActionUnitRead, identity.ActionUnitWrite}
	after.UpdatedAt = at(2)

	if err := s.UpdateRole(ctx, after, entry(), refuse); !errors.Is(err, errCheck) {
		t.Fatalf("update role with a refusing check: err = %v, want the error of the check", err)
	}

	got, err := s.GetRole(ctx, before.ID)
	if err != nil {
		t.Fatalf("get role: %v", err)
	}

	if got.Name != before.Name || !slices.Equal(got.Actions, before.Actions) {
		t.Fatalf("role after a refused update = %q %v, want %q %v", got.Name, got.Actions, before.Name, before.Actions)
	}
}

// testGrantCheckReads checks that the reads of a grant check see the
// current grants, roles and subjects, and not the grant that the write
// will store.
func testGrantCheckReads(t *testing.T, s access.Store) {
	t.Helper()

	ps, isPrincipals := s.(principal.Store)
	if !isPrincipals {
		t.Skip("the store keeps no principals")
	}

	ctx := t.Context()

	seedCaller(t, ps)

	role := newRole("rol-1", "Deployer", identity.ActionUnitRead)
	mustCreateRole(t, s, role)

	held := newGrant("grt-held", opsGroup1(), role.ID, identity.ServerObject())
	revoked := newGrant("grt-revoked", principalSubject("prn-a"), access.RoleViewer, identity.ServerObject())

	seeded := []access.Grant{held, revoked}
	for i := range seeded {
		if err := createGrant(t, s, seeded[i], entry()); err != nil {
			t.Fatalf("create grant %s: %v", seeded[i].ID, err)
		}
	}

	if err := revokeGrant(t, s, revoked.ID, "", entry()); err != nil {
		t.Fatalf("revoke grant: %v", err)
	}

	ran := false
	check := func(ctx context.Context, r access.GrantReads) error {
		ran = true

		return assertAliceReads(ctx, t, r, held, role)
	}

	g := newGrant("grt-new", principalSubject("prn-a"), access.RoleViewer, identity.ServerObject())
	if err := s.CreateGrant(ctx, g, fenceOf(t, s, ""), entry(), check); err != nil {
		t.Fatalf("grant with a check: %v", err)
	}

	if !ran {
		t.Fatal("the store did not run the check of the grant")
	}

	if _, err := s.GetGrant(ctx, g.ID); err != nil {
		t.Fatalf("get checked grant: %v", err)
	}
}

// assertAliceReads checks that r reads the subjects of alice with the group
// grp-1, held as the only grant of alice on the installation, and role.
func assertAliceReads(
	ctx context.Context, t *testing.T, r access.GrantReads, held access.Grant, role access.Role,
) error {
	t.Helper()

	subjects, err := r.SubjectsOf(ctx, "alice")
	if err != nil {
		return err
	}

	if !slices.Contains(subjects, opsGroup1()) {
		t.Errorf("subjects of alice in the check = %v, want the group grp-1", subjects)
	}

	grants, err := r.GrantsOn(ctx, subjects, []identity.Object{identity.ServerObject()})
	if err != nil {
		return err
	}

	if ids := grantIDs(grants); !slices.Equal(ids, []string{held.ID}) {
		t.Errorf("grants of alice in the check = %v, want %v", ids, []string{held.ID})
	}

	roles, err := r.GetRoles(ctx, []string{role.ID, "rol-missing"})
	if err != nil {
		return err
	}

	if len(roles) != 1 || roles[0].ID != role.ID || !slices.Equal(roles[0].Actions, role.Actions) {
		t.Errorf("roles in the check = %+v, want %s only", roles, role.ID)
	}

	return nil
}

// testRoleCheckReads checks that the reads of a role check see where the
// role is granted.
func testRoleCheckReads(t *testing.T, s access.Store) {
	t.Helper()

	below := newRole("rol-below", "Below", identity.ActionUnitRead)
	onPrincipal := newRole("rol-principal", "OnPrincipal", identity.ActionPrincipalManageTokens)
	mustCreateRole(t, s, below)
	mustCreateRole(t, s, onPrincipal)

	grants := []access.Grant{
		newGrant("grt-1", principalSubject("prn-a"), below.ID, identity.NamespaceObject("acme")),
		newGrant("grt-2", principalSubject("prn-a"), onPrincipal.ID, identity.PrincipalObject("prn-svc")),
	}
	for i := range grants {
		if err := createGrant(t, s, grants[i], entry()); err != nil {
			t.Fatalf("create grant %s: %v", grants[i].ID, err)
		}
	}

	assertRoleReads(t, s, below, roleUse{below: true, onPrincipal: false})
	assertRoleReads(t, s, onPrincipal, roleUse{below: false, onPrincipal: true})
}

// roleUse is where a role is granted: below the installation, and on a
// principal.
type roleUse struct {
	below       bool
	onPrincipal bool
}

// assertRoleReads updates role with a check, and checks that the check
// reads want: whether the role is granted below the installation and on a
// principal.
func assertRoleReads(t *testing.T, s access.Store, role access.Role, want roleUse) {
	t.Helper()

	ran := false
	check := func(ctx context.Context, r access.GrantReads) error {
		ran = true

		var (
			got roleUse
			err error
		)

		if got.below, err = r.RoleGrantedBelowInstallation(ctx, role.ID); err != nil {
			return err
		}

		if got.onPrincipal, err = r.RoleGrantedOnPrincipal(ctx, role.ID); err != nil {
			return err
		}

		if got != want {
			t.Errorf("role %s in the check = %+v, want %+v", role.ID, got, want)
		}

		return nil
	}

	updated := role
	updated.Description = "Updated"
	updated.UpdatedAt = at(2)

	if err := s.UpdateRole(t.Context(), updated, entry(), check); err != nil {
		t.Fatalf("update role %s with a check: %v", role.ID, err)
	}

	if !ran {
		t.Fatalf("the store did not run the check of the role %s", role.ID)
	}
}

// opsGroup1 is the group grp-1 that seedCaller makes alice a member of.
func opsGroup1() access.Subject {
	return access.Subject{Kind: access.SubjectGroup, ID: "grp-1"}
}

// PairFactory returns two handles on one fresh, empty database for one
// subtest: two stores that a server and its second replica would open.
type PairFactory func(t *testing.T) (access.Store, access.Store)

// waitWindow is how long a concurrent write must stay blocked while a
// check runs. A write that does not wait for the lock ends in a few
// milliseconds.
const waitWindow = 300 * time.Millisecond

// endWindow bounds the wait for the blocked write after the first write
// ends.
const endWindow = 10 * time.Second

// RunConcurrent executes the cases of a check that races a write on another
// handle: a revoke of the grant of the granter, and a role update. A check
// runs after its write takes the lock that the racing write takes too, so
// the racing write waits until the checked write ends. A racing write that
// commits first is visible to the check.
func RunConcurrent(t *testing.T, newPair PairFactory) {
	t.Helper()

	cases := []struct {
		test func(t *testing.T, a, b access.Store)
		name string
	}{
		{name: "a revoke on the installation waits for a grant check", test: testRevokeWaits("")},
		{name: "a revoke in a namespace waits for a grant check", test: testRevokeWaits("acme")},
		{name: "a grant check sees a revoke that committed first", test: testCheckSeesRevoke},
		{name: "a grant waits for a role check", test: testGrantWaitsForRoleCheck},
		{name: "a role update waits for a grant check", test: testRoleUpdateWaitsForGrantCheck},
		{name: "a revoke of the caller's grant waits for a revoke check", test: testRevokeWaitsForRevokeCheck},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a, b := newPair(t)
			tc.test(t, a, b)
		})
	}
}

// heldGrant is the grant of the granter that a racing write revokes.
func heldGrant(ns namespace.Name) access.Grant {
	obj := identity.ServerObject()
	if ns != "" {
		obj = identity.NamespaceObject(ns)
	}

	return newGrant("grt-held", principalSubject("prn-granter"), access.RoleNamespaceAdmin, obj)
}

// heldBy is a check that refuses the write unless the grant held is
// current, as an escalation check of the Service does.
func heldBy(held access.Grant) access.GrantCheck {
	return func(ctx context.Context, r access.GrantReads) error {
		grants, err := r.GrantsOn(ctx, []access.Subject{held.Subject}, []identity.Object{held.Object})
		if err != nil {
			return err
		}

		if !slices.ContainsFunc(grants, func(g access.Grant) bool { return g.ID == held.ID }) {
			return access.ErrEscalation
		}

		return nil
	}
}

// blocking returns a check that runs race once on its first run, checks
// that race does not end within waitWindow, then runs then. It returns the
// channel that gets the error of race when race ends.
func blocking(t *testing.T, race func() error, then access.GrantCheck) (access.GrantCheck, <-chan error) {
	t.Helper()

	done := make(chan error, 1)
	started := false

	return func(ctx context.Context, r access.GrantReads) error {
		if !started {
			started = true

			go func() { done <- race() }()

			select {
			case err := <-done:
				t.Errorf("the racing write ended while the check ran (err = %v); it must wait for the lock", err)
				done <- err
			case <-time.After(waitWindow):
			}
		}

		if then == nil {
			return nil
		}

		return then(ctx, r)
	}, done
}

// awaitRace returns the error of the racing write, and fails t when it
// does not end within endWindow.
func awaitRace(t *testing.T, done <-chan error) error {
	t.Helper()

	select {
	case err := <-done:
		return err
	case <-time.After(endWindow):
		t.Fatal("the racing write did not end after the checked write")

		return nil
	}
}

// testRevokeWaits checks that a revoke of the grant of the granter, on
// handle b, waits for a grant write on handle a while its check runs: the
// grant commits before the revoke, never after it.
func testRevokeWaits(ns namespace.Name) func(t *testing.T, a, b access.Store) {
	return func(t *testing.T, a, b access.Store) {
		t.Helper()

		ctx := t.Context()
		held := heldGrant(ns)
		fence := fenceOf(t, a, ns)

		if err := createGrant(t, a, held, entry()); err != nil {
			t.Fatalf("create held grant: %v", err)
		}

		check, done := blocking(t, func() error {
			return b.RevokeGrant(ctx, held.ID, fence, scoped(entry(), ns), nil)
		}, heldBy(held))

		g := newGrant("grt-new", principalSubject("prn-a"), access.RoleViewer, identity.ServerObject())
		if err := a.CreateGrant(ctx, g, fenceOf(t, a, ""), entry(), check); err != nil {
			t.Fatalf("grant while the held grant is current: %v", err)
		}

		if err := awaitRace(t, done); err != nil {
			t.Fatalf("revoke after the grant: %v", err)
		}

		if _, err := a.GetGrant(ctx, held.ID); !errors.Is(err, access.ErrNotFound) {
			t.Fatalf("held grant after its revoke: err = %v, want ErrNotFound", err)
		}
	}
}

// testCheckSeesRevoke checks that a grant check on handle a sees a revoke
// that committed first on handle b, so it refuses the grant.
func testCheckSeesRevoke(t *testing.T, a, b access.Store) {
	t.Helper()

	ctx := t.Context()
	held := heldGrant("")

	if err := createGrant(t, a, held, entry()); err != nil {
		t.Fatalf("create held grant: %v", err)
	}

	if err := revokeGrant(t, b, held.ID, "", entry()); err != nil {
		t.Fatalf("revoke held grant: %v", err)
	}

	g := newGrant("grt-new", principalSubject("prn-a"), access.RoleViewer, identity.ServerObject())
	if err := a.CreateGrant(ctx, g, fenceOf(t, a, ""), entry(), heldBy(held)); !errors.Is(err, access.ErrEscalation) {
		t.Fatalf("grant after the revoke of the held grant: err = %v, want ErrEscalation", err)
	}

	if _, err := a.GetGrant(ctx, g.ID); !errors.Is(err, access.ErrNotFound) {
		t.Fatalf("get refused grant: err = %v, want ErrNotFound", err)
	}
}

// testGrantWaitsForRoleCheck checks that a grant of a role on handle b
// waits for an update of that role on handle a while its check runs. The
// check then reads no grant of the role below the installation, and the
// grant commits after the update.
func testGrantWaitsForRoleCheck(t *testing.T, a, b access.Store) {
	t.Helper()

	ctx := t.Context()
	role := newRole("rol-1", "Deployer", identity.ActionUnitRead)
	mustCreateRole(t, a, role)

	g := newGrant("grt-1", principalSubject("prn-a"), role.ID, identity.NamespaceObject("acme"))
	fence := fenceOf(t, a, "acme")

	check, done := blocking(t, func() error {
		return b.CreateGrant(ctx, g, fence, scoped(entry(), "acme"), nil)
	}, func(ctx context.Context, r access.GrantReads) error {
		below, err := r.RoleGrantedBelowInstallation(ctx, role.ID)
		if err != nil {
			return err
		}

		if below {
			return access.ErrInUse
		}

		return nil
	})

	updated := role
	updated.Description = "Updated"
	updated.UpdatedAt = at(2)

	if err := a.UpdateRole(ctx, updated, entry(), check); err != nil {
		t.Fatalf("update role while no grant holds it: %v", err)
	}

	if err := awaitRace(t, done); err != nil {
		t.Fatalf("grant after the role update: %v", err)
	}
}

// testRoleUpdateWaitsForGrantCheck checks that an update of a role on
// handle b waits for a grant of that role on handle a while its check
// runs: the check reads the role as it was, and the update commits after
// the grant.
func testRoleUpdateWaitsForGrantCheck(t *testing.T, a, b access.Store) {
	t.Helper()

	ctx := t.Context()
	role := newRole("rol-1", "Deployer", identity.ActionUnitRead)
	mustCreateRole(t, a, role)

	updated := role
	updated.Actions = []identity.Action{identity.ActionServerManageAccess}
	updated.UpdatedAt = at(2)

	check, done := blocking(t, func() error {
		return b.UpdateRole(ctx, updated, entry(), nil)
	}, func(ctx context.Context, r access.GrantReads) error {
		roles, err := r.GetRoles(ctx, []string{role.ID})
		if err != nil {
			return err
		}

		if len(roles) != 1 || !slices.Equal(roles[0].Actions, role.Actions) {
			return access.ErrInvalid
		}

		return nil
	})

	g := newGrant("grt-1", principalSubject("prn-a"), role.ID, identity.NamespaceObject("acme"))
	if err := a.CreateGrant(ctx, g, fenceOf(t, a, "acme"), scoped(entry(), "acme"), check); err != nil {
		t.Fatalf("grant of the role before its update: %v", err)
	}

	if err := awaitRace(t, done); err != nil {
		t.Fatalf("role update after the grant: %v", err)
	}
}

// testRevokeWaitsForRevokeCheck checks that a revoke of the grant of the
// caller, on handle b, waits for a revoke of another grant on handle a
// while its check runs: the checked revoke commits before the revoke of
// the caller's grant, never after it.
func testRevokeWaitsForRevokeCheck(t *testing.T, a, b access.Store) {
	t.Helper()

	ctx := t.Context()
	held := heldGrant("acme")
	fence := fenceOf(t, a, "acme")
	target := newGrant("grt-target", principalSubject("prn-a"), access.RoleViewer, identity.NamespaceObject("acme"))

	seeded := []access.Grant{held, target}
	for i := range seeded {
		if err := createGrant(t, a, seeded[i], entry()); err != nil {
			t.Fatalf("create grant %s: %v", seeded[i].ID, err)
		}
	}

	check, done := blocking(t, func() error {
		return b.RevokeGrant(ctx, held.ID, fence, scoped(entry(), "acme"), nil)
	}, heldBy(held))

	if err := a.RevokeGrant(ctx, target.ID, fence, scoped(entry(), "acme"), check); err != nil {
		t.Fatalf("revoke while the held grant is current: %v", err)
	}

	if err := awaitRace(t, done); err != nil {
		t.Fatalf("revoke of the held grant after the checked revoke: %v", err)
	}
}
