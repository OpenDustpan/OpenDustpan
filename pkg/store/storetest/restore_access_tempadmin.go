package storetest

import (
	"crypto/sha256"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// Names of the live administrator that the temporary administrator case makes.
const (
	keepTempAdminID    = "prn-keep-admin"
	keepTempAdminGrant = "grt-keep-admin"
	keepTempAdminToken = "tok-keep-admin"
)

// testRestoreKeepAccessIgnoresBackupTempAdmins proves that a restore that
// keeps the access tables cannot disable a live administrator. The backup
// holds a temp_admins row for the live administrator with an expiry in the
// past and no end time. After the swap, one sweep of the temporary
// administrators must leave the administrator enabled, with its grant and its
// token. A store that takes the temp_admins rows from the backup fails here:
// the sweep ends the staged row, disables the principal, revokes its grants
// and ends its tokens.
func testRestoreKeepAccessIgnoresBackupTempAdmins(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	s := newStore(t)
	ports := keepAccessStores(t, s)

	temp, ok := s.(tempadmin.Store)
	if !ok {
		t.Skip("the store keeps no temporary administrators")
	}

	seedBackupStore(t, s)
	createKeepTempAdmin(t, ports)

	expired := keepTempAdminExpiry()

	stageKeepTempAdminBackup(t, ports)

	if _, err := ports.keeper.ApplyStagingKeepingAccess(t.Context()); err != nil {
		t.Fatalf("apply keeping the access tables: %v", err)
	}

	svc, err := tempadmin.NewService(tempadmin.Config{
		Store: temp, Reviews: nil, Now: func() time.Time { return expired.Add(time.Hour) }, Logger: nil,
	})
	if err != nil {
		t.Fatalf("temporary administrator service: %v", err)
	}

	done, err := svc.Sweep(t.Context(), ServerChange(expired.Add(time.Hour), tempadmin.Ref(keepTempAdminID)))
	if err != nil {
		t.Fatalf("sweep after the restore: %v", err)
	}

	checkKeepAdminIntact(t, ports, done)
}

// createKeepTempAdmin creates the live administrator: a person with a grant of
// the site administrator role on the installation and a personal token.
func createKeepTempAdmin(t *testing.T, ports keepAccessPorts) {
	t.Helper()

	ctx := t.Context()

	person := principal.Principal{
		CreatedAt: now(), UpdatedAt: now(), DisabledAt: time.Time{}, ID: keepTempAdminID,
		Subject: "subject-" + keepTempAdminID, DisplayName: "Keep administrator", Kind: principal.KindPerson,
		Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: false,
	}

	if err := ports.principals.CreatePrincipal(ctx, person, ServerChange(now(), principal.Ref(person.ID))); err != nil {
		t.Fatalf("create the administrator: %v", err)
	}

	grant := access.Grant{
		CreatedAt: now(), Condition: nil, Object: identity.ServerObject(), ID: keepTempAdminGrant,
		Role: access.RoleSiteAdministrator, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: keepTempAdminID},
	}
	fence, entry := GrantWrite(t, ports.stager, grant.Object, now(), "grant:"+grant.ID)

	if err := ports.grants.CreateGrant(ctx, grant, fence, entry, nil); err != nil {
		t.Fatalf("create the administrator grant: %v", err)
	}

	token := accesstoken.Token{
		CreatedAt: now(), ExpiresAt: now().Add(24 * 365 * time.Hour), LastUsedAt: time.Time{},
		RevokedAt: time.Time{}, DisabledAt: time.Time{}, ID: keepTempAdminToken, PrincipalID: keepTempAdminID,
		Name: "administrator token", Kind: accesstoken.KindPersonal, ReplacedBy: "", CreatedBy: "admin",
	}
	digest := accesstoken.Digest(sha256.Sum256([]byte(keepTempAdminToken)))

	if err := ports.tokens.CreateToken(ctx, token, digest, ServerChange(now(), accesstoken.Ref(token.ID))); err != nil {
		t.Fatalf("create the administrator token: %v", err)
	}
}

// checkKeepAdminIntact fails when the sweep ended the live administrator.
func checkKeepAdminIntact(t *testing.T, ports keepAccessPorts, swept []string) {
	t.Helper()

	ctx := t.Context()

	if len(swept) != 0 {
		t.Errorf("the sweep ended %q after a restore that keeps the access tables, want no record", swept)
	}

	got, err := ports.principals.GetPrincipal(ctx, keepTempAdminID)
	if err != nil {
		t.Fatalf("get the administrator: %v", err)
	}

	if !got.DisabledAt.IsZero() {
		t.Errorf("administrator %q is disabled at %v after the sweep, want it enabled", keepTempAdminID, got.DisabledAt)
	}

	if _, grantErr := ports.grants.GetGrant(ctx, keepTempAdminGrant); grantErr != nil {
		t.Errorf("get grant %q after the sweep: %v, want the grant kept", keepTempAdminGrant, grantErr)
	}

	tok, err := ports.tokens.GetToken(ctx, keepTempAdminToken)
	if err != nil {
		t.Fatalf("get the administrator token: %v", err)
	}

	if !tok.RevokedAt.IsZero() {
		t.Errorf("token %q is revoked at %v after the sweep, want it active", keepTempAdminToken, tok.RevokedAt)
	}
}

// keepTempAdminExpiry is the expiry of the staged temp_admins row: it is in the
// past of the sweep clock of the cases.
func keepTempAdminExpiry() time.Time {
	return time.Date(2026, time.October, 9, 0, 0, 0, 0, time.UTC)
}

// stageKeepTempAdminBackup exports the store of ports, adds a temp_admins row
// for the live administrator with an expiry in the past and no end time, and
// stages the result.
func stageKeepTempAdminBackup(t *testing.T, ports keepAccessPorts) {
	t.Helper()

	files, sum, descs := keepTempAdminBackup(t, ports.stager)
	opts := StageAs(t, ports.stager)

	if _, err := ports.stager.StagePortable(t.Context(), PortableOpener(files), sum, descs, opts); err != nil {
		t.Fatalf("stage the backup with a temp_admins row: %v", err)
	}
}

// keepTempAdminBackup exports s and adds the temp_admins row of the case.
func keepTempAdminBackup(
	t *testing.T, s PortableRestoreStore,
) (map[string][]byte, backup.Summary, []backup.PortableFile) {
	t.Helper()

	files, sum, descs := PortableFiles(t, s)

	row := DevTempAdminRow(keepTempAdminID, "subject-"+keepTempAdminID)
	row["expires_at_ms"] = keepTempAdminExpiry().UnixMilli()
	row["created_at_ms"] = now().UnixMilli()

	files, descs, sum = AddPortableRows(t, files, descs, sum, "temp_admins", row)

	return files, sum, descs
}

// testRestoreReplaceAccessTakesBackupTempAdmins proves that a restore that
// replaces the access tables brings the temp_admins rows of the backup with
// the principals that the rows name.
func testRestoreReplaceAccessTakesBackupTempAdmins(t *testing.T, newStore RestoreFactory) {
	t.Helper()

	src := newStore(t)
	srcPorts := keepAccessStores(t, src)

	if _, ok := src.(tempadmin.Store); !ok {
		t.Skip("the store keeps no temporary administrators")
	}

	seedBackupStore(t, src)
	createKeepTempAdmin(t, srcPorts)

	files, sum, descs := keepTempAdminBackup(t, srcPorts.stager)

	dst := newStore(t)
	dstPorts := keepAccessStores(t, dst)

	dstTemp, ok := dst.(tempadmin.Store)
	if !ok {
		t.Fatal("the target store keeps no temporary administrators, but the source store does")
	}

	opts := StageAs(t, dstPorts.stager)

	if _, err := dstPorts.stager.StagePortable(t.Context(), PortableOpener(files), sum, descs, opts); err != nil {
		t.Fatalf("stage the backup: %v", err)
	}

	if err := dst.ApplyStaging(t.Context()); err != nil {
		t.Fatalf("apply replacing the access tables: %v", err)
	}

	record, err := dstTemp.GetTempAdmin(t.Context(), keepTempAdminID)
	if err != nil {
		t.Fatalf("get the temporary administrator of the backup: %v", err)
	}

	if !record.EndedAt.IsZero() || !record.ExpiresAt.Equal(keepTempAdminExpiry()) {
		t.Errorf("record = ended %v, expires %v, want the row of the backup", record.EndedAt, record.ExpiresAt)
	}

	if _, err := dstPorts.principals.GetPrincipal(t.Context(), keepTempAdminID); err != nil {
		t.Errorf("get the principal of the temp_admins row: %v, want it restored with the row", err)
	}
}
