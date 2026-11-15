package storetest

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/stack"
)

// testRestoreKeepsAccessTables proves that a restore without
// server:restore-access keeps the current access tables and restores the
// other data, while a restore with it brings the access tables back. It
// takes a backup, changes every access table, restores the backup keeping
// the access tables, and proves the changes stay while the other data is
// restored. It then restores the same backup replacing the access tables
// and proves the backup values are back. Stores without the principal,
// token, access, session and portable ports skip it: the fake of this
// package is one.
func testRestoreKeepsAccessTables(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)
	ports := keepAccessStores(t, s)

	seed := seedBackupStore(t, s)
	createKeepSession(t, ports)
	putKeepMapping(t, ports.mappings, "backup-team")
	backupAccess := readBackupAccess(t, ports)

	sink, sum, files := exportPortable(t, ports.stager)

	gitHost := keepGitHostStores(s, seed.acme.Name)

	mutateAccessForKeepTest(t, s, ports)
	mutateKeepGitHost(t, gitHost)
	extra := mustCreateStack(t, s, seed.acme, "extra")

	stageKeepBackup(t, ports.stager, sink, sum, files)

	if _, err := ports.keeper.ApplyStagingKeepingAccess(t.Context()); err != nil {
		t.Fatalf("apply keeping the access tables: %v", err)
	}

	checkKeptAccess(t, ports, backupAccess)
	checkKeptGitHost(t, gitHost)
	checkKeptStacks(t, s, &seed, extra)

	stageKeepBackup(t, ports.stager, sink, sum, files)

	if err := s.ApplyStaging(t.Context()); err != nil {
		t.Fatalf("apply replacing the access tables: %v", err)
	}

	checkReplacedAccess(t, ports, backupAccess)
	checkReplacedGitHost(t, gitHost)
}

// keepAccessPorts holds the ports that a keep-access restore needs.
type keepAccessPorts struct {
	stager     PortableRestoreStore
	keeper     backup.AccessKeepingApplier
	tokens     accesstoken.Store
	grants     access.Store
	principals principal.Store
	sessions   session.Store
	sessionPol session.PolicyStore
	mappings   principal.GroupMappingStore
	providers  keepProviderPorts
}

// keepAccessStores returns the ports that a keep-access restore needs, or
// skips when the store misses one.
func keepAccessStores(t *testing.T, s RestoreStore) keepAccessPorts {
	t.Helper()

	ps, ok := s.(PortableRestoreStore)
	if !ok {
		t.Skip("the store has no portable export and stage")
	}

	keeper, ok := s.(backup.AccessKeepingApplier)
	if !ok {
		t.Skip("the store keeps no access tables on restore")
	}

	principals, ok := s.(principal.Store)
	if !ok {
		t.Skip("the store keeps no principals")
	}

	tokens, ok := s.(accesstoken.Store)
	if !ok {
		t.Skip("the store keeps no access tokens")
	}

	grants, ok := s.(access.Store)
	if !ok {
		t.Skip("the store keeps no grants")
	}

	sessions, ok := s.(session.Store)
	if !ok {
		t.Skip("the store keeps no sessions")
	}

	policy, ok := s.(session.PolicyStore)
	if !ok {
		t.Skip("the store keeps no session policy")
	}

	mappings, ok := s.(principal.GroupMappingStore)
	if !ok {
		t.Skip("the store keeps no group mappings")
	}

	return keepAccessPorts{
		mappings:   mappings,
		stager:     ps,
		keeper:     keeper,
		tokens:     tokens,
		grants:     grants,
		principals: principals,
		sessions:   sessions,
		sessionPol: policy,
		providers:  keepProviderStores(t, s),
	}
}

// backupAccess holds the access rows of the backup, so the keep and replace
// checks know what changed and what the backup holds.
type backupAccess struct {
	role        access.Role
	group       principal.Group
	memberCount int64
	tokenPolicy accesstoken.Policy
	sessionPol  session.Policy
}

// readBackupAccess reads the access rows the backup holds: the role, the
// group with its member count, and both policies.
func readBackupAccess(t *testing.T, ports keepAccessPorts) backupAccess {
	t.Helper()

	ctx := t.Context()

	role, err := ports.grants.GetRole(ctx, "rol-deployer")
	if err != nil {
		t.Fatalf("get backup role: %v", err)
	}

	group, err := ports.principals.GetGroup(ctx, "grp-ops")
	if err != nil {
		t.Fatalf("get backup group: %v", err)
	}

	tokenPolicy, err := ports.tokens.GetPolicy(ctx)
	if err != nil {
		t.Fatalf("get backup token policy: %v", err)
	}

	sessionPol, err := ports.sessionPol.GetSessionPolicy(ctx)
	if err != nil {
		t.Fatalf("get backup session policy: %v", err)
	}

	return backupAccess{
		role:        role,
		group:       group,
		memberCount: group.MemberCount,
		tokenPolicy: tokenPolicy,
		sessionPol:  sessionPol,
	}
}

// createKeepSession writes one person, one session of it and one sign-in
// request, so the backup holds them and the later changes have something to
// keep. The person is new, so the inactivity sweep of the seed leaves it
// active.
func createKeepSession(t *testing.T, ports keepAccessPorts) {
	t.Helper()

	ctx := t.Context()

	person := principal.Principal{
		CreatedAt:   now(),
		UpdatedAt:   now(),
		DisabledAt:  time.Time{},
		ID:          "prn-ses",
		Subject:     "ses-keep@example.com",
		DisplayName: "Session keeper",
		Kind:        principal.KindPerson,
		Source:      principal.SourceLocal,
		Namespace:   "",
		GroupCount:  0,
		BreakGlass:  false,
	}

	if err := ports.principals.CreatePrincipal(ctx, person, ServerChange(now(), "principal:prn-ses")); err != nil {
		t.Fatalf("create session principal: %v", err)
	}

	ses := session.Session{
		CreatedAt:   now(),
		LastSeenAt:  now(),
		AuthTime:    now().Add(-time.Minute),
		EndedAt:     time.Time{},
		ID:          "ses-keep",
		PrincipalID: "prn-ses",
		Provider:    "corp-idp",
		EndReason:   "",
		SourceIP:    "192.0.2.10",
		UserAgent:   "storetest",
		Generation:  1,
	}

	entry := ServerChange(now(), "session:ses-keep")

	if err := ports.sessions.CreateSession(ctx, ses, keepSessionDigest(1), entry); err != nil {
		t.Fatalf("create session: %v", err)
	}

	req := keepSignInRequest(now())

	if err := ports.sessions.CreateRequest(ctx, req, keepSessionDigest(2)); err != nil {
		t.Fatalf("create sign-in request: %v", err)
	}
}

// keepSessionDigest returns a distinct session digest for n: the session,
// the backup request, or the live request.
func keepSessionDigest(n int) session.Digest {
	switch n {
	case 2:
		return sha256.Sum256([]byte("keep backup request"))
	case 3:
		return sha256.Sum256([]byte("keep live request"))
	default:
		return sha256.Sum256([]byte("keep session"))
	}
}

// keepSignInRequest returns a sign-in request made at created.
func keepSignInRequest(created time.Time) session.Request {
	return session.Request{
		MaxAge: 0, HasMaxAge: false,
		CreatedAt:  created,
		ExpiresAt:  created.Add(session.RequestLifetime),
		Provider:   "corp-idp",
		Nonce:      "n-0S6_WzA2Mj-rG7k3QpL",
		Verifier:   "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk",
		ReturnPath: "/tokens?new=1",
	}
}

// mutateAccessForKeepTest changes every access table after the backup, so
// the keep restore must keep each change and the replacing restore must
// undo each one. The principal change deletes prn-deploy and creates
// prn-deploy2 with the same subject: the keep must not hit the subject
// unique index.
func mutateAccessForKeepTest(t *testing.T, s RestoreStore, ports keepAccessPorts) {
	t.Helper()

	revokeAccessForKeepTest(t, s, ports.tokens, ports.grants)
	updateKeepRole(t, ports.grants)
	updateKeepGroup(t, ports.principals)
	removeKeepMember(t, ports.principals)
	recreateKeepPrincipal(t, ports.principals)
	endKeepSession(t, ports.sessions)
	createLiveSignInRequest(t, ports.sessions)
	updateKeepTokenPolicy(t, ports.tokens)
	updateKeepSessionPolicy(t, ports.sessionPol)
	changeKeepMappings(t, ports.mappings)
	mutateKeepProviders(t, ports.providers)
}

// keepMappingKey names the mapping of the value to grp-ops for the sign-in
// provider oidc-keep.
func keepMappingKey(value string) principal.GroupMappingKey {
	return principal.GroupMappingKey{
		Source: principal.MembershipSourceClaim, SourceID: "oidc-keep", ClaimValue: value, GroupID: "grp-ops",
	}
}

// putKeepMapping stores the mapping of the value to grp-ops.
func putKeepMapping(t *testing.T, mappings principal.GroupMappingStore, value string) {
	t.Helper()

	m := principal.GroupMapping{
		CreatedAt: now(), Source: principal.MembershipSourceClaim, SourceID: "oidc-keep", ClaimValue: value,
		GroupID: "grp-ops", CreatedBy: "admin",
	}

	if err := mappings.PutGroupMapping(t.Context(), m, ServerChange(now(), "group-mapping:"+value)); err != nil {
		t.Fatalf("put group mapping %q: %v", value, err)
	}
}

// changeKeepMappings deletes the mapping of the backup and stores another,
// so the keep restore must keep the new one and the replacing restore must
// bring the old one back.
func changeKeepMappings(t *testing.T, mappings principal.GroupMappingStore) {
	t.Helper()

	removal := ServerChange(now().Add(3*time.Hour), "group-mapping:backup-team")
	if err := mappings.DeleteGroupMapping(t.Context(), keepMappingKey("backup-team"), removal); err != nil {
		t.Fatalf("delete group mapping: %v", err)
	}

	putKeepMapping(t, mappings, "changed-team")
}

// checkKeptMappings proves the restore without server:restore-access left the
// group mappings as they were: the new mapping stays and the deleted one of
// the backup does not come back.
func checkKeptMappings(t *testing.T, mappings principal.GroupMappingStore) {
	t.Helper()

	requireMappingValues(t, mappings, "after a keep restore", "changed-team")
}

// checkReplacedMappings proves the restore with server:restore-access brought
// the mappings of the backup back.
func checkReplacedMappings(t *testing.T, mappings principal.GroupMappingStore) {
	t.Helper()

	requireMappingValues(t, mappings, "after a restore that replaces the access tables", "backup-team")
}

// requireMappingValues fails unless the mappings of oidc-keep hold exactly
// the one value.
func requireMappingValues(t *testing.T, mappings principal.GroupMappingStore, when, value string) {
	t.Helper()

	got, err := mappings.ListGroupMappings(t.Context(), principal.MembershipSourceClaim, "oidc-keep")
	if err != nil {
		t.Fatalf("list group mappings %s: %v", when, err)
	}

	if len(got) != 1 || got[0].ClaimValue != value {
		t.Fatalf("group mappings %s = %+v, want only %q", when, got, value)
	}
}

// revokeAccessForKeepTest revokes one token and one grant, so the keep
// restore must keep the revocations and the replacing restore must undo them.
func revokeAccessForKeepTest(t *testing.T, s RestoreStore, tokens accesstoken.Store, grants access.Store) {
	t.Helper()

	ctx := t.Context()

	if err := tokens.RevokeToken(ctx, "tok-used", ServerChange(now(), "token:tok-used")); err != nil {
		t.Fatalf("revoke token: %v", err)
	}

	fence, entry := GrantWrite(t, s, identity.NamespaceObject("acme"), now(), "grant:grt-ops")

	if err := grants.RevokeGrant(ctx, "grt-ops", fence, entry, nil); err != nil {
		t.Fatalf("revoke grant: %v", err)
	}
}

// updateKeepRole changes the description of rol-deployer.
func updateKeepRole(t *testing.T, grants access.Store) {
	t.Helper()

	ctx := t.Context()

	role, err := grants.GetRole(ctx, "rol-deployer")
	if err != nil {
		t.Fatalf("get role: %v", err)
	}

	role.Description = "changed after the backup"
	role.UpdatedAt = now().Add(3 * time.Hour)

	if err := grants.UpdateRole(ctx, role, ServerChange(role.UpdatedAt, "role:rol-deployer"), nil); err != nil {
		t.Fatalf("update role: %v", err)
	}
}

// updateKeepGroup changes the description of grp-ops.
func updateKeepGroup(t *testing.T, principals principal.Store) {
	t.Helper()

	ctx := t.Context()

	group, err := principals.GetGroup(ctx, "grp-ops")
	if err != nil {
		t.Fatalf("get group: %v", err)
	}

	group.Description = "changed after the backup"
	group.UpdatedAt = now().Add(3 * time.Hour)

	if err := principals.UpdateGroup(ctx, group, ServerChange(group.UpdatedAt, "group:grp-ops")); err != nil {
		t.Fatalf("update group: %v", err)
	}
}

// removeKeepMember ends the membership of prn-deploy in grp-ops.
func removeKeepMember(t *testing.T, principals principal.Store) {
	t.Helper()

	removal := ServerChange(now().Add(3*time.Hour), "group:grp-ops#member:prn-deploy")

	if err := principals.RemoveMember(t.Context(), "grp-ops", "prn-deploy", removal); err != nil {
		t.Fatalf("remove member: %v", err)
	}
}

// recreateKeepPrincipal deletes prn-deploy and creates prn-deploy2 with the
// same subject. The keep merges principals by ID with update and delete
// before insert, so the new row does not hit the subject unique index.
func recreateKeepPrincipal(t *testing.T, principals principal.Store) {
	t.Helper()

	ctx := t.Context()

	deletion := ServerChange(now().Add(3*time.Hour), "principal:prn-deploy")

	if err := principals.DeletePrincipal(ctx, "prn-deploy", deletion); err != nil {
		t.Fatalf("delete principal: %v", err)
	}

	recreated := principal.Principal{
		CreatedAt:   now().Add(3 * time.Hour),
		UpdatedAt:   now().Add(3 * time.Hour),
		DisabledAt:  time.Time{},
		ID:          "prn-deploy2",
		Subject:     "ci-deploy",
		DisplayName: "CI redeployed",
		Kind:        principal.KindService,
		Source:      principal.SourceLocal,
		Namespace:   "",
		GroupCount:  0,
		BreakGlass:  false,
	}

	creation := ServerChange(recreated.CreatedAt, "principal:prn-deploy2")

	if err := principals.CreatePrincipal(ctx, recreated, creation); err != nil {
		t.Fatalf("recreate principal with the same subject: %v", err)
	}
}

// endKeepSession ends ses-keep.
func endKeepSession(t *testing.T, sessions session.Store) {
	t.Helper()

	entry := ServerChange(now().Add(3*time.Hour), "session:ses-keep")

	if err := sessions.EndSession(t.Context(), "ses-keep", session.ReasonSignOut, entry); err != nil {
		t.Fatalf("end session: %v", err)
	}
}

// createLiveSignInRequest writes a sign-in request that only the live data
// holds.
func createLiveSignInRequest(t *testing.T, sessions session.Store) {
	t.Helper()

	req := session.Request{
		MaxAge: 0, HasMaxAge: false,
		CreatedAt:  now().Add(3 * time.Hour),
		ExpiresAt:  now().Add(3*time.Hour + session.RequestLifetime),
		Provider:   "corp-idp",
		Nonce:      "n-liveOnlyRequest0001",
		Verifier:   "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXl",
		ReturnPath: "/tokens?new=1",
	}

	if err := sessions.CreateRequest(t.Context(), req, keepSessionDigest(3)); err != nil {
		t.Fatalf("create live sign-in request: %v", err)
	}
}

// updateKeepTokenPolicy changes the token policy after the backup.
func updateKeepTokenPolicy(t *testing.T, tokens accesstoken.Store) {
	t.Helper()

	_, err := tokens.UpdatePolicy(t.Context(), func(old accesstoken.Policy) (accesstoken.Policy, audit.Entry, error) {
		changed := old
		changed.MaxLifetime = time.Hour
		changed.DisableTokens = false
		changed.UpdatedAt = now().Add(3 * time.Hour)

		return changed, ServerChange(changed.UpdatedAt, accesstoken.PolicyRef), nil
	})
	if err != nil {
		t.Fatalf("update token policy: %v", err)
	}
}

// updateKeepSessionPolicy changes the session policy after the backup.
func updateKeepSessionPolicy(t *testing.T, policy session.PolicyStore) {
	t.Helper()

	_, err := policy.UpdateSessionPolicy(t.Context(), func(old session.Policy) (session.Policy, audit.Entry, error) {
		changed := old
		changed.Limits.Idle = 30 * time.Minute
		changed.Limits.MaxLength = 24 * time.Hour
		changed.UpdatedAt = now().Add(3 * time.Hour)

		return changed, ServerChange(changed.UpdatedAt, session.PolicyRef), nil
	})
	if err != nil {
		t.Fatalf("update session policy: %v", err)
	}
}

// stageKeepBackup stages the exported backup, failing on error.
func stageKeepBackup(
	t *testing.T, ps PortableRestoreStore, sink *portableSink, sum backup.Summary, files []backup.PortableFile,
) {
	t.Helper()

	if _, err := ps.StagePortable(t.Context(), sink.read, sum, files, StageAs(t, ps)); err != nil {
		t.Fatalf("stage backup: %v", err)
	}
}

// checkKeptTokenGrant proves the keep restore kept the token revoked and the
// grant revoked.
func checkKeptTokenGrant(t *testing.T, tokens accesstoken.Store, grants access.Store) {
	t.Helper()

	ctx := t.Context()

	kept, err := tokens.GetToken(ctx, "tok-used")
	if err != nil {
		t.Fatalf("get kept token: %v", err)
	}

	if kept.RevokedAt.IsZero() {
		t.Fatal("token tok-used is active after a restore that keeps the access tables, want revoked")
	}

	_, grantErr := grants.GetGrant(ctx, "grt-ops")
	if !errors.Is(grantErr, access.ErrNotFound) {
		t.Fatalf("get kept grant: err = %v, want ErrNotFound", grantErr)
	}
}

// checkKeptStacks proves the keep restore removed the extra stack and
// restored the seeded stack.
func checkKeptStacks(t *testing.T, s RestoreStore, seed *backupSeed, extra stack.Stack) {
	t.Helper()

	_, stackErr := s.GetStack(t.Context(), extra.Ref)
	if !errors.Is(stackErr, stack.ErrNotFound) {
		t.Fatalf("get extra stack after a keep restore: err = %v, want ErrNotFound", stackErr)
	}

	if got := mustGetStack(t, s, seed.prod.Ref); got.ID != seed.prod.ID {
		t.Fatalf("restored stack = %s, want %s", got.ID, seed.prod.ID)
	}
}

// checkKeptAccess proves the keep restore kept every access change: the
// token, the grant, the role, the group, the ended membership, the
// recreated principal, the ended session, the live sign-in request, and
// both policies.
func checkKeptAccess(t *testing.T, ports keepAccessPorts, want backupAccess) {
	t.Helper()

	checkKeptTokenGrant(t, ports.tokens, ports.grants)
	checkKeptRole(t, ports.grants, want)
	checkKeptGroup(t, ports.principals, want)
	checkKeptMemberRemoved(t, ports.principals)
	checkKeptMappings(t, ports.mappings)
	checkKeptRecreatedPrincipal(t, ports.principals)
	checkKeptSessionEnded(t, ports.sessions)
	checkKeptLiveRequest(t, ports.sessions)
	checkKeptTokenPolicy(t, ports.tokens, want)
	checkKeptSessionPolicy(t, ports.sessionPol, want)
	checkKeptProviders(t, ports.providers)
}

// checkKeptRole proves the role change survived the keep restore. Without
// roles in the access tables the backup role would be back, so dropping
// roles from the kept tables fails here.
func checkKeptRole(t *testing.T, grants access.Store, want backupAccess) {
	t.Helper()

	got, err := grants.GetRole(t.Context(), "rol-deployer")
	if err != nil {
		t.Fatalf("get kept role: %v", err)
	}

	if got.Description == want.role.Description {
		t.Fatalf("role description = %q, want the change after the backup", got.Description)
	}
}

// checkKeptGroup proves the group change survived the keep restore.
func checkKeptGroup(t *testing.T, principals principal.Store, want backupAccess) {
	t.Helper()

	got, err := principals.GetGroup(t.Context(), "grp-ops")
	if err != nil {
		t.Fatalf("get kept group: %v", err)
	}

	if got.Description == want.group.Description {
		t.Fatalf("group description = %q, want the change after the backup", got.Description)
	}
}

// checkKeptMemberRemoved proves prn-deploy is still out of grp-ops after
// the keep restore.
func checkKeptMemberRemoved(t *testing.T, principals principal.Store) {
	t.Helper()

	group, err := principals.GetGroup(t.Context(), "grp-ops")
	if err != nil {
		t.Fatalf("get kept group: %v", err)
	}

	if group.MemberCount != 0 {
		t.Fatalf("group members = %d, want none after the keep restore", group.MemberCount)
	}
}

// checkKeptRecreatedPrincipal proves the keep restore holds prn-deploy2
// with the subject ci-deploy, and prn-deploy stays deleted.
func checkKeptRecreatedPrincipal(t *testing.T, principals principal.Store) {
	t.Helper()

	ctx := t.Context()

	got, err := principals.GetPrincipalBySubject(ctx, "ci-deploy")
	if err != nil {
		t.Fatalf("get kept principal by subject: %v", err)
	}

	if got.ID != "prn-deploy2" {
		t.Fatalf("kept principal with subject ci-deploy = %s, want prn-deploy2", got.ID)
	}

	_, err = principals.GetPrincipal(ctx, "prn-deploy")
	if !errors.Is(err, principal.ErrNotFound) {
		t.Fatalf("get deleted principal after a keep restore: err = %v, want ErrNotFound", err)
	}
}

// checkKeptSessionEnded proves ses-keep is still ended after the keep
// restore. An ended session that comes back alive would fail here.
func checkKeptSessionEnded(t *testing.T, sessions session.Store) {
	t.Helper()

	got, err := sessions.GetSession(t.Context(), "ses-keep")
	if err != nil {
		t.Fatalf("get kept session: %v", err)
	}

	if got.EndedAt.IsZero() {
		t.Fatal("session ses-keep is active after a restore that keeps the access tables, want ended")
	}
}

// checkKeptLiveRequest proves the live-only sign-in request survived the
// keep restore by taking it.
func checkKeptLiveRequest(t *testing.T, sessions session.Store) {
	t.Helper()

	takeAt := now().Add(3*time.Hour + 5*time.Minute)

	if _, err := sessions.TakeRequest(t.Context(), keepSessionDigest(3), takeAt); err != nil {
		t.Fatalf("take live sign-in request after a keep restore: %v", err)
	}
}

// checkKeptTokenPolicy proves the token policy change survived the keep
// restore.
func checkKeptTokenPolicy(t *testing.T, tokens accesstoken.Store, want backupAccess) {
	t.Helper()

	got, err := tokens.GetPolicy(t.Context())
	if err != nil {
		t.Fatalf("get kept token policy: %v", err)
	}

	if got.DisableTokens == want.tokenPolicy.DisableTokens || got.MaxLifetime == want.tokenPolicy.MaxLifetime {
		t.Fatalf("kept token policy = %+v, want the change after the backup", got)
	}
}

// checkKeptSessionPolicy proves the session policy change survived the keep
// restore.
func checkKeptSessionPolicy(t *testing.T, policy session.PolicyStore, want backupAccess) {
	t.Helper()

	got, err := policy.GetSessionPolicy(t.Context())
	if err != nil {
		t.Fatalf("get kept session policy: %v", err)
	}

	if got.Limits == want.sessionPol.Limits {
		t.Fatalf("kept session policy = %+v, want the change after the backup", got.Limits)
	}
}

// checkReplacedAccess proves the replacing restore brought every access
// table back from the backup.
func checkReplacedAccess(t *testing.T, ports keepAccessPorts, want backupAccess) {
	t.Helper()

	checkReplacedTokenGrant(t, ports.tokens, ports.grants)
	checkReplacedRole(t, ports.grants, want)
	checkReplacedGroup(t, ports.principals, want)
	checkReplacedMember(t, ports.principals, want)
	checkReplacedMappings(t, ports.mappings)
	checkReplacedPrincipal(t, ports.principals)
	checkReplacedSession(t, ports.sessions)
	checkReplacedRequest(t, ports.sessions)
	checkReplacedTokenPolicy(t, ports.tokens, want)
	checkReplacedSessionPolicy(t, ports.sessionPol, want)
	checkReplacedProviders(t, ports.providers)
}

// checkReplacedTokenGrant proves the replacing restore brought the token
// and the grant back from the backup.
func checkReplacedTokenGrant(t *testing.T, tokens accesstoken.Store, grants access.Store) {
	t.Helper()

	ctx := t.Context()

	replaced, err := tokens.GetToken(ctx, "tok-used")
	if err != nil {
		t.Fatalf("get replaced token: %v", err)
	}

	if !replaced.RevokedAt.IsZero() {
		t.Fatal("token tok-used is revoked after a restore that replaces the access tables, want active")
	}

	if _, err := grants.GetGrant(ctx, "grt-ops"); err != nil {
		t.Fatalf("get replaced grant: %v", err)
	}
}

// checkReplacedRole proves the replacing restore brought the backup role
// back.
func checkReplacedRole(t *testing.T, grants access.Store, want backupAccess) {
	t.Helper()

	got, err := grants.GetRole(t.Context(), "rol-deployer")
	if err != nil {
		t.Fatalf("get replaced role: %v", err)
	}

	if got.Description != want.role.Description {
		t.Fatalf("replaced role description = %q, want %q", got.Description, want.role.Description)
	}
}

// checkReplacedGroup proves the replacing restore brought the backup group
// back.
func checkReplacedGroup(t *testing.T, principals principal.Store, want backupAccess) {
	t.Helper()

	got, err := principals.GetGroup(t.Context(), "grp-ops")
	if err != nil {
		t.Fatalf("get replaced group: %v", err)
	}

	if got.Description != want.group.Description {
		t.Fatalf("replaced group description = %q, want %q", got.Description, want.group.Description)
	}
}

// checkReplacedMember proves prn-deploy is back in grp-ops after the
// replacing restore.
func checkReplacedMember(t *testing.T, principals principal.Store, want backupAccess) {
	t.Helper()

	group, err := principals.GetGroup(t.Context(), "grp-ops")
	if err != nil {
		t.Fatalf("get replaced group: %v", err)
	}

	if group.MemberCount != want.memberCount {
		t.Fatalf("replaced group members = %d, want %d", group.MemberCount, want.memberCount)
	}
}

// checkReplacedPrincipal proves the replacing restore brought prn-deploy
// back and dropped prn-deploy2.
func checkReplacedPrincipal(t *testing.T, principals principal.Store) {
	t.Helper()

	ctx := t.Context()

	got, err := principals.GetPrincipalBySubject(ctx, "ci-deploy")
	if err != nil {
		t.Fatalf("get replaced principal by subject: %v", err)
	}

	if got.ID != "prn-deploy" {
		t.Fatalf("replaced principal with subject ci-deploy = %s, want prn-deploy", got.ID)
	}

	_, err = principals.GetPrincipal(ctx, "prn-deploy2")
	if !errors.Is(err, principal.ErrNotFound) {
		t.Fatalf("get prn-deploy2 after a replacing restore: err = %v, want ErrNotFound", err)
	}
}

// checkReplacedSession proves ses-keep is active again after the replacing
// restore.
func checkReplacedSession(t *testing.T, sessions session.Store) {
	t.Helper()

	got, err := sessions.GetSession(t.Context(), "ses-keep")
	if err != nil {
		t.Fatalf("get replaced session: %v", err)
	}

	if !got.EndedAt.IsZero() {
		t.Fatal("session ses-keep is ended after a restore that replaces the access tables, want active")
	}
}

// checkReplacedRequest proves the backup sign-in request is back after the
// replacing restore by taking it.
func checkReplacedRequest(t *testing.T, sessions session.Store) {
	t.Helper()

	if _, err := sessions.TakeRequest(t.Context(), keepSessionDigest(2), now().Add(time.Minute)); err != nil {
		t.Fatalf("take backup sign-in request after a replacing restore: %v", err)
	}
}

// checkReplacedTokenPolicy proves the replacing restore brought the backup
// token policy back.
func checkReplacedTokenPolicy(t *testing.T, tokens accesstoken.Store, want backupAccess) {
	t.Helper()

	got, err := tokens.GetPolicy(t.Context())
	if err != nil {
		t.Fatalf("get replaced token policy: %v", err)
	}

	if got != want.tokenPolicy {
		t.Fatalf("replaced token policy = %+v, want %+v", got, want.tokenPolicy)
	}
}

// checkReplacedSessionPolicy proves the replacing restore brought the
// backup session policy back.
func checkReplacedSessionPolicy(t *testing.T, policy session.PolicyStore, want backupAccess) {
	t.Helper()

	got, err := policy.GetSessionPolicy(t.Context())
	if err != nil {
		t.Fatalf("get replaced session policy: %v", err)
	}

	if got != want.sessionPol {
		t.Fatalf("replaced session policy = %+v, want %+v", got, want.sessionPol)
	}
}

// testRestoreKeepAccessMismatch proves that a restore that keeps the access
// tables refuses a backup that misses a namespace or a unit a kept grant
// still names. Each case runs on a fresh store: seed, export, add live-only
// access data, stage the backup, and expect ErrRestoreAccessMismatch with
// the live data unchanged.
func testRestoreKeepAccessMismatch(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	t.Run("grant on a missing namespace", func(t *testing.T) {
		t.Parallel()
		checkKeepMismatchNamespace(t, newStore(t))
	})

	t.Run("grant on a missing unit", func(t *testing.T) {
		t.Parallel()
		checkKeepMismatchUnit(t, newStore(t))
	})

	t.Run("principal on a missing namespace", func(t *testing.T) {
		t.Parallel()
		checkKeepMismatchPrincipal(t, newStore(t))
	})
}

// checkKeepMismatchNamespace adds a live-only namespace with a grant on it,
// then proves the keep restore refuses with ErrRestoreAccessMismatch and
// changes nothing.
func checkKeepMismatchNamespace(t *testing.T, s RestoreStore) {
	t.Helper()

	ports := keepAccessStores(t, s)
	ctx := t.Context()

	seedBackupStore(t, s)
	sink, sum, files := exportPortable(t, ports.stager)

	fresh := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           now(),
		Name:                "freshns",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(ctx, fresh); err != nil {
		t.Fatalf("create live namespace: %v", err)
	}

	grant := access.Grant{
		CreatedAt: now(),
		Condition: nil,
		Object:    identity.NamespaceObject("freshns"),
		ID:        "grt-freshns",
		Role:      access.RoleViewer,
		CreatedBy: "admin",
		Subject:   access.Subject{Kind: access.SubjectGroup, ID: "grp-ops"},
	}

	fence, entry := GrantWrite(t, s, grant.Object, now(), "grant:grt-freshns")

	if err := ports.grants.CreateGrant(ctx, grant, fence, entry, nil); err != nil {
		t.Fatalf("create live grant: %v", err)
	}

	before := portableSummaryOf(t, ports.stager)

	stageKeepBackup(t, ports.stager, sink, sum, files)

	_, err := ports.keeper.ApplyStagingKeepingAccess(ctx)
	if !errors.Is(err, backup.ErrRestoreAccessMismatch) {
		t.Fatalf("apply with a grant on a missing namespace: err = %v, want ErrRestoreAccessMismatch", err)
	}

	assertMismatchChangedNothing(t, s, ports, mismatchWant{
		before:      before,
		ns:          "freshns",
		grantID:     "grt-freshns",
		principalID: "",
	})
}

// checkKeepMismatchUnit adds a live-only stack with a grant on its unit,
// then proves the keep restore refuses with ErrRestoreAccessMismatch and
// changes nothing.
func checkKeepMismatchUnit(t *testing.T, s RestoreStore) {
	t.Helper()

	ports := keepAccessStores(t, s)
	ctx := t.Context()

	seed := seedBackupStore(t, s)
	sink, sum, files := exportPortable(t, ports.stager)

	unitStack := mustCreateStack(t, s, seed.acme, "freshunit")

	stored := mustGetStack(t, s, unitStack.Ref)

	obj, err := identity.UnitObject("pulumi", seed.acme.Name, "web/freshunit")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	obj = obj.WithUnitID(stored.ID.String())

	grant := access.Grant{
		CreatedAt: now(),
		Condition: nil,
		Object:    obj,
		ID:        "grt-freshunit",
		Role:      access.RoleViewer,
		CreatedBy: "admin",
		Subject:   access.Subject{Kind: access.SubjectGroup, ID: "grp-ops"},
	}

	fence, entry := GrantWrite(t, s, grant.Object, now(), "grant:grt-freshunit")

	if err := ports.grants.CreateGrant(ctx, grant, fence, entry, nil); err != nil {
		t.Fatalf("create live unit grant: %v", err)
	}

	before := portableSummaryOf(t, ports.stager)

	stageKeepBackup(t, ports.stager, sink, sum, files)

	_, applyErr := ports.keeper.ApplyStagingKeepingAccess(ctx)
	if !errors.Is(applyErr, backup.ErrRestoreAccessMismatch) {
		t.Fatalf("apply with a grant on a missing unit: err = %v, want ErrRestoreAccessMismatch", applyErr)
	}

	if _, err := s.GetStack(ctx, unitStack.Ref); err != nil {
		t.Fatalf("get live stack after a refused restore: %v", err)
	}

	assertMismatchChangedNothing(t, s, ports, mismatchWant{
		before:      before,
		ns:          "",
		grantID:     "grt-freshunit",
		principalID: "",
	})
}

// mismatchWant holds what a refused keep restore must leave behind: the
// live summary from before the refused apply, the live-only namespace when
// the case made one, and the live-only grant or principal.
type mismatchWant struct {
	before      backup.Summary
	ns          string
	grantID     string
	principalID string
}

// assertMismatchChangedNothing proves a refused keep restore changed
// nothing: every table still holds its live rows, the namespace still exists
// when the case made one, and the live-only grant or principal is still
// there.
func assertMismatchChangedNothing(
	t *testing.T,
	s RestoreStore,
	ports keepAccessPorts,
	want mismatchWant,
) {
	t.Helper()

	ctx := t.Context()

	after := portableSummaryOf(t, ports.stager)

	for table, count := range want.before.RowCounts {
		if after.RowCounts[table] != count {
			t.Fatalf("table %q after a refused restore = %d, want %d", table, after.RowCounts[table], count)
		}
	}

	if want.ns != "" {
		if _, err := s.GetNamespace(ctx, namespace.Name(want.ns)); err != nil {
			t.Fatalf("get live namespace after a refused restore: %v", err)
		}
	}

	if want.grantID != "" {
		if _, err := ports.grants.GetGrant(ctx, want.grantID); err != nil {
			t.Fatalf("get live grant after a refused restore: %v", err)
		}
	}

	if want.principalID != "" {
		if _, err := ports.principals.GetPrincipal(ctx, want.principalID); err != nil {
			t.Fatalf("get live principal after a refused restore: %v", err)
		}
	}

	if err := s.DiscardStaging(ctx); err != nil {
		t.Fatalf("discard staging area after a refused restore: %v", err)
	}
}

// checkKeepMismatchPrincipal adds a live-only namespace with a principal in
// it, then proves the keep restore refuses with ErrRestoreAccessMismatch and
// changes nothing.
func checkKeepMismatchPrincipal(t *testing.T, s RestoreStore) {
	t.Helper()

	ports := keepAccessStores(t, s)
	ctx := t.Context()

	seedBackupStore(t, s)
	sink, sum, files := exportPortable(t, ports.stager)

	fresh := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           now(),
		Name:                "freshprn",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}

	if err := s.CreateNamespace(ctx, fresh); err != nil {
		t.Fatalf("create live namespace: %v", err)
	}

	live, err := s.GetNamespace(ctx, fresh.Name)
	if err != nil {
		t.Fatalf("get live namespace: %v", err)
	}

	person := principal.Principal{
		CreatedAt:   now(),
		UpdatedAt:   now(),
		DisabledAt:  time.Time{},
		ID:          "prn-fresh",
		Subject:     "fresh-ci",
		DisplayName: "Fresh CI",
		Kind:        principal.KindService,
		Source:      principal.SourceLocal,
		Namespace:   fresh.Name,
		GroupCount:  0,
		BreakGlass:  false,
	}

	entry := ServerChange(now(), "principal:prn-fresh")
	entry.Namespace = fresh.Name

	if err := ports.principals.CreateNamespacePrincipal(ctx, person, live.Fence(), entry); err != nil {
		t.Fatalf("create live namespace principal: %v", err)
	}

	before := portableSummaryOf(t, ports.stager)

	stageKeepBackup(t, ports.stager, sink, sum, files)

	_, applyErr := ports.keeper.ApplyStagingKeepingAccess(ctx)
	if !errors.Is(applyErr, backup.ErrRestoreAccessMismatch) {
		t.Fatalf("apply with a principal on a missing namespace: err = %v, want ErrRestoreAccessMismatch", applyErr)
	}

	assertMismatchChangedNothing(t, s, ports, mismatchWant{
		before:      before,
		ns:          "freshprn",
		grantID:     "",
		principalID: "prn-fresh",
	})
}

// testRestoreKeepsLiveOnlyPrincipal proves that a principal created after the
// backup keeps its child rows through a keep-access restore. The live
// principal has an access token, a session and a group membership that the
// backup does not hold. The keep must keep all of them, and the merge must not
// fail on a foreign key. A merge that runs in the wrong order fails here.
func testRestoreKeepsLiveOnlyPrincipal(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)
	ports := keepAccessStores(t, s)

	seedBackupStore(t, s)
	sink, sum, files := exportPortable(t, ports.stager)

	createLiveOnlyPrincipal(t, ports)
	stageKeepBackup(t, ports.stager, sink, sum, files)

	if _, err := ports.keeper.ApplyStagingKeepingAccess(t.Context()); err != nil {
		t.Fatalf("apply keeping the access tables with a live-only principal: %v", err)
	}

	checkKeptLiveOnlyPrincipal(t, ports)
}

// createLiveOnlyPrincipal creates prn-live-only after the backup, with one
// access token, one session and one membership of grp-ops.
func createLiveOnlyPrincipal(t *testing.T, ports keepAccessPorts) {
	t.Helper()

	ctx := t.Context()
	at := now().Add(3 * time.Hour)

	live := principal.Principal{
		CreatedAt:   at,
		UpdatedAt:   at,
		DisabledAt:  time.Time{},
		ID:          "prn-live-only",
		Subject:     "live-only@example.com",
		DisplayName: "Live only",
		Kind:        principal.KindPerson,
		Source:      principal.SourceLocal,
		Namespace:   "",
		GroupCount:  0,
		BreakGlass:  false,
	}

	if err := ports.principals.CreatePrincipal(ctx, live, ServerChange(at, "principal:prn-live-only")); err != nil {
		t.Fatalf("create live-only principal: %v", err)
	}

	token := backupToken("tok-live-only", "prn-live-only", accesstoken.KindPersonal)
	token.CreatedAt = at

	entry := ServerChange(at, "token:tok-live-only")

	if err := ports.tokens.CreateToken(ctx, token, accesstoken.Digest{4}, entry); err != nil {
		t.Fatalf("create live-only token: %v", err)
	}

	ses := session.Session{
		CreatedAt:   at,
		LastSeenAt:  at,
		AuthTime:    at.Add(-time.Minute),
		EndedAt:     time.Time{},
		ID:          "ses-live-only",
		PrincipalID: "prn-live-only",
		Provider:    "corp-idp",
		EndReason:   "",
		SourceIP:    "192.0.2.11",
		UserAgent:   "storetest",
		Generation:  1,
	}

	digest := session.Digest(sha256.Sum256([]byte("live-only session")))

	if err := ports.sessions.CreateSession(ctx, ses, digest, ServerChange(at, "session:ses-live-only")); err != nil {
		t.Fatalf("create live-only session: %v", err)
	}

	member := principal.Membership{
		AddedAt: at, SuspendedAt: time.Time{}, GroupID: "grp-ops", PrincipalID: "prn-live-only",
		Source: principal.MembershipSourceLocal, SourceID: "", State: principal.MembershipStateActive,
		Pinned: false,
	}

	memberEntry := ServerChange(at, "group:grp-ops#member:prn-live-only")

	if err := ports.principals.AddMember(ctx, member, memberEntry, nil); err != nil {
		t.Fatalf("add live-only member: %v", err)
	}
}

// checkKeptLiveOnlyPrincipal proves the live-only principal and each of its
// child rows survived the keep restore.
func checkKeptLiveOnlyPrincipal(t *testing.T, ports keepAccessPorts) {
	t.Helper()

	ctx := t.Context()

	if _, err := ports.principals.GetPrincipal(ctx, "prn-live-only"); err != nil {
		t.Fatalf("get kept live-only principal: %v", err)
	}

	if _, err := ports.tokens.GetToken(ctx, "tok-live-only"); err != nil {
		t.Fatalf("get kept live-only token: %v", err)
	}

	if _, err := ports.sessions.GetSession(ctx, "ses-live-only"); err != nil {
		t.Fatalf("get kept live-only session: %v", err)
	}

	query := principal.Query{After: "", Low: "", High: "", Limit: principal.MaxLimit + 1}

	members, _, err := ports.principals.ListMembers(ctx, "grp-ops", query)
	if err != nil {
		t.Fatalf("list kept members of grp-ops: %v", err)
	}

	for i := range members {
		if members[i].PrincipalID == "prn-live-only" {
			return
		}
	}

	t.Fatalf("kept members of grp-ops = %d, none is prn-live-only", len(members))
}
