package storetest

import (
	"errors"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// siteAdministrator makes a person with the site-administrator role on the
// installation and returns the person and the grant.
func (d *delegation) siteAdministrator(t *testing.T, subject string) (principal.Principal, access.Grant) {
	t.Helper()

	p := d.principalOf(t, subject, principal.KindPerson)

	return p, d.mustGrant(t, bootstrap(), p, access.RoleSiteAdministrator, identity.ServerObject())
}

// wantLastSiteAdministrator fails the test unless err reports a change of the
// last site administrator.
func wantLastSiteAdministrator(t *testing.T, what string, err error) {
	t.Helper()

	if !errors.Is(err, access.ErrLastSiteAdministrator) {
		t.Fatalf("%s = %v; want access.ErrLastSiteAdministrator", what, err)
	}
}

// testLastSiteAdministrator proves that the store refuses, in the
// transaction of the change, each change that leaves the installation with
// no site administrator.
func testLastSiteAdministrator(t *testing.T, d *delegation) {
	t.Helper()

	ctx := t.Context()
	admin, grant := d.siteAdministrator(t, "root@example.com")
	managePrincipals := changeEntry(bootstrap(), identity.ActionServerManagePrincipals)

	wantLastSiteAdministrator(t, "delete of the last site administrator",
		d.principals.DeletePrincipal(ctx, managePrincipals, bootstrap(), admin.ID))
	wantLastSiteAdministrator(t, "disable of the last site administrator",
		d.principals.DisablePrincipal(ctx, managePrincipals, bootstrap(), admin.ID))

	_, err := d.access.RevokeByID(
		ctx,
		changeEntry(bootstrap(), identity.ActionServerManageAccess),
		bootstrap(),
		grant.ID,
	)
	wantLastSiteAdministrator(t, "revoke of the last site-administrator grant", err)

	// Every refused change left the administrator as it was.
	got, err := d.principals.GetPrincipal(ctx, admin.ID)
	if err != nil || !got.DisabledAt.IsZero() {
		t.Fatalf("administrator after the refused changes = %+v, %v; want present and enabled", got, err)
	}

	grants, err := d.st.GrantsOf(ctx, access.Subject{Kind: access.SubjectPrincipal, ID: admin.ID})
	if err != nil || len(grants) != 1 {
		t.Fatalf("grants after the refused changes = %v, %v; want the one grant", grants, err)
	}
}

// testLastSiteAdministratorSecond proves that a change works while another
// site administrator remains, and that the remaining one is then protected.
func testLastSiteAdministratorSecond(t *testing.T, d *delegation) {
	t.Helper()

	ctx := t.Context()
	first, _ := d.siteAdministrator(t, "first@example.com")
	second, _ := d.siteAdministrator(t, "second@example.com")
	managePrincipals := changeEntry(bootstrap(), identity.ActionServerManagePrincipals)

	if err := d.principals.DisablePrincipal(ctx, managePrincipals, bootstrap(), first.ID); err != nil {
		t.Fatalf("disable of one of two site administrators: %v", err)
	}

	wantLastSiteAdministrator(t, "delete of the one enabled site administrator",
		d.principals.DeletePrincipal(ctx, managePrincipals, bootstrap(), second.ID))

	// A disabled administrator cannot act, so it does not count.
	if err := d.principals.EnablePrincipal(ctx, managePrincipals, bootstrap(), first.ID); err != nil {
		t.Fatalf("enable: %v", err)
	}

	if err := d.principals.DeletePrincipal(ctx, managePrincipals, bootstrap(), second.ID); err != nil {
		t.Fatalf("delete of one of two site administrators: %v", err)
	}
}

// testLastSiteAdministratorGroup proves that the role through a group counts,
// and that the end of the group, of the membership or of the grant of the
// group is refused when it ends the last site administrator.
func testLastSiteAdministratorGroup(t *testing.T, d *delegation) {
	t.Helper()

	ctx := t.Context()
	admin := d.principalOf(t, "root@example.com", principal.KindPerson)
	group := d.groupOf(t, "site-admins")
	grant := d.grantRoleToGroup(t, group, access.RoleSiteAdministrator, identity.ServerObject())
	d.addMemberOf(t, group, admin)

	manageGroups := changeEntry(bootstrap(), identity.ActionServerManageGroups)

	wantLastSiteAdministrator(t, "removal of the last site administrator from its group",
		d.principals.RemoveMember(ctx, manageGroups, bootstrap(), group.ID, admin.ID))
	wantLastSiteAdministrator(t, "delete of the group of the last site administrator",
		d.principals.DeleteGroup(ctx, manageGroups, bootstrap(), group.ID))
	wantLastSiteAdministrator(t, "delete of the member of the group",
		d.principals.DeletePrincipal(ctx, changeEntry(bootstrap(), identity.ActionServerManagePrincipals),
			bootstrap(), admin.ID))

	_, err := d.access.RevokeByID(
		ctx,
		changeEntry(bootstrap(), identity.ActionServerManageAccess),
		bootstrap(),
		grant.ID,
	)
	wantLastSiteAdministrator(t, "revoke of the grant of the group", err)

	// A direct grant to the same person is a second path: the group can go.
	d.mustGrant(t, bootstrap(), admin, access.RoleSiteAdministrator, identity.ServerObject())

	if err := d.principals.RemoveMember(ctx, manageGroups, bootstrap(), group.ID, admin.ID); err != nil {
		t.Fatalf("removal from the group when a direct grant remains: %v", err)
	}
}

// testNoSiteAdministratorNoRefusal proves that an installation with no site
// administrator at all stays open to changes: the guard refuses only a change
// that takes the last one.
func testNoSiteAdministratorNoRefusal(t *testing.T, d *delegation) {
	t.Helper()

	ctx := t.Context()
	person := d.principalOf(t, "ops@example.com", principal.KindPerson)
	d.mustGrant(t, bootstrap(), person, access.RoleAccountAdministrator, identity.ServerObject())

	err := d.principals.DeletePrincipal(ctx, changeEntry(bootstrap(), identity.ActionServerManagePrincipals),
		bootstrap(), person.ID)
	if err != nil {
		t.Fatalf("delete of an account administrator: %v", err)
	}
}

// testLastSiteAdministratorCountsPersons proves that only a person counts as a
// site administrator. A service principal can hold the role, but every route
// that makes a site administrator needs a fresh console session, which only a
// person has. So the change that takes the last person is refused while a
// service or workload principal still holds the role.
func testLastSiteAdministratorCountsPersons(t *testing.T, d *delegation) {
	t.Helper()

	ctx := t.Context()
	person, _ := d.siteAdministrator(t, "root@example.com")
	managePrincipals := changeEntry(bootstrap(), identity.ActionServerManagePrincipals)

	var service principal.Principal

	for _, kind := range []principal.Kind{principal.KindService, principal.KindWorkload} {
		bot := d.principalOf(t, "bot-"+string(kind), kind)
		d.mustGrant(t, bootstrap(), bot, access.RoleSiteAdministrator, identity.ServerObject())

		if kind == principal.KindService {
			service = bot
		}
	}

	wantLastSiteAdministrator(t, "delete of the last person with a site-administrator service principal",
		d.principals.DeletePrincipal(ctx, managePrincipals, bootstrap(), person.ID))
	wantLastSiteAdministrator(t, "disable of the last person with a site-administrator service principal",
		d.principals.DisablePrincipal(ctx, managePrincipals, bootstrap(), person.ID))

	// A principal that is not a person is not protected by the guard.
	if err := d.principals.DeletePrincipal(ctx, managePrincipals, bootstrap(), service.ID); err != nil {
		t.Fatalf("delete of a service principal that holds the role: %v", err)
	}
}

// AuditWriteHolder is an optional DelegationStore capability for the parallel
// case of the last site administrator. A store that runs one write at a time
// does not implement it.
type AuditWriteHolder interface {
	DelegationStore
	// HoldAuditWrites makes each audit append of the store wait until release
	// runs. Reads of the store do not wait. release can run more than once.
	HoldAuditWrites(t *testing.T) (release func())
	// WaitForLockWaiters returns when n transactions of the store wait for a
	// lock.
	WaitForLockWaiters(t *testing.T, n int)
}

// testLastSiteAdministratorParallel proves that two removals of two site
// administrators that run at the same time leave one: exactly one of them is
// refused. The store serializes the writes, and each count reads the commit of
// the write before it.
//
// When the store is an AuditWriteHolder, the case holds the audit appends
// until both removals wait for a lock. A removal appends its audit entry after
// its counts. So when the store does not serialize the removals, both count
// before either one commits, and the case fails.
func testLastSiteAdministratorParallel(t *testing.T, d *delegation) {
	t.Helper()

	ctx := t.Context()
	first, _ := d.siteAdministrator(t, "first@example.com")
	second, _ := d.siteAdministrator(t, "second@example.com")
	managePrincipals := changeEntry(bootstrap(), identity.ActionServerManagePrincipals)

	var (
		wg      sync.WaitGroup
		results [2]error
		start   = make(chan struct{})
	)

	targets := []string{first.ID, second.ID}

	release := func() {}

	holder, holds := d.st.(AuditWriteHolder)
	if holds {
		release = holder.HoldAuditWrites(t)
	}

	for i := range targets {
		wg.Go(func() {
			<-start

			results[i] = d.principals.DisablePrincipal(ctx, managePrincipals, bootstrap(), targets[i])
		})
	}

	close(start)

	if holds {
		holder.WaitForLockWaiters(t, len(targets))
	}

	release()
	wg.Wait()

	refused := 0

	for _, err := range results {
		switch {
		case err == nil:
		case errors.Is(err, access.ErrLastSiteAdministrator):
			refused++
		default:
			t.Fatalf("parallel disable = %v, want nil or access.ErrLastSiteAdministrator", err)
		}
	}

	if refused != 1 {
		t.Fatalf("parallel disables of two administrators: %d refused (%v), want exactly one", refused, results)
	}
}
