package principaltest

import (
	"fmt"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// workingBreakGlassStore is every port that the count of the working
// break-glass accounts reads or seeds.
type workingBreakGlassStore interface {
	principal.Store
	principal.WorkingBreakGlassCounter
	mfa.PasskeyStore
	access.GrantStore
	namespace.Store
}

// countNamespace holds the namespace principal of the count suite.
const countNamespace = namespace.Name("count-team")

// workingBreakGlassCountCase is one row of the count suite.
type workingBreakGlassCountCase struct {
	seed      func(t *testing.T, s workingBreakGlassStore, n int)
	name      string
	wantDelta int64
}

// testWorkingBreakGlassCount proves which break-glass accounts the count
// includes. Each row seeds its own accounts, so the count grows by the delta
// that the row names.
func testWorkingBreakGlassCount(t *testing.T, s principal.Store) {
	t.Helper()

	store, ok := s.(workingBreakGlassStore)
	if !ok {
		t.Skip("the store keeps no passkeys, grants or namespaces")
	}

	ctx := t.Context()

	if err := store.CreateNamespace(ctx, countNamespaceRecord()); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	for i, tc := range workingBreakGlassCountCases() {
		n := i + 1

		t.Run(tc.name, func(t *testing.T) {
			before, err := store.CountWorkingBreakGlass(ctx, at(59))
			if err != nil {
				t.Fatalf("count before the row: %v", err)
			}

			tc.seed(t, store, n)

			after, err := store.CountWorkingBreakGlass(ctx, at(59))
			if err != nil {
				t.Fatalf("count after the row: %v", err)
			}

			if got := after - before; got != tc.wantDelta {
				t.Fatalf("working break-glass count grew by %d, want %d: %d -> %d", got, tc.wantDelta, before, after)
			}
		})
	}
}

// countNamespaceRecord returns the namespace of the count suite.
func countNamespaceRecord() namespace.Namespace {
	return namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           at(0),
		Name:                countNamespace,
		PrimaryInstallation: "",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
}

// workingBreakGlassCountCases returns every row of the count suite.
func workingBreakGlassCountCases() []workingBreakGlassCountCase {
	out := make([]workingBreakGlassCountCase, 0, 16)

	out = append(out, workingBreakGlassAccountCases()...)
	out = append(out, workingBreakGlassCredentialCases()...)
	out = append(out, workingBreakGlassGroupCases()...)
	out = append(out, workingBreakGlassDirectGrantCases()...)

	return out
}

// workingBreakGlassAccountCases returns the rows of the account properties.
func workingBreakGlassAccountCases() []workingBreakGlassCountCase {
	return []workingBreakGlassCountCase{
		{
			name: "working",
			seed: func(t *testing.T, s workingBreakGlassStore, n int) {
				t.Helper()
				countWorkingAccount(t, s, countID("prn-working", n))
			},
			wantDelta: 1,
		},
		{
			name: "working through the account-administrator role",
			seed: func(t *testing.T, s workingBreakGlassStore, n int) {
				t.Helper()

				id := countID("prn-account", n)
				countBreakGlass(t, s, id, principal.SourceLocal)
				countPasskey(t, s, id, countID("pky-account", n))
				countGrant(t, s, installationGrant(countID("grt-account", n),
					access.Subject{Kind: access.SubjectPrincipal, ID: id},
					access.RoleAccountAdministrator))
			},
			wantDelta: 1,
		},
		{
			name: "disabled",
			seed: func(t *testing.T, s workingBreakGlassStore, n int) {
				t.Helper()
				countDisabled(t, s, n)
			},
			wantDelta: 0,
		},
		{
			name: "deleted",
			seed: func(t *testing.T, s workingBreakGlassStore, n int) {
				t.Helper()
				countDeleted(t, s, n)
			},
			wantDelta: 0,
		},
		{
			name: "not break-glass",
			seed: func(t *testing.T, s workingBreakGlassStore, n int) {
				t.Helper()

				id := countID("prn-person", n)
				mustCreatePrincipal(t, s, newPrincipal(id, id+"@example.com", principal.KindPerson))
				countPasskey(t, s, id, countID("pky-person", n))
				countGrant(t, s, installationGrant(countID("grt-person", n),
					access.Subject{Kind: access.SubjectPrincipal, ID: id},
					access.RoleSiteAdministrator))
			},
			wantDelta: 0,
		},
		{
			name: "source not local",
			seed: func(t *testing.T, s workingBreakGlassStore, n int) {
				t.Helper()

				id := countID("prn-ldap", n)
				countBreakGlass(t, s, id, principal.SourceLDAP)
				countPasskey(t, s, id, countID("pky-ldap", n))
				countGrant(t, s, installationGrant(countID("grt-ldap", n),
					access.Subject{Kind: access.SubjectPrincipal, ID: id},
					access.RoleSiteAdministrator))
			},
			wantDelta: 0,
		},
		{
			name: "a namespace principal",
			seed: func(t *testing.T, s workingBreakGlassStore, n int) {
				t.Helper()

				id := countID("prn-namespace", n)

				p := newPrincipal(id, id+"@example.com", principal.KindPerson)
				p.BreakGlass = true
				p.Namespace = countNamespace

				err := s.CreateNamespacePrincipal(t.Context(), p, countFence(), nsEntry(countNamespace))
				if err != nil {
					t.Fatalf("create namespace principal: %v", err)
				}

				countPasskey(t, s, id, countID("pky-namespace", n))
				countGrant(t, s, namespaceGrant(countID("grt-namespace", n),
					access.Subject{Kind: access.SubjectPrincipal, ID: id},
					access.RoleSiteAdministrator))
			},
			wantDelta: 0,
		},
	}
}

// workingBreakGlassCredentialCases returns the rows of the credential
// properties.
func workingBreakGlassCredentialCases() []workingBreakGlassCountCase {
	return []workingBreakGlassCountCase{
		{
			name: "no passkey",
			seed: func(t *testing.T, s workingBreakGlassStore, n int) {
				t.Helper()

				id := countID("prn-no-key", n)
				countBreakGlass(t, s, id, principal.SourceLocal)
				countGrant(t, s, installationGrant(countID("grt-no-key", n),
					access.Subject{Kind: access.SubjectPrincipal, ID: id},
					access.RoleSiteAdministrator))
			},
			wantDelta: 0,
		},
		{
			name: "only a suspended passkey",
			seed: func(t *testing.T, s workingBreakGlassStore, n int) {
				t.Helper()

				id := countID("prn-suspended", n)
				countBreakGlass(t, s, id, principal.SourceLocal)
				countSuspendedPasskey(t, s, id, countID("pky-suspended", n))
				countGrant(t, s, installationGrant(countID("grt-suspended", n),
					access.Subject{Kind: access.SubjectPrincipal, ID: id},
					access.RoleSiteAdministrator))
			},
			wantDelta: 0,
		},
	}
}

// workingBreakGlassGroupCases returns the rows of the group grant properties.
func workingBreakGlassGroupCases() []workingBreakGlassCountCase {
	return []workingBreakGlassCountCase{
		{
			name: "a grant through an active group",
			seed: func(t *testing.T, s workingBreakGlassStore, n int) {
				t.Helper()

				id := countID("prn-group", n)
				countBreakGlass(t, s, id, principal.SourceLocal)
				countPasskey(t, s, id, countID("pky-group", n))

				groupID := countID("grp-active", n)
				mustCreateGroup(t, s, newGroup(groupID, countID("Active ", n)))
				mustAddMember(t, s, groupID, id)
				countGrant(t, s, installationGrant(countID("grt-group", n),
					access.Subject{Kind: access.SubjectGroup, ID: groupID},
					access.RoleSiteAdministrator))
			},
			wantDelta: 1,
		},
		{
			name: "a grant through a removed membership",
			seed: func(t *testing.T, s workingBreakGlassStore, n int) {
				t.Helper()

				countKeeper(t, s, countID("prn-keeper-removed", n))

				id := countID("prn-removed", n)
				countBreakGlass(t, s, id, principal.SourceLocal)
				countPasskey(t, s, id, countID("pky-removed", n))

				groupID := countID("grp-removed", n)
				mustCreateGroup(t, s, newGroup(groupID, countID("Removed ", n)))
				mustAddMember(t, s, groupID, id)
				countGrant(t, s, installationGrant(countID("grt-removed", n),
					access.Subject{Kind: access.SubjectGroup, ID: groupID},
					access.RoleSiteAdministrator))

				if err := s.RemoveMember(t.Context(), groupID, id, entry()); err != nil {
					t.Fatalf("remove membership: %v", err)
				}
			},
			wantDelta: 0,
		},
		{
			name: "a grant through a deleted group",
			seed: func(t *testing.T, s workingBreakGlassStore, n int) {
				t.Helper()

				countKeeper(t, s, countID("prn-keeper-group", n))

				id := countID("prn-deleted-group", n)
				countBreakGlass(t, s, id, principal.SourceLocal)
				countPasskey(t, s, id, countID("pky-deleted-group", n))

				groupID := countID("grp-deleted", n)
				mustCreateGroup(t, s, newGroup(groupID, countID("Deleted ", n)))
				mustAddMember(t, s, groupID, id)
				countGrant(t, s, installationGrant(countID("grt-deleted-group", n),
					access.Subject{Kind: access.SubjectGroup, ID: groupID},
					access.RoleSiteAdministrator))

				if err := s.DeleteGroup(t.Context(), groupID, entry()); err != nil {
					t.Fatalf("delete group: %v", err)
				}
			},
			wantDelta: 0,
		},
	}
}

// workingBreakGlassDirectGrantCases returns the rows of the direct grant
// properties.
func workingBreakGlassDirectGrantCases() []workingBreakGlassCountCase {
	return []workingBreakGlassCountCase{
		{
			name: "a revoked grant",
			seed: func(t *testing.T, s workingBreakGlassStore, n int) {
				t.Helper()

				countKeeper(t, s, countID("prn-keeper-revoked", n))

				id := countID("prn-revoked", n)
				countBreakGlass(t, s, id, principal.SourceLocal)
				countPasskey(t, s, id, countID("pky-revoked", n))

				grantID := countID("grt-revoked", n)
				countGrant(t, s, installationGrant(grantID,
					access.Subject{Kind: access.SubjectPrincipal, ID: id},
					access.RoleSiteAdministrator))

				if err := s.RevokeGrant(t.Context(), grantID, installationFence(), entry(), nil); err != nil {
					t.Fatalf("revoke grant: %v", err)
				}
			},
			wantDelta: 0,
		},
		{
			name: "a grant with a condition",
			seed: func(t *testing.T, s workingBreakGlassStore, n int) {
				t.Helper()

				id := countID("prn-condition", n)
				countBreakGlass(t, s, id, principal.SourceLocal)
				countPasskey(t, s, id, countID("pky-condition", n))

				condition := "request.time < timestamp(\"2026-01-01T00:00:00Z\")"

				grant := installationGrant(countID("grt-condition", n),
					access.Subject{Kind: access.SubjectPrincipal, ID: id},
					access.RoleSiteAdministrator)
				grant.Condition = &condition

				countGrant(t, s, grant)
			},
			wantDelta: 0,
		},
		{
			name: "a grant of another role",
			seed: func(t *testing.T, s workingBreakGlassStore, n int) {
				t.Helper()

				id := countID("prn-role", n)
				countBreakGlass(t, s, id, principal.SourceLocal)
				countPasskey(t, s, id, countID("pky-role", n))
				countGrant(t, s, installationGrant(countID("grt-role", n),
					access.Subject{Kind: access.SubjectPrincipal, ID: id},
					access.RoleViewer))
			},
			wantDelta: 0,
		},
		{
			name: "a grant on a namespace",
			seed: func(t *testing.T, s workingBreakGlassStore, n int) {
				t.Helper()

				id := countID("prn-ns-grant", n)
				countBreakGlass(t, s, id, principal.SourceLocal)
				countPasskey(t, s, id, countID("pky-ns-grant", n))
				countGrant(t, s, namespaceGrant(countID("grt-ns-grant", n),
					access.Subject{Kind: access.SubjectPrincipal, ID: id},
					access.RoleSiteAdministrator))
			},
			wantDelta: 0,
		},
	}
}

// countID returns the ID of the fixture n with prefix.
func countID(prefix string, n int) string {
	return fmt.Sprintf("%s-%d", prefix, n)
}

// countBreakGlass stores an enabled break-glass person with source.
func countBreakGlass(t *testing.T, s workingBreakGlassStore, id string, source principal.Source) {
	t.Helper()

	p := newPrincipal(id, id+"@example.com", principal.KindPerson)
	p.BreakGlass = true
	p.Source = source

	mustCreatePrincipal(t, s, p)
}

// countWorkingAccount stores a working break-glass account: an enabled local
// break-glass person with a passkey and a direct grant of the
// site-administrator role on the installation.
func countWorkingAccount(t *testing.T, s workingBreakGlassStore, id string) {
	t.Helper()

	countBreakGlass(t, s, id, principal.SourceLocal)
	countPasskey(t, s, id, "pky-"+id)
	countGrant(t, s, installationGrant("grt-"+id,
		access.Subject{Kind: access.SubjectPrincipal, ID: id},
		access.RoleSiteAdministrator))
}

// countKeeper stores a person that keeps the last-site-administrator rule
// satisfied while a row removes the grant of its own account.
func countKeeper(t *testing.T, s workingBreakGlassStore, id string) {
	t.Helper()

	mustCreatePrincipal(t, s, newPrincipal(id, id+"@example.com", principal.KindPerson))
	countGrant(t, s, installationGrant("grt-"+id,
		access.Subject{Kind: access.SubjectPrincipal, ID: id},
		access.RoleSiteAdministrator))
}

// countDisabled stores a working break-glass account and disables it, with a
// second enabled break-glass account and a keeper of the
// site-administrator rule.
func countDisabled(t *testing.T, s workingBreakGlassStore, n int) {
	t.Helper()

	countKeeper(t, s, countID("prn-keeper-disabled", n))
	countBreakGlass(t, s, countID("prn-other-disabled", n), principal.SourceLocal)

	id := countID("prn-disabled", n)
	countWorkingAccount(t, s, id)

	if err := s.DisablePrincipal(t.Context(), id, entry()); err != nil {
		t.Fatalf("disable break-glass account: %v", err)
	}
}

// countDeleted stores a working break-glass account and deletes it, with a
// second enabled break-glass account and a keeper of the
// site-administrator rule.
func countDeleted(t *testing.T, s workingBreakGlassStore, n int) {
	t.Helper()

	countKeeper(t, s, countID("prn-keeper-deleted", n))
	countBreakGlass(t, s, countID("prn-other-deleted", n), principal.SourceLocal)

	id := countID("prn-deleted", n)
	countWorkingAccount(t, s, id)

	if err := s.DeletePrincipal(t.Context(), id, entry()); err != nil {
		t.Fatalf("delete break-glass account: %v", err)
	}
}

// countPasskey adds a passkey to personID.
func countPasskey(t *testing.T, s workingBreakGlassStore, personID, id string) {
	t.Helper()

	p := mfa.Passkey{
		CreatedAt:      at(1),
		LastUsedAt:     time.Time{},
		SuspendedAt:    time.Time{},
		ID:             id,
		PersonID:       personID,
		Name:           "key-" + personID,
		CredentialID:   []byte("credential-" + id),
		PublicKey:      []byte("public-key-" + id),
		Transports:     nil,
		AAGUID:         [16]byte{},
		SignCount:      2,
		BackupEligible: false,
	}

	if err := s.AddPasskey(t.Context(), p, entry()); err != nil {
		t.Fatalf("add passkey %q: %v", id, err)
	}
}

// countSuspendedPasskey adds a passkey and suspends it with a counter that
// goes back.
func countSuspendedPasskey(t *testing.T, s workingBreakGlassStore, personID, id string) {
	t.Helper()

	countPasskey(t, s, personID, id)

	err := s.RecordUse(t.Context(), mfa.PasskeyUse{At: at(2), PasskeyID: id, SignCount: 1}, entry())
	wantErr(t, "suspend passkey "+id, err, mfa.ErrCounterRegressed)
}

// countGrantSpec is one grant that the count suite seeds.
type countGrantSpec struct {
	ID        string
	Subject   access.Subject
	Role      string
	Object    identity.Object
	Fence     namespace.Fence
	Condition *string
}

// installationGrant returns a grant on the installation with no condition.
func installationGrant(id string, subject access.Subject, role string) countGrantSpec {
	return countGrantSpec{
		ID: id, Subject: subject, Role: role, Object: identity.ServerObject(),
		Fence: installationFence(), Condition: nil,
	}
}

// namespaceGrant returns a grant on the count namespace with no condition.
func namespaceGrant(id string, subject access.Subject, role string) countGrantSpec {
	return countGrantSpec{
		ID: id, Subject: subject, Role: role, Object: identity.NamespaceObject(countNamespace),
		Fence: countFence(), Condition: nil,
	}
}

// installationFence returns the zero fence of a grant on the installation.
func installationFence() namespace.Fence {
	var fence namespace.Fence

	return fence
}

// countFence returns the fence of the count namespace.
func countFence() namespace.Fence {
	return namespace.Fence{Namespace: countNamespace, FailoverVersion: 1}
}

// countGrant adds the grant g.
func countGrant(t *testing.T, s workingBreakGlassStore, g countGrantSpec) {
	t.Helper()

	grant := access.Grant{
		CreatedAt: at(3),
		Condition: g.Condition,
		Object:    g.Object,
		ID:        g.ID,
		Role:      g.Role,
		CreatedBy: "admin",
		Subject:   g.Subject,
	}

	e := entry()
	e.Object = g.Object.String()

	if ns := g.Object.Namespace(); ns != "" {
		e.Namespace = ns
	}

	if err := s.CreateGrant(t.Context(), grant, g.Fence, e, nil); err != nil {
		t.Fatalf("create grant %q: %v", g.ID, err)
	}
}
