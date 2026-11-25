package backup_test

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/store/jobcheck"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/mfa"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestBreakGlassRestoreAndRace proves the two refusals of the break-glass
// floor over a real SQLite store. The restore case restores a backup with one
// working account over two live ones: the restore ends failed with the code
// principal.no-working-break-glass, the live accounts stay unchanged, and the
// audit log holds the failure entry of the restore. The race case disables
// the two live accounts in parallel: exactly one disable is refused and one
// account stays enabled. The HTTP status of the code is checked in
// internal/api/server, because this package has no HTTP layer.
func TestBreakGlassRestoreAndRace(t *testing.T) {
	t.Parallel()

	t.Run("restore of a backup with one working account", func(t *testing.T) {
		t.Parallel()
		runBreakGlassRestoreRefusal(t)
	})

	t.Run("parallel disable of the last two working accounts", func(t *testing.T) {
		t.Parallel()
		runBreakGlassDisableRace(t)
	})
}

// runBreakGlassRestoreRefusal restores a backup with one working account over
// two live ones, and checks the refusal, the live accounts and the audit
// entry.
func runBreakGlassRestoreRefusal(t *testing.T) {
	t.Helper()

	target := openBreakGlassBlobs(t)

	source := newBreakGlassRestoreRig(t, target)
	seedWorkingBreakGlass(t, source.store, "prn-bg-1")

	backupID := backup.ID(ident.NewID("bkp-"))
	if _, err := source.backups.Run(t.Context(), backupID); err != nil {
		t.Fatalf("run backup: %v", err)
	}

	dest := newBreakGlassRestoreRig(t, target)
	liveIDs := []string{"prn-bg-1", "prn-bg-2"}

	for _, id := range liveIDs {
		seedWorkingBreakGlass(t, dest.store, id)
	}

	before := breakGlassLiveRows(t, dest.store, liveIDs)

	restoreID := backup.RestoreID(ident.NewID("rst-"))
	req := backup.RestoreRequest{
		BackupID: backupID, PreBackupID: "", CheckOnly: false, Force: false, RequestedBy: "tester",
		KeepAccessTables: false,
	}

	if err := dest.restores.Start(t.Context(), restoreID, req); err != nil {
		t.Fatalf("start restore: %v", err)
	}

	row := runBreakGlassRestore(t, dest.restores, restoreID)
	if row.State != backup.RestoreFailed {
		t.Fatalf("restore state = %q, want %q (error %q)", row.State, backup.RestoreFailed, row.Error)
	}

	if row.DevReport == nil || row.DevReport.ErrorCode != backup.CodeNoWorkingBreakGlass {
		t.Fatalf("restore code = %+v, want %q", row.DevReport, backup.CodeNoWorkingBreakGlass)
	}

	if after := breakGlassLiveRows(t, dest.store, liveIDs); !reflect.DeepEqual(after, before) {
		t.Fatalf("live accounts after the refused restore = %+v, want %+v", after, before)
	}

	checkRestoreFailureEntry(t, dest.store, restoreID)
}

// runBreakGlassDisableRace disables the last two working accounts in
// parallel, and checks that exactly one disable is refused.
func runBreakGlassDisableRace(t *testing.T) {
	t.Helper()

	st := openBreakGlassStore(t)
	ids := []string{"prn-bg-1", "prn-bg-2"}

	for _, id := range ids {
		seedWorkingBreakGlass(t, st, id)
	}

	results := make([]error, len(ids))

	var wg sync.WaitGroup

	for i, id := range ids {
		wg.Go(func() {
			results[i] = st.DisablePrincipal(t.Context(), id, storetest.ServerChange(time.Now(), "principal:"+id))
		})
	}

	wg.Wait()

	refused := 0

	for i, err := range results {
		switch {
		case err == nil:
		case errors.Is(err, principal.ErrNoWorkingBreakGlass):
			refused++
		default:
			t.Fatalf("disable %q = %v, want nil or %v", ids[i], err, principal.ErrNoWorkingBreakGlass)
		}
	}

	if refused != 1 {
		t.Fatalf("%d of 2 disables were refused, want 1", refused)
	}

	working, err := st.CountWorkingBreakGlass(t.Context(), time.Now())
	if err != nil {
		t.Fatalf("count the working break-glass accounts: %v", err)
	}

	if working != 1 {
		t.Fatalf("working break-glass accounts = %d, want 1", working)
	}
}

// breakGlassRestoreRig is one SQLite store with the backup and the restore
// services over it, as the server wires them.
type breakGlassRestoreRig struct {
	store    *sqlite.Store
	backups  *backup.Service
	restores *backup.RestoreService
}

func newBreakGlassRestoreRig(t *testing.T, target *fs.Store) breakGlassRestoreRig {
	t.Helper()

	st := openBreakGlassStore(t)

	if _, err := st.ClaimKind(t.Context(), installation.KindProduction); err != nil {
		t.Fatalf("claim the installation kind: %v", err)
	}

	live := openBreakGlassBlobs(t)

	backups, err := backup.NewService(backup.Config{
		Jobs:     jobcheck.Checker{},
		Exporter: st, Kinds: st, Registry: st, Blobs: live, Target: target, TargetName: "dir:test",
		Operations: st.DurableOperationsPort(), Version: "test", StateOpener: nil, Owner: "", Lease: 0, Now: nil,
		MaxSQLBytes: 0, Logger: nil,
	})
	if err != nil {
		t.Fatalf("new backup service: %v", err)
	}

	restores, err := backup.NewRestoreService(backup.RestoreConfig{
		Live: st, Operations: st.DurableOperationsPort(), Backups: backups,
		Maintenance: storetest.StateMaintenance{State: st, Hold: false}, Blobs: live, Leases: st, Target: target,
		Key: nil, Installation: "test", AfterApply: nil, Admit: nil, AllowDevBackups: false, AllowDevHint: "",
		Owner: "", Lease: 0, CleanupLockWait: 0, RetryBase: time.Millisecond, Now: nil, Logger: nil,
		AccessSetting:       nil,
		AccessSettingStatic: nil,
	})
	if err != nil {
		t.Fatalf("new restore service: %v", err)
	}

	return breakGlassRestoreRig{store: st, backups: backups, restores: restores}
}

func openBreakGlassStore(t *testing.T) *sqlite.Store {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	return st
}

func openBreakGlassBlobs(t *testing.T) *fs.Store {
	t.Helper()

	blobs, err := fs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	return blobs
}

// seedWorkingBreakGlass stores a working break-glass account: an enabled
// break-glass person with a passkey and a site administrator grant.
func seedWorkingBreakGlass(t *testing.T, st *sqlite.Store, id string) {
	t.Helper()

	ctx := t.Context()
	at := time.Now().UTC().Truncate(time.Millisecond)

	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{},
		ID: id, Subject: id + "@example.com", DisplayName: id, Kind: principal.KindPerson,
		Source: principal.SourceLocal, Namespace: "", GroupCount: 0, BreakGlass: true,
	}

	if err := st.CreatePrincipal(ctx, p, storetest.ServerChange(at, "principal:"+id)); err != nil {
		t.Fatalf("create break-glass principal %q: %v", id, err)
	}

	passkey := mfa.Passkey{
		CreatedAt: at, LastUsedAt: time.Time{}, SuspendedAt: time.Time{},
		ID: "pky-" + id, PersonID: id, Name: "key-" + id,
		CredentialID: []byte("credential-" + id), PublicKey: []byte("public-key-" + id),
		Transports: nil, AAGUID: [16]byte{}, SignCount: 0, BackupEligible: false,
	}

	if err := st.AddPasskey(ctx, passkey, storetest.ServerChange(at, "passkey:pky-"+id)); err != nil {
		t.Fatalf("add passkey of %q: %v", id, err)
	}

	grant := access.Grant{
		CreatedAt: at, Condition: nil, Object: identity.ServerObject(), ID: "grt-" + id,
		Role: access.RoleSiteAdministrator, CreatedBy: "admin",
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: id},
	}

	err := st.CreateGrant(ctx, grant, namespace.Fence{Namespace: "", FailoverVersion: 0},
		storetest.ServerChange(at, "grant:grt-"+id), nil)
	if err != nil {
		t.Fatalf("create grant of %q: %v", id, err)
	}
}

// breakGlassLiveRow is the live state of one break-glass account that a
// refused restore must keep.
type breakGlassLiveRow struct {
	principal principal.Principal
	passkeys  []mfa.Passkey
}

func breakGlassLiveRows(t *testing.T, st *sqlite.Store, ids []string) map[string]breakGlassLiveRow {
	t.Helper()

	out := make(map[string]breakGlassLiveRow, len(ids))

	for _, id := range ids {
		p, err := st.GetPrincipal(t.Context(), id)
		if err != nil {
			t.Fatalf("get principal %q: %v", id, err)
		}

		keys, err := st.Passkeys(t.Context(), id)
		if err != nil {
			t.Fatalf("list passkeys of %q: %v", id, err)
		}

		out[id] = breakGlassLiveRow{principal: p, passkeys: keys}
	}

	return out
}

// breakGlassRestoreRuns bounds the runs of the restore operation. Each run
// takes one step, and a restore has fewer steps.
const breakGlassRestoreRuns = 100

// runBreakGlassRestore runs the restore operation, as a worker does, until the
// restore leaves checking and applying, and returns the final row.
func runBreakGlassRestore(t *testing.T, restores *backup.RestoreService, id backup.RestoreID) backup.RestoreRecord {
	t.Helper()

	var last backup.RestoreRecord

	for range breakGlassRestoreRuns {
		row, err := restores.Get(t.Context(), id)
		if err != nil {
			t.Fatalf("get restore: %v", err)
		}

		last = row

		if row.State != backup.RestoreChecking && row.State != backup.RestoreApplying {
			return row
		}

		if err := restores.RunOnce(t.Context()); err != nil && !errors.Is(err, durable.ErrNoWork) &&
			t.Context().Err() == nil {
			t.Logf("run a restore operation: %v", err)
		}
	}

	t.Fatalf("restore %s did not end after %d runs (state %q)", id, breakGlassRestoreRuns, last.State)

	return last
}

// checkRestoreFailureEntry proves that the audit log of the installation holds
// the failure entry of the refused restore, which says that the live data did
// not change.
func checkRestoreFailureEntry(t *testing.T, st *sqlite.Store, id backup.RestoreID) {
	t.Helper()

	listing, err := st.List(t.Context(), audit.ServerScope, audit.Page{Size: audit.MaxPageSize, Before: 0})
	if err != nil {
		t.Fatalf("list the server audit log: %v", err)
	}

	for i := range listing.Entries {
		e := &listing.Entries[i]
		if e.Action == string(identity.ActionServerRestore) && e.Outcome == audit.OutcomeFailure &&
			e.Subject == "tester" && strings.Contains(e.Object, "restore "+id.String()) &&
			strings.Contains(e.Object, "failed before the swap") {
			return
		}
	}

	t.Fatalf("the server audit log holds no failure entry of restore %s: %+v", id, listing.Entries)
}
