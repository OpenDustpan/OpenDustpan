package postgres_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/access/accesstest"
	"opendustpan.org/opendustpan/pkg/accesstoken/accesstokentest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/audit/audittest"
	"opendustpan.org/opendustpan/pkg/backup"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/principal/principaltest"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
	"opendustpan.org/opendustpan/pkg/session/sessiontest"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/storetest"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// envURL names the variable holding a PostgreSQL URL for integration tests.
// Each subtest gets its own schema, so the database can be shared.
const envURL = "DUSTPAN_TEST_POSTGRES_URL"

func TestConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.Run(t, func(t *testing.T) storetest.Store {
		t.Helper()

		return open(t, base)
	})
}

// TestSharedConformance runs the two-handle suite: both handles open one
// schema, as two servers open one database.
func TestSharedConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunShared(t, func(t *testing.T) (storetest.Store, storetest.Store) {
		t.Helper()

		schemaURL := isolatedURL(t, base)

		return openURL(t, schemaURL), openURL(t, schemaURL)
	})
}

// TestAccessConformance runs the role and grant store suite.
func TestAccessConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	accesstest.Run(t, func(t *testing.T) access.Store {
		t.Helper()

		return open(t, base)
	})
}

// TestDelegation runs the suite of the tokens of service principals.
func TestDelegation(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL delegation suite", envURL)
	}

	storetest.RunDelegation(t, func(t *testing.T) storetest.DelegationStore {
		t.Helper()

		return open(t, base)
	})
}

// TestAccessTokenConformance runs the access token store suite.
func TestAccessTokenConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	accesstokentest.Run(t, func(t *testing.T) accesstokentest.Store {
		t.Helper()

		return open(t, base)
	})
}

// TestSessionConformance runs the session store suite.
func TestSessionConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	sessiontest.Run(t, func(t *testing.T) sessiontest.Store {
		t.Helper()

		return open(t, base)
	})
}

// TestNamespacePrincipalConformance runs the suite of the principals of a
// namespace.
func TestNamespacePrincipalConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	principaltest.RunNamespace(t, func(t *testing.T) principaltest.NamespaceStore {
		t.Helper()

		return open(t, base)
	})
}

// TestPrincipalConformance runs the principal and group store suite.
func TestPrincipalConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	principaltest.Run(t, func(t *testing.T) principal.Store {
		t.Helper()

		return open(t, base)
	})
}

func TestSchemaConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunSchema(t, func(t *testing.T) storetest.SchemaStore {
		t.Helper()

		return open(t, base)
	})
}

// TestPingClosed checks Ping answers on an open store and returns exactly the
// sentinel text, with no driver text, after Close.
func TestPingClosed(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	s := open(t, base)

	if err := s.Ping(t.Context()); err != nil {
		t.Fatalf("ping open store: %v", err)
	}

	s.Close()

	err := s.Ping(t.Context())
	if err == nil || !errors.Is(err, namespace.ErrUnavailable) {
		t.Fatalf("ping closed store: err = %v, want ErrUnavailable", err)
	}

	if want := "ping postgres database: database unavailable"; err.Error() != want {
		t.Fatalf("ping closed store error = %q, want %q", err, want)
	}
}

func TestSecretsConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	secrettest.Run(t, func(t *testing.T) secrettest.Store {
		t.Helper()

		return open(t, base)
	})
}

func TestInstallationSecretsConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	secrettest.RunInstallation(t, func(t *testing.T) secrettest.InstallationStore {
		t.Helper()

		return open(t, base)
	})
}

func TestUpdatesConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunUpdates(t, func(t *testing.T) storetest.UpdatesStore {
		t.Helper()

		return openSQLUpdates(t, base)
	})
}

// TestAuditFeedConformance runs the audit feed cases: entries above a
// sequence and the cursors of the log sinks.
func TestAuditFeedConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	audittest.RunFeed(t, func(t *testing.T) audittest.FeedStore {
		t.Helper()

		s, err := postgres.Open(t.Context(), isolatedURL(t, base))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(s.Close)

		return s
	})
}

// TestAuditConformance runs the audit store suite: append, list order,
// paging, fencing, isolation, concurrent appends and the chain check. The
// tamper hooks change or remove a row through SQL: the port has no update
// or delete path, so only SQL plants a changed field or a deleted entry.
func TestAuditConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	audittest.Run(t, func(t *testing.T) audittest.TestStore {
		t.Helper()

		schemaURL := isolatedURL(t, base)

		s, err := postgres.Open(t.Context(), schemaURL)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(s.Close)

		return audittest.TestStore{
			Store: s,
			TamperEntry: func(ctx context.Context, ns namespace.Name, seq int64, field, value string) error {
				return tamperAuditEntry(ctx, schemaURL, ns, seq, auditFieldChange{field: field, value: value})
			},
			DeleteEntry: func(ctx context.Context, ns namespace.Name, seq int64) error {
				return deleteAuditEntry(ctx, schemaURL, ns, seq)
			},
		}
	})
}

// TestAuditEntryReaderConformance runs the audit.EntryReader cases: one
// entry at a sequence and the last entry of a scope.
func TestAuditEntryReaderConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	audittest.RunEntryReader(t, func(t *testing.T) audittest.EntryStore {
		t.Helper()

		s, err := postgres.Open(t.Context(), isolatedURL(t, base))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(s.Close)

		return s
	})
}

// TestAuditChainConformance runs the chain switch cases: entries without a
// hash, runs of chained entries, and the recorded chain state.
func TestAuditChainConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	audittest.RunChain(t, func(t *testing.T) audittest.ChainTestStore {
		t.Helper()

		schemaURL := isolatedURL(t, base)

		s, err := postgres.Open(t.Context(), schemaURL)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(s.Close)

		return audittest.ChainTestStore{
			Store: s,
			TamperEntry: func(t *testing.T, ns namespace.Name, seq int64, field, value string) {
				t.Helper()

				change := auditFieldChange{field: field, value: value}
				if err := tamperAuditEntry(t.Context(), schemaURL, ns, seq, change); err != nil {
					t.Fatalf("change entry %d: %v", seq, err)
				}
			},
			Reopen: func(t *testing.T) audittest.ChainStore {
				t.Helper()

				second, err := postgres.Open(t.Context(), schemaURL)
				if err != nil {
					t.Fatalf("reopen store: %v", err)
				}

				t.Cleanup(second.Close)

				return second
			},
		}
	})
}

// errUnknownAuditField reports a tamper hook field outside the whitelist.
var errUnknownAuditField = errors.New("unknown audit field")

// tamperAuditColumn maps a tamper hook field to its column. The column
// equals the field; the switch keeps the field name out of SQL unless it
// names one of the whitelisted columns.
func tamperAuditColumn(field string) (string, bool) {
	switch field {
	case "subject",
		"action",
		"object",
		"outcome",
		"kind",
		"hash",
		"prev_hash",
		"at_ms",
		"sequence",
		"changes",
		"fence_version",
		"value_path":
		return field, true
	default:
		return "", false
	}
}

// tamperValue answers the parameter of the tamper hook for column: at_ms,
// sequence and fence_version are numbers, every other column is text.
func tamperValue(column string) string {
	if column == "at_ms" || column == "sequence" || column == "fence_version" {
		return "$1::text::bigint"
	}

	return "$1"
}

// auditFieldChange names one column change of the tamper hook.
type auditFieldChange struct {
	field string
	value string
}

// tamperAuditEntry changes one column of one audit entry through a second
// database handle: the port has no update path.
func tamperAuditEntry(
	ctx context.Context, schemaURL string, ns namespace.Name, seq int64, change auditFieldChange,
) error {
	column, ok := tamperAuditColumn(change.field)
	if !ok {
		return fmt.Errorf("%w: %q", errUnknownAuditField, change.field)
	}

	conn, err := pgx.Connect(ctx, schemaURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	defer func() {
		_ = conn.Close(ctx) //nolint:errcheck // Test helper: the Exec result below carries the verdict.
	}()

	_, execErr := conn.Exec(
		ctx,
		`UPDATE audit_entries SET `+column+` = `+tamperValue(column)+` WHERE namespace = $2 AND sequence = $3`,
		change.value,
		ns.String(),
		seq,
	)
	if execErr != nil {
		return fmt.Errorf("change audit entry: %w", execErr)
	}

	return nil
}

// deleteAuditEntry removes one audit entry through a second database handle:
// the port has no delete path.
func deleteAuditEntry(ctx context.Context, schemaURL string, ns namespace.Name, seq int64) error {
	conn, err := pgx.Connect(ctx, schemaURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	defer func() {
		_ = conn.Close(ctx) //nolint:errcheck // Test helper: the Exec result below carries the verdict.
	}()

	_, execErr := conn.Exec(
		ctx, `DELETE FROM audit_entries WHERE namespace = $1 AND sequence = $2`, ns.String(), seq,
	)
	if execErr != nil {
		return fmt.Errorf("delete audit entry: %w", execErr)
	}

	return nil
}

// TestUpdateHistoryConformance runs the history cases of ADR 28 decision 4
// on PostgreSQL: the creator and restore source survive, and history reports
// the checkpoint size.
func TestUpdateHistoryConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunUpdateHistory(t, func(t *testing.T) storetest.HistoryStore {
		t.Helper()

		return open(t, base)
	})
}

func TestWorkspaceConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunWorkspace(t, func(t *testing.T) storetest.WorkspaceStore {
		t.Helper()

		return openAuditedWorkspaces(t, base)
	})
}

func TestIPAllowlistConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunIPAllowlist(t, func(t *testing.T) storetest.IPAllowlistStore {
		t.Helper()

		return open(t, base)
	})
}

func TestNamespacePagingConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunNamespacePaging(t, func(t *testing.T) storetest.PagingStore {
		t.Helper()

		return open(t, base)
	})
}

func TestBackupConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunBackup(t, func(t *testing.T) storetest.BackupStore {
		t.Helper()

		return openBackup(t, base)
	})
}

func TestPortableExportConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL portable export suite", envURL)
	}

	storetest.RunPortable(t, func(t *testing.T) storetest.PortableStore {
		t.Helper()

		return openBackup(t, base)
	})
}

func TestRestoreConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunRestore(t, func(t *testing.T) storetest.RestoreStore {
		t.Helper()

		return openBackup(t, base)
	})
}

// restoreAuditMaintenance enters and leaves maintenance mode around a
// restore at once: the audit-entry test runs no concurrent work.
type restoreAuditMaintenance struct{}

// Enter implements backup.RestoreMaintenance.
func (restoreAuditMaintenance) Enter(
	_ context.Context, _ bool,
) (maintenance.EnterResult, error) {
	return maintenance.EnterResult{Canceled: nil}, nil
}

// Leave implements backup.RestoreMaintenance.
func (restoreAuditMaintenance) Leave(_ context.Context) error {
	return nil
}

// TestRestoreLeavesAuditEntryAfterSwap proves a restore that runs through the
// swap leaves the restore audit entry, with the access mode, in the restored
// audit log: an entry written before the swap would go away with the old
// live data. It runs the same restore service the server wires, over a real
// store, with a backup taken just before, once replacing the access tables
// and once keeping the current rows.
func TestRestoreLeavesAuditEntryAfterSwap(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		keep bool
		want string
	}{
		{name: "replaces access tables", keep: false, want: "access tables replaced"},
		{name: "keeps access tables", keep: true, want: "access tables kept"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkRestoreAuditEntryAfterSwap(t, tc.keep, tc.want)
		})
	}
}

// checkRestoreAuditEntryAfterSwap restores a backup taken just before, keeping
// the access tables when keep is true, and proves the restored audit log
// holds the restore audit entry naming want.
func checkRestoreAuditEntryAfterSwap(t *testing.T, keep bool, want string) {
	t.Helper()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL restore suite", envURL)
	}

	ctx := t.Context()
	st := openBackup(t, base)
	live, target := openRestoreAuditBlobs(t)
	backups, restores := newRestoreAuditServices(t, st, live, target)

	backupID := backup.ID(ident.NewID("bkp-"))

	if _, runErr := backups.Run(ctx, backupID); runErr != nil {
		t.Fatalf("run backup %s: %v", backupID, runErr)
	}

	restoreID := backup.RestoreID(ident.NewID("rst-"))
	req := backup.RestoreRequest{
		BackupID:         backupID,
		PreBackupID:      "",
		CheckOnly:        false,
		Force:            false,
		RequestedBy:      "tester",
		KeepAccessTables: keep,
	}

	if startErr := restores.Start(ctx, restoreID, req); startErr != nil {
		t.Fatalf("start restore: %v", startErr)
	}

	waitRestoreSucceeded(t, restores, restoreID)
	assertRestoreAuditEntry(t, st, restoreID, backupID, want)
}

// openRestoreAuditBlobs opens the live and target blob stores of the
// audit-entry test, each in its own temporary directory. The two stores
// stay unnamed, as nonamedreturns requires; the first is the live store, the
// second is the target.
//
//nolint:revive // Unnamed two-store result per nonamedreturns; order is documented.
func openRestoreAuditBlobs(t *testing.T) (*blobfs.Store, *blobfs.Store) {
	t.Helper()

	live, openErr := blobfs.Open(t.TempDir())
	if openErr != nil {
		t.Fatalf("open live blob store: %v", openErr)
	}

	t.Cleanup(func() {
		if closeErr := live.Close(); closeErr != nil {
			t.Errorf("close live blob store: %v", closeErr)
		}
	})

	target, openErr := blobfs.Open(t.TempDir())
	if openErr != nil {
		t.Fatalf("open backup target: %v", openErr)
	}

	t.Cleanup(func() {
		if closeErr := target.Close(); closeErr != nil {
			t.Errorf("close backup target: %v", closeErr)
		}
	})

	return live, target
}

// newRestoreAuditServices wires the backup and restore services of the
// audit-entry test over st, the same services the server wires.
func newRestoreAuditServices(
	t *testing.T, st *backupStore, live, target *blobfs.Store,
) (*backup.Service, *backup.RestoreService) {
	t.Helper()

	backups, err := backup.NewService(backup.Config{
		Exporter:    st,
		Registry:    st,
		Blobs:       live,
		StateOpener: nil,
		Target:      target,
		TargetName:  "dir:test",
		Now:         nil,
		Version:     "test",
		MaxSQLBytes: 0,
	})
	if err != nil {
		t.Fatalf("new backup service: %v", err)
	}

	restores, err := backup.NewRestoreService(backup.RestoreConfig{
		Live:        st,
		Registry:    st,
		Backups:     backups,
		Maintenance: restoreAuditMaintenance{},
		Blobs:       live,
		Target:      target,
		Key:         nil,
		Region:      "test",
		AfterApply:  nil,
		Admit:       nil,
		Now:         nil,
		Logger:      nil,
	})
	if err != nil {
		t.Fatalf("new restore service: %v", err)
	}

	return backups, restores
}

// waitRestoreSucceeded polls the restore id until it succeeds, failing on
// any other end state or when the wait times out.
func waitRestoreSucceeded(t *testing.T, restores *backup.RestoreService, id backup.RestoreID) {
	t.Helper()

	ctx := t.Context()
	deadline := time.Now().Add(time.Minute)

	for {
		row, getErr := restores.Get(ctx, id)
		if getErr != nil {
			t.Fatalf("get restore: %v", getErr)
		}

		if row.State == backup.RestoreSucceeded {
			return
		}

		if row.State != backup.RestoreChecking && row.State != backup.RestoreApplying {
			t.Fatalf("restore state = %q, want succeeded (error %q)", row.State, row.Error)
		}

		if time.Now().After(deadline) {
			t.Fatalf("restore %s never succeeded", id)
		}

		select {
		case <-ctx.Done():
			t.Fatalf("restore %s: %v", id, ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// assertRestoreAuditEntry proves the restored audit log holds the restore
// audit entry of id over backupID, naming the want access mode.
func assertRestoreAuditEntry(
	t *testing.T, st *backupStore, id backup.RestoreID, backupID backup.ID, wantMode string,
) {
	t.Helper()

	listing, err := st.List(
		t.Context(), audit.ServerScope, audit.Page{Number: 1, Size: audit.DefaultPageSize},
	)
	if err != nil {
		t.Fatalf("list entries: %v", err)
	}

	want := "restore " + id.String() + " of backup " + backupID.String() + " (" + wantMode + ")"

	for i := range listing.Entries {
		entry := listing.Entries[i]
		if entry.Action != string(identity.ActionServerRestore) {
			continue
		}

		if entry.Object != want {
			t.Fatalf("restore audit object = %q, want %q", entry.Object, want)
		}

		return
	}

	t.Fatalf("no audit entry holds %q", want)
}

// backupStore wraps a PostgreSQL store with the URL of its schema, so the
// suite can close and reopen the same backing storage.
type backupStore struct {
	*postgres.Store

	url string
	t   *testing.T
}

var (
	_ storetest.Reopener       = (*backupStore)(nil)
	_ storetest.StagingWitness = (*backupStore)(nil)
)

// openBackup returns a backup store in a fresh schema of the database at base.
func openBackup(t *testing.T, base string) *backupStore {
	t.Helper()

	schemaURL := isolatedURL(t, base)

	s, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	return &backupStore{Store: s, url: schemaURL, t: t}
}

// Close implements storetest.Reopener: it releases the pool. Closing twice is
// safe, so the cleanup above stays.
func (s *backupStore) Close() error {
	s.Store.Close()

	return nil
}

// Reopen implements storetest.Reopener: it reconnects to the same schema. A
// reconnect preserves running backup rows; the server, not Open, marks them
// failed at start.
func (s *backupStore) Reopen() storetest.BackupStore { //nolint:ireturn // Reopener returns a new handle as the interface.
	reopened, err := postgres.Open(s.t.Context(), s.url)
	if err != nil {
		s.t.Fatalf("reopen store: %v", err)
	}

	s.t.Cleanup(reopened.Close)

	return &backupStore{Store: reopened, url: s.url, t: s.t}
}

// CurrentStagingID implements storetest.StagingWitness: the name of the kept
// staging schema, or empty when no staging area exists.
func (s *backupStore) CurrentStagingID() string {
	return s.StagingSchemaForTest()
}

// StagingAlive implements storetest.StagingWitness: whether the staging schema
// with id still exists. A query error reads as not alive, so the conformance
// case fails instead of passing on a broken check.
func (s *backupStore) StagingAlive(id string) bool {
	if id == "" {
		return false
	}

	ctx := context.Background()

	conn, err := pgx.Connect(ctx, s.url)
	if err != nil {
		return false
	}

	defer func() {
		_ = conn.Close(ctx) //nolint:errcheck // Test-only existence check.
	}()

	var count int

	const query = `SELECT COUNT(*) FROM information_schema.schemata WHERE schema_name = $1`

	if err := conn.QueryRow(ctx, query, id).Scan(&count); err != nil {
		return false
	}

	return count == 1
}

// open returns a store in a fresh schema of the database at base.
func open(tb testing.TB, base string) *postgres.Store {
	tb.Helper()

	return openURL(tb, isolatedURL(tb, base))
}

// openURL returns a store on the schema that schemaURL names. Each call
// opens its own handle, so two calls give two handles on one schema.
func openURL(tb testing.TB, schemaURL string) *postgres.Store {
	tb.Helper()

	s, err := postgres.Open(tb.Context(), schemaURL)
	if err != nil {
		tb.Fatalf("open store: %v", err)
	}

	tb.Cleanup(s.Close)

	return s
}

// sqlUpdatesStore wraps a Store with its schema URL, so a case can change its
// rows with direct SQL, as the audit case does to break audit appends.
type sqlUpdatesStore struct {
	storetest.UpdatesStore

	url string
	t   *testing.T
}

var _ storetest.AuditBreaker = (*sqlUpdatesStore)(nil)

// errNoJournalCount reports a wrapped store without the journaled count.
var errNoJournalCount = errors.New("wrapped store does not count running journaled updates")

// openSQLUpdates returns a store in a fresh schema of the database at base
// that a case can change through SQL.
func openSQLUpdates(t *testing.T, base string) *sqlUpdatesStore {
	t.Helper()

	schemaURL := isolatedURL(t, base)

	s, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	return &sqlUpdatesStore{UpdatesStore: s, url: schemaURL, t: t}
}

// CountRunningJournaledUpdates forwards to the wrapped store, whose method
// the embedded UpdatesStore interface hides.
func (s *sqlUpdatesStore) CountRunningJournaledUpdates(ctx context.Context) (int64, error) {
	counter, ok := s.UpdatesStore.(interface {
		CountRunningJournaledUpdates(ctx context.Context) (int64, error)
	})
	if !ok {
		return 0, fmt.Errorf("count running journaled updates: %w", errNoJournalCount)
	}

	count, err := counter.CountRunningJournaledUpdates(ctx)
	if err != nil {
		return 0, fmt.Errorf("count running journaled updates: %w", err)
	}

	return count, nil
}

// List forwards to the audit list of the wrapped store, which the embedded
// interface hides, so the delete case can read the entry of a delete.
// Inner implements storetest.Wrapper.
func (s *sqlUpdatesStore) Inner() any {
	return s.UpdatesStore
}

func (s *sqlUpdatesStore) List(ctx context.Context, ns namespace.Name, page audit.Page) (audit.Listing, error) {
	lister, ok := s.UpdatesStore.(audit.Store)
	if !ok {
		return audit.Listing{}, errNoAuditList
	}

	listing, err := lister.List(ctx, ns, page)
	if err != nil {
		return audit.Listing{}, fmt.Errorf("list audit entries: %w", err)
	}

	return listing, nil
}

// errNoAuditList reports a wrapped store without an audit list.
var errNoAuditList = errors.New("wrapped store lists no audit entries")

// BreakAuditAppends implements storetest.AuditBreaker.
func (s *sqlUpdatesStore) BreakAuditAppends(ctx context.Context) error {
	return breakAuditAppends(ctx, s.url)
}

// auditedWorkspaces is a store for the workspace suite that can break its
// audit appends. It embeds the store itself, so the suite also reads its
// audit list.
type auditedWorkspaces struct {
	*postgres.Store

	url string
}

var _ storetest.AuditBreaker = (*auditedWorkspaces)(nil)

// BreakAuditAppends implements storetest.AuditBreaker.
func (s *auditedWorkspaces) BreakAuditAppends(ctx context.Context) error {
	return breakAuditAppends(ctx, s.url)
}

// openAuditedWorkspaces returns a store in a fresh schema of the database at
// base whose audit appends the suite can break.
func openAuditedWorkspaces(t *testing.T, base string) *auditedWorkspaces {
	t.Helper()

	schemaURL := isolatedURL(t, base)

	s, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	return &auditedWorkspaces{Store: s, url: schemaURL}
}

// breakAuditAppends creates a trigger in the schema at schemaURL that raises an
// error on every insert of an audit entry.
func breakAuditAppends(ctx context.Context, schemaURL string) (err error) {
	conn, err := pgx.Connect(ctx, schemaURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close: %w", closeErr))
		}
	}()

	_, err = conn.Exec(ctx, `CREATE FUNCTION break_audit_appends() RETURNS trigger
LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'audit append broken by the test'; END $$;
CREATE TRIGGER break_audit_appends BEFORE INSERT ON audit_entries
FOR EACH ROW EXECUTE FUNCTION break_audit_appends();`)
	if err != nil {
		return fmt.Errorf("create the trigger: %w", err)
	}

	return nil
}

// TestUnknownStoredStateIsUnavailable plants a stored installation state
// outside the known set and proves GetState answers ErrUnavailable without
// the value, never ErrInvalidState: an unknown stored value is a server
// fault, not a bad request.
func TestUnknownStoredStateIsUnavailable(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	schemaURL := isolatedURL(t, base)

	s, openErr := postgres.Open(t.Context(), schemaURL)
	if openErr != nil {
		t.Fatalf("open store: %v", openErr)
	}

	t.Cleanup(s.Close)

	ctx := t.Context()

	conn, connErr := pgx.Connect(ctx, schemaURL)
	if connErr != nil {
		t.Fatalf("connect: %v", connErr)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	const dropStateCheck = `ALTER TABLE installation DROP CONSTRAINT installation_state_check`

	if _, execErr := conn.Exec(ctx, dropStateCheck); execErr != nil {
		t.Fatalf("drop installation state check: %v", execErr)
	}

	if _, execErr := conn.Exec(ctx, `UPDATE installation SET state = 'offline'`); execErr != nil {
		t.Fatalf("plant unknown installation state: %v", execErr)
	}

	_, stateErr := s.GetState(ctx)
	if !errors.Is(stateErr, maintenance.ErrUnavailable) {
		t.Fatalf("get unknown installation state: err = %v, want ErrUnavailable", stateErr)
	}

	if errors.Is(stateErr, maintenance.ErrInvalidState) {
		t.Fatalf("get unknown installation state: err = %v, must not report ErrInvalidState", stateErr)
	}

	if strings.Contains(stateErr.Error(), "offline") {
		t.Fatalf("get unknown installation state error %q holds the stored value", stateErr)
	}
}

// the renamed stack keeps its tags and ID, and the old name is gone.
// TestRenameKeepsTags checks that tags follow the stack ID through a rename:
// the renamed stack keeps its tags and ID, and the old name is gone.
func TestRenameKeepsTags(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	s := open(t, base)
	ctx := t.Context()
	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)

	ns := namespace.Namespace{
		CreatedAt:       now,
		Name:            "acme",
		HomeRegion:      "eu-west",
		FailoverVersion: 1,
		Overrides:       namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	ref, parseErr := stack.ParseRef("acme", "web", "prod")
	if parseErr != nil {
		t.Fatalf("parse ref: %v", parseErr)
	}

	st := stack.Stack{
		CreatedAt: now,
		UpdatedAt: now,
		Tags:      map[string]string{"team": "platform"},
		Ref:       ref,
		ID:        stack.ID(ident.NewID("stk-")),
		Version:   0,
	}

	if err := s.CreateStack(ctx, st, ns.Fence()); err != nil {
		t.Fatalf("create stack: %v", err)
	}

	next := map[string]string{"team": "e2e"}
	if err := s.UpdateStackTags(ctx, ref, next, now, ns.Fence()); err != nil {
		t.Fatalf("update tags: %v", err)
	}

	if err := s.RenameStack(ctx, ref, "renamed", ns.Fence()); err != nil {
		t.Fatalf("rename stack: %v", err)
	}

	renamed, err := stack.ParseRef("acme", "web", "renamed")
	if err != nil {
		t.Fatalf("parse renamed ref: %v", err)
	}

	got, err := s.GetStack(ctx, renamed)
	if err != nil {
		t.Fatalf("get renamed stack: %v", err)
	}

	if !maps.Equal(got.Tags, next) {
		t.Fatalf("renamed tags = %v, want %v", got.Tags, next)
	}

	if got.ID != st.ID {
		t.Fatalf("renamed ID = %q, want %q", got.ID, st.ID)
	}

	if _, err := s.GetStack(ctx, ref); !errors.Is(err, stack.ErrNotFound) {
		t.Fatalf("get old name: err = %v, want ErrNotFound", err)
	}
}

func TestMigration0003FillsIDs(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	migrated := isolatedURL(t, base)
	seedAt0002(t, migrated)

	s, err := postgres.Open(t.Context(), migrated)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}

	t.Cleanup(s.Close)

	list, err := s.ListStacks(t.Context(), stack.Filter{Namespace: "acme", Project: ""})
	if err != nil {
		t.Fatalf("list stacks: %v", err)
	}

	if len(list) != 2 {
		t.Fatalf("migrated stacks = %d, want 2", len(list))
	}

	seen := map[stack.ID]bool{}

	for _, st := range list {
		if st.ID == "" || !strings.HasPrefix(st.ID.String(), "stk-") {
			t.Fatalf("migrated stack %s ID = %q, want stk- prefix", st.Ref, st.ID)
		}

		if seen[st.ID] {
			t.Fatalf("duplicate migrated ID %q", st.ID)
		}

		seen[st.ID] = true
	}
}

func TestMigration0005KeepsHistory(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	migrated := isolatedURL(t, base)
	seedAt0003WithHistory(t, migrated)

	s, err := postgres.Open(t.Context(), migrated)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}

	t.Cleanup(s.Close)

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	checkMigratedStack(t, s, ref)
	checkMigratedUpdate(t, s, ref)
	checkMigratedHistory(t, s, ref)
	checkMigratedCheckpoint(t, s, ref)
	checkMigratedEvents(t, s, ref)
}

func checkMigratedStack(t *testing.T, s *postgres.Store, ref stack.Ref) {
	t.Helper()

	gotStack, err := s.GetStack(t.Context(), ref)
	if err != nil {
		t.Fatalf("get stack: %v", err)
	}

	if gotStack.ID.String() != "stk-aaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("migrated stack ID = %q, want stk-aaaaaaaaaaaaaaaaaaaa", gotStack.ID)
	}
}

func checkMigratedUpdate(t *testing.T, s *postgres.Store, ref stack.Ref) {
	t.Helper()

	gotUpdate, err := s.GetUpdate(t.Context(), ref, "u1")
	if err != nil {
		t.Fatalf("get update: %v", err)
	}

	if gotUpdate.Version != 1 || gotUpdate.Status != stack.StatusSucceeded {
		t.Fatalf("migrated update = version %d status %q, want 1 succeeded", gotUpdate.Version, gotUpdate.Status)
	}
}

func checkMigratedHistory(t *testing.T, s *postgres.Store, ref stack.Ref) {
	t.Helper()

	history, err := s.ListUpdates(t.Context(), ref, stack.Page{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("list updates: %v", err)
	}

	if len(history) != 1 || history[0].ID != "u1" {
		t.Fatalf("migrated history = %+v, want one update u1", history)
	}
}

func checkMigratedCheckpoint(t *testing.T, s *postgres.Store, ref stack.Ref) {
	t.Helper()

	cp, err := s.LatestCheckpoint(t.Context(), ref)
	if err != nil {
		t.Fatalf("latest checkpoint: %v", err)
	}

	if string(cp.Deployment) != "{}" || cp.Update != "u1" {
		t.Fatalf("migrated checkpoint = deployment %q update %q, want {} u1", cp.Deployment, cp.Update)
	}
}

func checkMigratedEvents(t *testing.T, s *postgres.Store, ref stack.Ref) {
	t.Helper()

	events, err := s.ListEvents(t.Context(), ref, "u1", -1, 10)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}

	if len(events) != 2 {
		t.Fatalf("migrated events = %d, want 2", len(events))
	}
}

func TestMigration0006KeepsOneRowData(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	migrated := isolatedURL(t, base)
	seedAt0005WithOneRow(t, migrated)

	s, err := postgres.Open(t.Context(), migrated)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}

	t.Cleanup(s.Close)

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	cp, err := s.LatestCheckpoint(t.Context(), ref)
	if err != nil {
		t.Fatalf("latest checkpoint: %v", err)
	}

	if string(cp.Deployment) != `{"v":1}` || cp.Update != "u1" {
		t.Fatalf("migrated checkpoint = deployment %q update %q, want {\"v\":1} u1", cp.Deployment, cp.Update)
	}

	byVersion, err := s.CheckpointByVersion(t.Context(), ref, 1)
	if err != nil {
		t.Fatalf("checkpoint of version 1: %v", err)
	}

	if string(byVersion.Deployment) != `{"v":1}` || byVersion.Update != "u1" {
		t.Fatalf(
			"migrated checkpoint of version 1 = deployment %q update %q, want {\"v\":1} u1",
			byVersion.Deployment,
			byVersion.Update,
		)
	}

	if _, err := s.CheckpointByVersion(t.Context(), ref, 2); !errors.Is(err, stack.ErrNoState) {
		t.Fatalf("checkpoint of missing version 2: err = %v, want ErrNoState", err)
	}
}

// seedAt0005WithOneRow creates a database at migration 0005 with a stack, one
// finished update and its checkpoint: the one-row-per-stack layout.
func seedAt0005WithOneRow(t *testing.T, migrated string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	applyMigrations0005(t, conn)
	recordMigrations0005(t, conn)
	seedOneRowNamespace(t, conn)
	seedOneRowStack(t, conn)
	seedOneRowUpdate(t, conn)
	seedOneRowCheckpoint(t, conn)
}

func applyMigrations0005(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	for _, name := range []string{
		"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql",
		"0004_updates_stack_id.sql", "0005_updates_drop_names.sql",
	} {
		data, readErr := os.ReadFile( //nolint:gosec // test fixture reads fixed migration files
			filepath.Join("migrations", name),
		)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		if _, execErr := conn.Exec(t.Context(), string(data)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
}

func recordMigrations0005(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const createMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT NOT NULL PRIMARY KEY,
		name    TEXT   NOT NULL
	)`

	if _, err := conn.Exec(t.Context(), createMigrations); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`

	names := []string{
		"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql",
		"0004_updates_stack_id.sql", "0005_updates_drop_names.sql",
	}

	for i, name := range names {
		if _, err := conn.Exec(t.Context(), record, int64(i+1), name); err != nil {
			t.Fatalf("record migration %s: %v", name, err)
		}
	}
}

func seedOneRowNamespace(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO namespaces (name, home_region, failover_version, created_at_ms)
		VALUES ('acme', 'eu-west', 1, 0)`

	if _, err := conn.Exec(t.Context(), seed); err != nil {
		t.Fatalf("seed namespace: %v", err)
	}
}

func seedOneRowStack(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO stacks (namespace, project, name, id, tags, version, created_at_ms, updated_at_ms)
		VALUES ('acme', 'web', 'prod', 'stk-aaaaaaaaaaaaaaaaaaaa', '{}', 1, 0, 0)`

	if _, err := conn.Exec(t.Context(), seed); err != nil {
		t.Fatalf("seed stack: %v", err)
	}
}

func seedOneRowUpdate(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO updates (stack_id, id, kind, dry_run, status, version, revision,
		lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms)
		VALUES ('stk-aaaaaaaaaaaaaaaaaaaa', 'u1', 'update', false, 'succeeded', 1, 3, ''::bytea, 0, 1000, 2000, 3000)`

	if _, err := conn.Exec(t.Context(), seed); err != nil {
		t.Fatalf("seed update: %v", err)
	}
}

func seedOneRowCheckpoint(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO checkpoints (stack_id, update_id, lease_digest, schema_version, sequence,
		digest, invalid, features, deployment, saved_at_ms)
		VALUES ('stk-aaaaaaaaaaaaaaaaaaaa', 'u1', ''::bytea, 3, 0,
		'\x0000000000000000000000000000000000000000000000000000000000000000', false, '[]', '{"v":1}', 4000)`

	if _, err := conn.Exec(t.Context(), seed); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
}

// seedAt0003WithHistory creates a database at migration 0003 with a stack,
// one update, its checkpoint and two events, all keyed by name.
func seedAt0003WithHistory(t *testing.T, migrated string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	applyMigrations0003(t, conn)
	recordMigrations0003(t, conn)
	seedHistoryNamespace(t, conn)
	seedHistoryStack(t, conn)
	seedHistoryUpdate(t, conn)
	seedHistoryCheckpoint(t, conn)
	seedHistoryEvents(t, conn)
}

func applyMigrations0003(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	for _, name := range []string{"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql"} {
		data, readErr := os.ReadFile( //nolint:gosec // test fixture reads fixed migration files
			filepath.Join("migrations", name),
		)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		_, execErr := conn.Exec(t.Context(), string(data))
		if execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
}

func recordMigrations0003(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const createMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT NOT NULL PRIMARY KEY,
		name    TEXT   NOT NULL
	)`

	_, err := conn.Exec(t.Context(), createMigrations)
	if err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`

	for _, m := range []struct {
		version int64
		name    string
	}{{version: 1, name: "0001_init.sql"}, {version: 2, name: "0002_updates.sql"}, {version: 3, name: "0003_stack_id.sql"}} {
		_, err := conn.Exec(t.Context(), record, m.version, m.name)
		if err != nil {
			t.Fatalf("record migration %s: %v", m.name, err)
		}
	}
}

func seedHistoryNamespace(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO namespaces (name, home_region, failover_version, created_at_ms)
		VALUES ('acme', 'eu-west', 1, 0)`

	_, err := conn.Exec(t.Context(), seed)
	if err != nil {
		t.Fatalf("seed namespace: %v", err)
	}
}

func seedHistoryStack(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO stacks (namespace, project, name, id, tags, version, created_at_ms, updated_at_ms)
		VALUES ('acme', 'web', 'prod', 'stk-aaaaaaaaaaaaaaaaaaaa', '{}', 1, 0, 0)`

	_, err := conn.Exec(t.Context(), seed)
	if err != nil {
		t.Fatalf("seed stack: %v", err)
	}
}

func seedHistoryUpdate(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO updates (namespace, project, stack, id, kind, dry_run, status, version, revision,
		lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms)
		VALUES ('acme', 'web', 'prod', 'u1', 'update', false, 'succeeded', 1, 3, ''::bytea, 0, 1000, 2000, 3000)`

	_, err := conn.Exec(t.Context(), seed)
	if err != nil {
		t.Fatalf("seed update: %v", err)
	}
}

func seedHistoryCheckpoint(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO checkpoints (namespace, project, stack, update_id, lease_digest, schema_version, sequence,
		digest, invalid, features, deployment, saved_at_ms)
		VALUES ('acme', 'web', 'prod', 'u1', ''::bytea, 3, 0,
		'\x0000000000000000000000000000000000000000000000000000000000000000', false, '[]', '{}', 4000)`

	_, err := conn.Exec(t.Context(), seed)
	if err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
}

func seedHistoryEvents(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO update_events (namespace, project, stack, update_id, sequence, timestamp_ms, payload)
		VALUES ('acme', 'web', 'prod', 'u1', $1, $2, '{}')`

	_, err := conn.Exec(t.Context(), seed, 0, 5000)
	if err != nil {
		t.Fatalf("seed event 0: %v", err)
	}

	_, err = conn.Exec(t.Context(), seed, 1, 5001)
	if err != nil {
		t.Fatalf("seed event 1: %v", err)
	}
}

func TestMigration0007KeepsTags(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	migrated := isolatedURL(t, base)
	seedAt0006WithTags(t, migrated)

	s, err := postgres.Open(t.Context(), migrated)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}

	t.Cleanup(s.Close)

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	got, err := s.GetStack(t.Context(), ref)
	if err != nil {
		t.Fatalf("get stack: %v", err)
	}

	if got.Tags["team"] != "platform" || len(got.Tags) != 1 {
		t.Fatalf("migrated tags = %v, want map[team:platform]", got.Tags)
	}
}

func TestMigration0009FillsInventory(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	migrated := isolatedURL(t, base)
	seedAt0008WithStacks(t, migrated)

	s, err := postgres.Open(t.Context(), migrated)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}

	t.Cleanup(s.Close)

	checkMigratedInventory(t, s)
}

// TestMigration0012FillsHistoryFields checks that updates from before the
// history columns read with an empty creator, no restore source and the
// checkpoint size from the join.
func TestMigration0012FillsHistoryFields(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	migrated := isolatedURL(t, base)
	seedAt0010WithUpdates(t, migrated)

	s, err := postgres.Open(t.Context(), migrated)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}

	t.Cleanup(s.Close)

	ref, err := stack.ParseRef("acme", "web", "prod")
	if err != nil {
		t.Fatalf("parse ref: %v", err)
	}

	checkMigratedHistoryUpdates(t, s, ref)
	checkMigratedHistoryList(t, s, ref)
}

// checkMigratedHistoryUpdates checks the migrated updates carry empty history fields
// and the checkpoint size of u1.
func checkMigratedHistoryUpdates(t *testing.T, s *postgres.Store, ref stack.Ref) {
	t.Helper()

	for _, want := range []struct {
		id   stack.UpdateID
		size int64
	}{
		{id: "u1", size: int64(len(`{"v":1}`))},
		{id: "u2", size: 0},
	} {
		checkMigratedHistoryUpdate(t, s, ref, want.id, want.size)
	}
}

// checkMigratedHistoryUpdate checks one migrated update against its expected size.
func checkMigratedHistoryUpdate(t *testing.T, s *postgres.Store, ref stack.Ref, id stack.UpdateID, size int64) {
	t.Helper()

	got, getErr := s.GetUpdate(t.Context(), ref, id)
	if getErr != nil {
		t.Fatalf("get update %s: %v", id, getErr)
	}

	if got.CreatedBy != "" || got.RestoredFrom != 0 || got.CheckpointBytes != size {
		t.Fatalf(
			"migrated update %s = creator %q restored %d size %d, want empty, 0 and %d",
			id,
			got.CreatedBy,
			got.RestoredFrom,
			got.CheckpointBytes,
			size,
		)
	}
}

// checkMigratedHistoryList checks the migrated history holds u2 then u1
// with empty history fields and the size of u1.
func checkMigratedHistoryList(t *testing.T, s *postgres.Store, ref stack.Ref) {
	t.Helper()

	history, err := s.ListUpdates(t.Context(), ref, stack.Page{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("list updates: %v", err)
	}

	if len(history) != 2 || history[0].ID != "u2" || history[0].CheckpointBytes != 0 ||
		history[1].ID != "u1" || history[1].CheckpointBytes != int64(len(`{"v":1}`)) {
		t.Fatalf("migrated history = %+v, want [u2 without checkpoint, u1 with size]", history)
	}

	for i := range history {
		if history[i].CreatedBy != "" || history[i].RestoredFrom != 0 {
			t.Fatalf(
				"migrated history %s = creator %q restored %d, want empty and 0",
				history[i].ID,
				history[i].CreatedBy,
				history[i].RestoredFrom,
			)
		}
	}
}

// seedAt0010WithUpdates creates a database at migration 0010 with a stack,
// two finished updates and the checkpoint of the first.
func seedAt0010WithUpdates(t *testing.T, migrated string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	applyMigrations0010(t, conn)
	recordMigrations0010(t, conn)
	seed0010Namespace(t, conn)
	seed0010Stack(t, conn)
	seed0010Updates(t, conn)
	seed0010Checkpoint(t, conn)
}

// seed0010Namespace seeds the namespace of the 0011 and 0012 migration tests.
func seed0010Namespace(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO namespaces (name, home_region, failover_version, created_at_ms)
		VALUES ('acme', 'eu-west', 1, 0)`

	if _, err := conn.Exec(t.Context(), seed); err != nil {
		t.Fatalf("seed namespace: %v", err)
	}
}

// seed0010Stack seeds one stack without tags, as the schema at 0010 holds it.
func seed0010Stack(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO stacks (namespace, project, name, id, version, created_at_ms, updated_at_ms)
		VALUES ('acme', 'web', 'prod', 'stk-aaaaaaaaaaaaaaaaaaaa', 2, 0, 0)`

	if _, err := conn.Exec(t.Context(), seed); err != nil {
		t.Fatalf("seed stack: %v", err)
	}
}

// applyMigrations0010 applies migrations 0001 to 0010 to conn.
func applyMigrations0010(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	names := []string{
		"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql",
		"0004_updates_stack_id.sql", "0005_updates_drop_names.sql", "0006_checkpoints_per_update.sql",
		"0007_stack_tags.sql", "0008_secret_keys.sql", "0009_inventory.sql", "0010_workspaces.sql",
	}

	for _, name := range names {
		data, readErr := os.ReadFile( //nolint:gosec // test fixture reads fixed migration files
			filepath.Join("migrations", name),
		)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		if _, execErr := conn.Exec(t.Context(), string(data)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
}

// recordMigrations0010 records migrations 0001 to 0010 as applied.
func recordMigrations0010(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const createMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT NOT NULL PRIMARY KEY,
		name    TEXT   NOT NULL
	)`

	if _, err := conn.Exec(t.Context(), createMigrations); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`

	names := []string{
		"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql",
		"0004_updates_stack_id.sql", "0005_updates_drop_names.sql", "0006_checkpoints_per_update.sql",
		"0007_stack_tags.sql", "0008_secret_keys.sql", "0009_inventory.sql", "0010_workspaces.sql",
	}

	for i, name := range names {
		if _, err := conn.Exec(t.Context(), record, int64(i+1), name); err != nil {
			t.Fatalf("record migration %s: %v", name, err)
		}
	}
}

// seed0010Updates seeds two finished updates without the history columns.
func seed0010Updates(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO updates (stack_id, id, kind, dry_run, status, version, revision,
		lease_digest, lease_expires_at_ms, created_at_ms, started_at_ms, ended_at_ms)
		VALUES ('stk-aaaaaaaaaaaaaaaaaaaa', $1, 'update', false, 'succeeded', $2, 3, ''::bytea, 0, 1000, 2000, 3000)`

	for _, u := range []struct {
		id      string
		version int64
	}{{id: "u1", version: 1}, {id: "u2", version: 2}} {
		if _, err := conn.Exec(t.Context(), seed, u.id, u.version); err != nil {
			t.Fatalf("seed update %s: %v", u.id, err)
		}
	}
}

// seed0010Checkpoint seeds the checkpoint of update u1.
func seed0010Checkpoint(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO checkpoints (stack_id, update_id, lease_digest, schema_version, sequence,
		digest, invalid, features, deployment, saved_at_ms)
		VALUES ('stk-aaaaaaaaaaaaaaaaaaaa', 'u1', ''::bytea, 3, 0,
		'\x0000000000000000000000000000000000000000000000000000000000000000', false, '[]', '{"v":1}', 4000)`

	if _, err := conn.Exec(t.Context(), seed); err != nil {
		t.Fatalf("seed checkpoint: %v", err)
	}
}

// checkMigratedInventory checks the migration filled one entry per stack.
func checkMigratedInventory(t *testing.T, s *postgres.Store) {
	t.Helper()

	entries, err := s.ListUnits(t.Context(), "acme")
	if err != nil {
		t.Fatalf("list inventory: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("migrated inventory entries = %+v, want two", entries)
	}

	for i := range entries {
		checkMigratedEntry(t, &entries[i])
	}
}

// checkMigratedEntry checks one migrated entry against its stack.
func checkMigratedEntry(t *testing.T, e *inventory.Entry) {
	t.Helper()

	want := map[string]struct {
		unitID      string
		lastChanged time.Time
	}{
		"web/prod": {unitID: "stk-aaaaaaaaaaaaaaaaaaaa", lastChanged: time.UnixMilli(2000).UTC()},
		"web/dev":  {unitID: "stk-bbbbbbbbbbbbbbbbbbbb", lastChanged: time.UnixMilli(3000).UTC()},
	}

	w, ok := want[e.Name]
	if !ok {
		t.Fatalf("migrated inventory entry name = %q, want web/prod or web/dev", e.Name)
	}

	if e.Tool != "pulumi" || e.Namespace.String() != "acme" || e.UnitID != w.unitID ||
		e.DisplayName != e.Name || !e.LastChanged.Equal(w.lastChanged) ||
		e.Status != "" || e.ResourceCount != nil {
		t.Fatalf("migrated inventory entry = %+v, want unit %q changed %v", e, w.unitID, w.lastChanged)
	}
}

// seedAt0008WithStacks creates a database at migration 0008 with two stacks
// and no inventory rows.
func seedAt0008WithStacks(t *testing.T, migrated string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	applyMigrations0008(t, conn)
	recordMigrations0008(t, conn)
	seedInventoryStacks(t, conn)
}

// applyMigrations0008 applies migrations 0001 to 0008 to conn.
func applyMigrations0008(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	names := []string{
		"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql",
		"0004_updates_stack_id.sql", "0005_updates_drop_names.sql", "0006_checkpoints_per_update.sql",
		"0007_stack_tags.sql", "0008_secret_keys.sql",
	}

	for _, name := range names {
		data, readErr := os.ReadFile( //nolint:gosec // test fixture reads fixed migration files
			filepath.Join("migrations", name),
		)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		if _, execErr := conn.Exec(t.Context(), string(data)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
}

// recordMigrations0008 records migrations 0001 to 0008 as applied.
func recordMigrations0008(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const createMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT NOT NULL PRIMARY KEY,
		name    TEXT   NOT NULL
	)`

	if _, err := conn.Exec(t.Context(), createMigrations); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`

	names := []string{
		"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql",
		"0004_updates_stack_id.sql", "0005_updates_drop_names.sql", "0006_checkpoints_per_update.sql",
		"0007_stack_tags.sql", "0008_secret_keys.sql",
	}

	for i, name := range names {
		if _, err := conn.Exec(t.Context(), record, int64(i+1), name); err != nil {
			t.Fatalf("record migration %s: %v", name, err)
		}
	}
}

// seedInventoryStacks seeds a namespace with two stacks.
func seedInventoryStacks(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seedNamespace = `INSERT INTO namespaces (name, home_region, failover_version, created_at_ms)
		VALUES ('acme', 'eu-west', 1, 0)`

	if _, err := conn.Exec(t.Context(), seedNamespace); err != nil {
		t.Fatalf("seed namespace: %v", err)
	}

	const seedStack = `INSERT INTO stacks (namespace, project, name, id, version, created_at_ms, updated_at_ms)
		VALUES ('acme', 'web', $1, $2, 0, 1000, $3)`

	for _, s := range []struct {
		name    string
		id      string
		updated int64
	}{
		{name: "prod", id: "stk-aaaaaaaaaaaaaaaaaaaa", updated: 2000},
		{name: "dev", id: "stk-bbbbbbbbbbbbbbbbbbbb", updated: 3000},
	} {
		if _, err := conn.Exec(t.Context(), seedStack, s.name, s.id, s.updated); err != nil {
			t.Fatalf("seed stack %s: %v", s.name, err)
		}
	}
}

// seedAt0006WithTags creates a database at migration 0006 with a stack whose
// tags still live in the stacks row.
func seedAt0006WithTags(t *testing.T, migrated string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	for _, name := range []string{
		"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql",
		"0004_updates_stack_id.sql", "0005_updates_drop_names.sql", "0006_checkpoints_per_update.sql",
	} {
		data, readErr := os.ReadFile( //nolint:gosec // test fixture reads fixed migration files
			filepath.Join("migrations", name),
		)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		if _, execErr := conn.Exec(t.Context(), string(data)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}

	const createMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT NOT NULL PRIMARY KEY,
		name    TEXT   NOT NULL
	)`

	if _, err := conn.Exec(t.Context(), createMigrations); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`

	names := []string{
		"0001_init.sql", "0002_updates.sql", "0003_stack_id.sql",
		"0004_updates_stack_id.sql", "0005_updates_drop_names.sql", "0006_checkpoints_per_update.sql",
	}

	for i, name := range names {
		if _, err := conn.Exec(t.Context(), record, int64(i+1), name); err != nil {
			t.Fatalf("record migration %s: %v", name, err)
		}
	}

	const seedNamespace = `INSERT INTO namespaces (name, home_region, failover_version, created_at_ms)
		VALUES ('acme', 'eu-west', 1, 0)`

	if _, err := conn.Exec(t.Context(), seedNamespace); err != nil {
		t.Fatalf("seed namespace: %v", err)
	}

	const seedStack = `INSERT INTO stacks (namespace, project, name, id, tags, version, created_at_ms, updated_at_ms)
		VALUES ('acme', 'web', 'prod', 'stk-aaaaaaaaaaaaaaaaaaaa', '{"team":"platform"}', 0, 0, 0)`

	if _, err := conn.Exec(t.Context(), seedStack); err != nil {
		t.Fatalf("seed stack: %v", err)
	}
}

// seedAt0002 creates a database at migration 0002 with two stacks and no IDs.
func seedAt0002(t *testing.T, migrated string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	applyBaseMigrations(t, conn)
	recordBaseMigrations(t, conn)
	seedBaseStacks(t, conn)
}

func applyBaseMigrations(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	for _, name := range []string{"0001_init.sql", "0002_updates.sql"} {
		data, readErr := os.ReadFile( //nolint:gosec // test fixture reads fixed migration files
			filepath.Join("migrations", name),
		)
		if readErr != nil {
			t.Fatalf("read %s: %v", name, readErr)
		}

		if _, execErr := conn.Exec(t.Context(), string(data)); execErr != nil {
			t.Fatalf("apply %s: %v", name, execErr)
		}
	}
}

func recordBaseMigrations(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const createMigrations = `CREATE TABLE IF NOT EXISTS schema_migrations (
		version BIGINT NOT NULL PRIMARY KEY,
		name    TEXT   NOT NULL
	)`

	if _, err := conn.Exec(t.Context(), createMigrations); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}

	const record = `INSERT INTO schema_migrations (version, name) VALUES ($1, $2)`

	for _, m := range []struct {
		version int64
		name    string
	}{{version: 1, name: "0001_init.sql"}, {version: 2, name: "0002_updates.sql"}} {
		if _, err := conn.Exec(t.Context(), record, m.version, m.name); err != nil {
			t.Fatalf("record migration: %v", err)
		}
	}
}

func seedBaseStacks(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	_, err := conn.Exec(
		t.Context(),
		`INSERT INTO namespaces (name, home_region, failover_version, created_at_ms) VALUES ('acme', 'eu-west', 1, 0)`,
	)
	if err != nil {
		t.Fatalf("seed namespace: %v", err)
	}

	const seedStack = `INSERT INTO stacks
		(namespace, project, name, tags, version, created_at_ms, updated_at_ms)
		VALUES ('acme', $1, $2, '{}', 0, 0, 0)`

	for _, s := range [][2]string{{"web", "prod"}, {"web", "dev"}} {
		if _, err := conn.Exec(t.Context(), seedStack, s[0], s[1]); err != nil {
			t.Fatalf("seed stack %s: %v", s[1], err)
		}
	}
}

// isolatedURL creates a fresh schema and returns base with search_path set to it.
func isolatedURL(tb testing.TB, base string) string {
	tb.Helper()

	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		tb.Fatalf("random schema name: %v", err)
	}

	name := "test_" + hex.EncodeToString(suffix)

	conn, err := pgx.Connect(tb.Context(), base)
	if err != nil {
		tb.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(tb.Context()); closeErr != nil {
			tb.Errorf("close: %v", closeErr)
		}
	}()

	if _, execErr := conn.Exec(tb.Context(), "CREATE SCHEMA "+pgx.Identifier{name}.Sanitize()); execErr != nil {
		tb.Fatalf("create schema: %v", execErr)
	}

	u, parseErr := url.Parse(base)
	if parseErr != nil {
		tb.Fatalf("parse %s: %v", envURL, parseErr)
	}

	q := u.Query()
	q.Set("search_path", name)
	u.RawQuery = q.Encode()

	return u.String()
}

// TestOpenRefusesBinaryBelowMinimum proves that a schema whose minimum
// binary version is above the version of the binary stops open, names both
// versions and leaves the database unchanged.
func TestOpenRefusesBinaryBelowMinimum(t *testing.T) {
	t.Parallel()

	migrated, wantVersions, wantTables := newerSchemaDatabase(t, "v0.3.0")

	_, err := postgres.Open(t.Context(), migrated, postgres.WithBinaryVersion("v0.2.0"))
	if !errors.Is(err, schema.ErrBinaryBelowMinimum) {
		t.Fatalf("open below the minimum: err = %v, want ErrBinaryBelowMinimum", err)
	}

	if !strings.Contains(err.Error(), "v0.2.0") || !strings.Contains(err.Error(), "v0.3.0") {
		t.Fatalf("open below the minimum: err = %v, want it to name v0.2.0 and v0.3.0", err)
	}

	afterVersions, afterTables := newerSchemaSnapshot(t, migrated)

	if !slices.Equal(afterVersions, wantVersions) {
		t.Fatalf("versions after refusal = %v, want %v", afterVersions, wantVersions)
	}

	if !slices.Equal(afterTables, wantTables) {
		t.Fatalf("tables after refusal = %v, want %v", afterTables, wantTables)
	}
}

// TestOpenStartsOnNewerSchemaAtMinimum proves that a binary at or above the
// minimum opens a schema that records a migration it does not embed, and
// applies nothing.
func TestOpenStartsOnNewerSchemaAtMinimum(t *testing.T) {
	t.Parallel()

	for _, binary := range []string{"v0.3.0", "dev"} {
		t.Run(binary, func(t *testing.T) {
			t.Parallel()
			openNewerSchemaAt(t, binary)
		})
	}
}

// openNewerSchemaAt opens a schema with a future migration that sets minimum
// v0.3.0 as a binary of version binary. The open succeeds, applies nothing
// and reads the minimum back.
func openNewerSchemaAt(t *testing.T, binary string) {
	t.Helper()

	migrated, wantVersions, wantTables := newerSchemaDatabase(t, "v0.3.0")

	s, err := postgres.Open(t.Context(), migrated, postgres.WithBinaryVersion(binary))
	if err != nil {
		t.Fatalf("open at the minimum: %v", err)
	}

	applied, err := s.AppliedMigrations(t.Context())

	s.Close()

	if err != nil {
		t.Fatalf("applied migrations: %v", err)
	}

	if last := applied[len(applied)-1]; last.MinBinaryVersion != "v0.3.0" {
		t.Fatalf("newest applied migration %+v, want minimum v0.3.0", last)
	}

	afterVersions, afterTables := newerSchemaSnapshot(t, migrated)

	if !slices.Equal(afterVersions, wantVersions) {
		t.Fatalf("versions after open = %v, want %v", afterVersions, wantVersions)
	}

	if !slices.Equal(afterTables, wantTables) {
		t.Fatalf("tables after open = %v, want %v", afterTables, wantTables)
	}
}

// newerSchemaDatabase migrates an isolated schema, then records one future
// migration that sets minimum. It returns the URL with the recorded versions
// and the tables.
func newerSchemaDatabase(t *testing.T, minimum string) (string, []int64, []string) {
	t.Helper()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	migrated := isolatedURL(t, base)

	s, err := postgres.Open(t.Context(), migrated)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	s.Close()

	beforeVersions, _ := newerSchemaSnapshot(t, migrated)
	if len(beforeVersions) == 0 {
		t.Fatal("schema versions are empty, want the embedded migrations")
	}

	extra := beforeVersions[len(beforeVersions)-1] + 1

	insertFutureVersion(t, migrated, extra, minimum)

	wantVersions := append(slices.Clone(beforeVersions), extra)
	gotVersions, gotTables := newerSchemaSnapshot(t, migrated)

	if !slices.Equal(gotVersions, wantVersions) {
		t.Fatalf("seeded versions = %v, want %v", gotVersions, wantVersions)
	}

	return migrated, wantVersions, gotTables
}

// newerSchemaSnapshot reads the recorded versions and the table names of the
// database at migrated.
func newerSchemaSnapshot(t *testing.T, migrated string) ([]int64, []string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	return readSchemaVersions(t, conn), readSchemaTables(t, conn)
}

// readSchemaVersions reads the recorded versions, ordered.
func readSchemaVersions(t *testing.T, conn *pgx.Conn) []int64 {
	t.Helper()

	rows, queryErr := conn.Query(t.Context(), `SELECT version FROM schema_migrations ORDER BY version`)
	if queryErr != nil {
		t.Fatalf("read schema versions: %v", queryErr)
	}

	defer rows.Close()

	var versions []int64

	for rows.Next() {
		var version int64
		if scanErr := rows.Scan(&version); scanErr != nil {
			rows.Close()
			t.Fatalf("scan schema version: %v", scanErr)
		}

		versions = append(versions, version)
	}

	rows.Close()

	if iterErr := rows.Err(); iterErr != nil {
		t.Fatalf("read schema versions: %v", iterErr)
	}

	return versions
}

// readSchemaTables reads the table names, ordered.
func readSchemaTables(t *testing.T, conn *pgx.Conn) []string {
	t.Helper()

	rows, queryErr := conn.Query(
		t.Context(),
		`SELECT tablename FROM pg_tables WHERE schemaname = current_schema() ORDER BY tablename`,
	)
	if queryErr != nil {
		t.Fatalf("read tables: %v", queryErr)
	}

	defer rows.Close()

	var tables []string

	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			rows.Close()
			t.Fatalf("scan table: %v", scanErr)
		}

		tables = append(tables, name)
	}

	rows.Close()

	if iterErr := rows.Err(); iterErr != nil {
		t.Fatalf("read tables: %v", iterErr)
	}

	return tables
}

// insertFutureVersion records one version newer than the binary embeds.
func insertFutureVersion(t *testing.T, migrated string, version int64, minimum string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	name := fmt.Sprintf("%04d_future.sql", version)

	const record = `INSERT INTO schema_migrations (version, name, min_binary_version) VALUES ($1, $2, $3)`

	if _, execErr := conn.Exec(t.Context(), record, version, name, minimum); execErr != nil {
		t.Fatalf("record future version: %v", execErr)
	}
}

// TestMigration0011FillsRestoredFrom checks that migration 0011 keeps the
// finalized versions of a database at 0010: every existing row reads back
// with an empty restored_from, and the list stays newest first with the
// right total.
func TestMigration0011FillsRestoredFrom(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	migrated := isolatedURL(t, base)
	seedAt0010WithStateVersions(t, migrated)

	s, err := postgres.Open(t.Context(), migrated)
	if err != nil {
		t.Fatalf("open migrated store: %v", err)
	}

	t.Cleanup(s.Close)

	checkMigratedStateVersions(t, s)
}

// checkMigratedStateVersions checks the migrated versions are listed newest
// first with an empty restored_from and the right total.
func checkMigratedStateVersions(t *testing.T, s *postgres.Store) {
	t.Helper()

	ctx := t.Context()
	wsID := workspace.ID("ws-aaaaaaaaaaaaaaaaaaaa")

	list, total, err := s.ListStateVersions(ctx, wsID, workspace.Page{Limit: 10, Offset: 0})
	if err != nil {
		t.Fatalf("list state versions: %v", err)
	}

	if total != 2 {
		t.Fatalf("migrated total = %d, want 2", total)
	}

	if len(list) != 2 {
		t.Fatalf("migrated state versions = %d, want 2", len(list))
	}

	if list[0].ID.String() != "sv-bbbbbbbbbbbbbbbbbbbb" ||
		list[1].ID.String() != "sv-aaaaaaaaaaaaaaaaaaaa" {
		t.Fatalf("migrated order = [%s %s], want newest first", list[0].ID, list[1].ID)
	}

	for i := range list {
		if list[i].RestoredFrom != "" {
			t.Fatalf("migrated state version %s restored_from = %q, want empty", list[i].ID, list[i].RestoredFrom)
		}
	}

	for _, id := range []workspace.StateVersionID{
		"sv-aaaaaaaaaaaaaaaaaaaa",
		"sv-bbbbbbbbbbbbbbbbbbbb",
		"sv-cccccccccccccccccccc",
	} {
		got, err := s.GetStateVersion(ctx, id)
		if err != nil {
			t.Fatalf("get state version %s: %v", id, err)
		}

		if got.RestoredFrom != "" {
			t.Fatalf("migrated state version %s restored_from = %q, want empty", id, got.RestoredFrom)
		}
	}
}

// seedAt0010WithStateVersions creates a database at migration 0010 with one
// workspace, two finalized state versions and one pending version.
func seedAt0010WithStateVersions(t *testing.T, migrated string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), migrated)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	applyMigrations0010(t, conn)
	recordMigrations0010(t, conn)
	seed0010Namespace(t, conn)
	seed0010Workspace(t, conn)
	seed0010StateVersions(t, conn)
}

// seed0010Workspace seeds one workspace that points at its newest version.
func seed0010Workspace(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO workspaces (id, namespace, name, revision, lock_id, lock_holder,
		lock_reason, locked_at_ms, current_state_version_id, created_at_ms, updated_at_ms)
		VALUES ('ws-aaaaaaaaaaaaaaaaaaaa', 'acme', 'network', 1, '', '', '', 0,
		'sv-bbbbbbbbbbbbbbbbbbbb', 1000, 3000)`

	if _, err := conn.Exec(t.Context(), seed); err != nil {
		t.Fatalf("seed workspace: %v", err)
	}
}

// seed0010StateVersions seeds two finalized versions and one pending version
// with the 0010 columns: no restored_from yet.
func seed0010StateVersions(t *testing.T, conn *pgx.Conn) {
	t.Helper()

	const seed = `INSERT INTO state_versions (id, workspace_id, status, serial, lineage, md5, size,
		content_key, outputs, force, upload_digest, upload_expires_at_ms, holder, lock_id,
		created_at_ms, finalized_at_ms)
		VALUES
		('sv-aaaaaaaaaaaaaaaaaaaa', 'ws-aaaaaaaaaaaaaaaaaaaa', 'finalized', 1, 'lineage-1',
		'\x00000000000000000000000000000001', 11, 'content-old', NULL, FALSE, ''::bytea, 5000,
		'seed', 'lock-1', 1000, 2000),
		('sv-bbbbbbbbbbbbbbbbbbbb', 'ws-aaaaaaaaaaaaaaaaaaaa', 'finalized', 2, 'lineage-1',
		'\x00000000000000000000000000000002', 22, 'content-new', NULL, FALSE, ''::bytea, 5000,
		'seed', 'lock-1', 2000, 3000),
		('sv-cccccccccccccccccccc', 'ws-aaaaaaaaaaaaaaaaaaaa', 'pending', 3, 'lineage-1',
		'\x00000000000000000000000000000003', 0, '', NULL, FALSE, ''::bytea, 5000,
		'seed', 'lock-1', 2500, 0)`

	if _, err := conn.Exec(t.Context(), seed); err != nil {
		t.Fatalf("seed state versions: %v", err)
	}
}

// TestBackupCopyColumnsMatchSchema compares the COPY column list of every
// table of backup.Tables with its live schema columns: a migration that
// adds a column fails here until its author extends the COPY list.
func TestBackupCopyColumnsMatchSchema(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL backup coverage test", envURL)
	}

	schemaURL := isolatedURL(t, base)

	s, err := postgres.Open(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	conn, err := pgx.Connect(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	listed := postgres.CopyColumnsForTest()

	for _, table := range backup.Tables {
		columns, ok := listed[table]
		if !ok {
			t.Fatalf("backup table %q misses its COPY column list", table)
		}

		live := readTableColumns(t, conn, table)

		if checkErr := postgres.CheckCopyColumnsForTest(table, columns, live); checkErr != nil {
			t.Fatal(checkErr)
		}
	}
}

// readTableColumns returns the column names of table in the current schema,
// in ordinal order.
func readTableColumns(t *testing.T, conn *pgx.Conn, table string) []string {
	t.Helper()

	const query = `SELECT column_name FROM information_schema.columns` +
		` WHERE table_schema = current_schema() AND table_name = $1 ORDER BY ordinal_position`

	rows, err := conn.Query(t.Context(), query, table)
	if err != nil {
		t.Fatalf("read columns of table %q: %v", table, err)
	}

	defer rows.Close()

	var columns []string

	for rows.Next() {
		var column string

		if scanErr := rows.Scan(&column); scanErr != nil {
			rows.Close()
			t.Fatalf("scan column of table %q: %v", table, scanErr)
		}

		columns = append(columns, column)
	}

	rows.Close()

	if iterErr := rows.Err(); iterErr != nil {
		t.Fatalf("read columns of table %q: %v", table, iterErr)
	}

	if len(columns) == 0 {
		t.Fatalf("table %q holds no columns, want the migrated schema", table)
	}

	return columns
}

// TestBackupCopyColumnMismatchNamesTableAndColumn proves the coverage check
// names the table and the column, for a missing column and an unknown one.
func TestBackupCopyColumnMismatchNamesTableAndColumn(t *testing.T) {
	t.Parallel()

	missing := postgres.CheckCopyColumnsForTest("updates", []string{"id"}, []string{"id", "created_by"})
	if missing == nil || !strings.Contains(missing.Error(), `"updates"`) ||
		!strings.Contains(missing.Error(), `"created_by"`) {
		t.Fatalf("missing column error = %v, want the table %q and the column %q", missing, "updates", "created_by")
	}

	unknown := postgres.CheckCopyColumnsForTest("checkpoints", []string{"id", "nope"}, []string{"id"})
	if unknown == nil || !strings.Contains(unknown.Error(), `"checkpoints"`) ||
		!strings.Contains(unknown.Error(), `"nope"`) {
		t.Fatalf("unknown column error = %v, want the table %q and the column %q", unknown, "checkpoints", "nope")
	}
}

// BenchmarkPrincipalLists measures each principal and group list with its
// total, at the scale of principaltest.Bench. It needs the PostgreSQL URL.
func BenchmarkPrincipalLists(b *testing.B) {
	base := os.Getenv(envURL)
	if base == "" {
		b.Skipf("set %s to run the PostgreSQL benchmarks", envURL)
	}

	principaltest.Bench(b, open(b, base))
}

// BenchmarkCallerGrants measures the first page of the grants of a caller
// at the scale of accesstest.BenchCallerGrants.
func BenchmarkCallerGrants(b *testing.B) {
	base := os.Getenv(envURL)
	if base == "" {
		b.Skipf("set %s to run the PostgreSQL benchmarks", envURL)
	}

	accesstest.BenchCallerGrants(b, open(b, base))
}

// BenchmarkAuthorizer measures one decision and one list-objects at the
// scale of accesstest.Bench.
func BenchmarkAuthorizer(b *testing.B) {
	base := os.Getenv(envURL)
	if base == "" {
		b.Skipf("set %s to run the PostgreSQL benchmarks", envURL)
	}

	accesstest.Bench(b, open(b, base))
}
