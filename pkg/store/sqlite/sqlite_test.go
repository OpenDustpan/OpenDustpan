package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/access/accesstest"
	"opendustpan.org/opendustpan/pkg/accesstoken/accesstokentest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/audit/audittest"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/principal/principaltest"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
	"opendustpan.org/opendustpan/pkg/session/sessiontest"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
	"opendustpan.org/opendustpan/pkg/tempadmin/tempadmintest"
)

func TestConformance(t *testing.T) {
	t.Parallel()

	storetest.Run(t, func(t *testing.T) storetest.Store {
		t.Helper()

		return open(t)
	})
}

// TestSharedConformance runs the two-handle suite. One SQLite file allows
// one store, so the suite gets that store twice: one database with two
// concurrent callers.
func TestSharedConformance(t *testing.T) {
	t.Parallel()

	storetest.RunShared(t, func(t *testing.T) (storetest.Store, storetest.Store) {
		t.Helper()

		path := filepath.Join(t.TempDir(), "dustpan.db")

		copyTemplate(t, path)

		s := openPath(t, path)

		return s, s
	})
}

// TestAccessConformance runs the role and grant store suite.
func TestAccessConformance(t *testing.T) {
	t.Parallel()

	accesstest.Run(t, func(t *testing.T) access.Store {
		t.Helper()

		return open(t)
	})
}

// TestAccessConcurrency runs the cases of a grant or role check that races
// a write on the same database handle.
func TestAccessConcurrency(t *testing.T) {
	t.Parallel()

	accesstest.RunConcurrent(t, func(t *testing.T) (access.Store, access.Store) {
		t.Helper()

		path := filepath.Join(t.TempDir(), "dustpan.db")
		copyTemplate(t, path)

		s := openPath(t, path)

		return s, s
	})
}

// TestDelegation runs the suite of the tokens of service principals.
func TestDelegation(t *testing.T) {
	t.Parallel()

	storetest.RunDelegation(t, func(t *testing.T) storetest.DelegationStore {
		t.Helper()

		return open(t)
	})
}

// TestAccessTokenConformance runs the access token store suite.
func TestAccessTokenConformance(t *testing.T) {
	t.Parallel()

	accesstokentest.Run(t, func(t *testing.T) accesstokentest.Store {
		t.Helper()

		return open(t)
	})
}

// TestSessionConformance runs the session store suite.
func TestSessionConformance(t *testing.T) {
	t.Parallel()

	sessiontest.Run(t, func(t *testing.T) sessiontest.Store {
		t.Helper()

		return open(t)
	})
}

// TestTempAdminConformance runs the temporary administrator store suite.
func TestTempAdminConformance(t *testing.T) {
	t.Parallel()

	tempadmintest.Run(t, func(t *testing.T) tempadmintest.Store {
		t.Helper()

		return open(t)
	})
}

// TestSignInFailuresConformance runs the shared failure-budget suite: one
// SQLite file allows one store, so the suite gets that store for every
// handle. The store reads its window from its own clock, so it ignores skew.
func TestSignInFailuresConformance(t *testing.T) {
	t.Parallel()

	storetest.RunSignInFailures(t, func(t *testing.T) storetest.SignInFailureHarness {
		t.Helper()

		s := open(t)

		return storetest.SignInFailureHarness{
			Open: func(*testing.T, time.Duration) authgate.FailureCounter { return s },
		}
	})
}

// TestAuditCheckConformance runs the shared suite of the audit chain check
// state: one SQLite file allows one store, so the suite gets that store for
// every handle.
func TestAuditCheckConformance(t *testing.T) {
	t.Parallel()

	storetest.RunAuditCheck(t, func(t *testing.T) storetest.AuditCheckHarness {
		t.Helper()

		s := open(t)

		return storetest.AuditCheckHarness{
			Open: func(*testing.T) audit.CheckStore { return s },
		}
	})
}

// TestSessionFreshConformance runs the session cases that change a policy
// on one handle and use a session on the other. One SQLite file allows one
// store, so the suite gets that store twice.
func TestSessionFreshConformance(t *testing.T) {
	t.Parallel()

	sessiontest.RunFresh(t, func(t *testing.T) (sessiontest.Store, sessiontest.Store) {
		t.Helper()

		s := open(t)

		return s, s
	})
}

// TestNamespacePrincipalConformance runs the suite of the principals of a
// namespace.
func TestNamespacePrincipalConformance(t *testing.T) {
	t.Parallel()

	principaltest.RunNamespace(t, func(t *testing.T) principaltest.NamespaceStore {
		t.Helper()

		return open(t)
	})
}

// TestPrincipalConformance runs the principal and group store suite.
func TestPrincipalConformance(t *testing.T) {
	t.Parallel()

	principaltest.Run(t, func(t *testing.T) principal.Store {
		t.Helper()

		return open(t)
	})
}

// TestAuditFeedConformance runs the audit.Feed suite: entries after a
// sequence and the fenced sink cursors.
func TestAuditFeedConformance(t *testing.T) {
	t.Parallel()

	audittest.RunFeed(t, func(t *testing.T) audittest.FeedStore {
		t.Helper()

		return open(t)
	})
}

// TestShipperRefusesLeaselessStore proves that a shipper with a store-backed
// feed and no lease store is refused at construction.
func TestShipperRefusesLeaselessStore(t *testing.T) {
	t.Parallel()

	audittest.CheckShipperRefusesLeaselessStore(t, open(t))
}

// TestAuditConformance runs the audit store suite: append, list order,
// paging, fencing, isolation, concurrent appends and the chain check. The
// tamper hooks change or remove a row through SQL: the port has no update
// or delete path, so only SQL plants a changed field or a deleted entry.
func TestAuditConformance(t *testing.T) {
	t.Parallel()

	audittest.Run(t, func(t *testing.T) audittest.TestStore {
		t.Helper()

		path := filepath.Join(t.TempDir(), "dustpan.db")

		copyTemplate(t, path)

		s, err := sqlite.Open(t.Context(), path)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Errorf("close store: %v", err)
			}
		})

		return audittest.TestStore{
			Store: s,
			TamperEntry: func(ctx context.Context, ns namespace.Name, seq int64, field, value string) error {
				return tamperAuditEntry(ctx, path, ns, seq, auditFieldChange{field: field, value: value})
			},
			DeleteEntry: func(ctx context.Context, ns namespace.Name, seq int64) error {
				return deleteAuditEntry(ctx, path, ns, seq)
			},
		}
	})
}

// TestAuditEntryReaderConformance runs the audit.EntryReader cases: one
// entry at a sequence and the last entry of a scope.
func TestAuditEntryReaderConformance(t *testing.T) {
	t.Parallel()

	audittest.RunEntryReader(t, func(t *testing.T) audittest.EntryStore {
		t.Helper()

		s, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Errorf("close store: %v", err)
			}
		})

		return s
	})
}

// TestAuditCheckpointEpochChange runs the case of a checkpoint write after
// an epoch change on SQLite entries.
func TestAuditCheckpointEpochChange(t *testing.T) {
	t.Parallel()

	s, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	blobs, err := blobfs.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	audittest.CheckEpochChangeStopsWrite(t, s, blobs)
}

// chainApprovalStore wraps a Store with its path, so the approval cases can
// break audit appends with direct SQL. It embeds the store, so the suite
// also reads its chain state, list and durable operations.
type chainApprovalStore struct {
	*sqlite.Store

	path string
}

var (
	_ audittest.ChainApprover   = (*chainApprovalStore)(nil)
	_ audittest.ChainBreaker    = (*chainApprovalStore)(nil)
	_ audittest.ChainOperations = (*chainApprovalStore)(nil)
)

// BreakAuditAppends implements audittest.ChainBreaker.
func (s *chainApprovalStore) BreakAuditAppends(ctx context.Context) error {
	return breakAuditAppends(ctx, s.path)
}

// TestAuditChainConformance runs the chain switch cases: entries without a
// hash, runs of chained entries, and the recorded chain state.
func TestAuditChainConformance(t *testing.T) {
	t.Parallel()

	audittest.RunChain(t, func(t *testing.T) audittest.ChainTestStore {
		t.Helper()

		path := filepath.Join(t.TempDir(), "dustpan.db")

		s, err := sqlite.Open(t.Context(), path)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Errorf("close store: %v", err)
			}
		})

		return audittest.ChainTestStore{
			Store: &chainApprovalStore{Store: s, path: path},
			TamperEntry: func(t *testing.T, ns namespace.Name, seq int64, field, value string) {
				t.Helper()

				change := auditFieldChange{field: field, value: value}
				if err := tamperAuditEntry(t.Context(), path, ns, seq, change); err != nil {
					t.Fatalf("change entry %d: %v", seq, err)
				}
			},
			// One SQLite file allows one store, so there is no second
			// handle to reopen.
			Reopen: nil,
			DeleteChainState: func(t *testing.T) {
				t.Helper()

				execRawSQL(t, path, `DELETE FROM audit_chain_state`)
			},
		}
	})
}

// execRawSQL runs one statement on a raw handle on the database at path.
func execRawSQL(t *testing.T, path, statement string) {
	t.Helper()

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	if _, execErr := conn.ExecContext(t.Context(), statement); execErr != nil {
		t.Fatalf("%s: %v", statement, execErr)
	}
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
		"value_path",
		"value_count",
		"object_id",
		"idp_subject",
		"token_id",
		"version",
		"source_ip",
		"user_agent",
		"channel",
		"request_id",
		"reason":
		return field, true
	default:
		return "", false
	}
}

// auditFieldChange names one column change of the tamper hook.
type auditFieldChange struct {
	field string
	value string
}

// tamperAuditEntry changes one column of one audit entry through a second
// database handle: the port has no update path.
func tamperAuditEntry(
	ctx context.Context, path string, ns namespace.Name, seq int64, change auditFieldChange,
) error {
	column, ok := tamperAuditColumn(change.field)
	if !ok {
		return fmt.Errorf("%w: %q", errUnknownAuditField, change.field)
	}

	conn, err := openRawDB(path)
	if err != nil {
		return fmt.Errorf("open sqlite database: %w", err)
	}

	defer func() {
		_ = conn.Close() //nolint:errcheck // Test helper: the Exec result below carries the verdict.
	}()

	_, execErr := conn.ExecContext(
		ctx,
		`UPDATE audit_entries SET `+column+` = ? WHERE namespace = ? AND sequence = ?`, //nolint:gosec // Column comes from the tamperAuditColumn allowlist, never from the caller.
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
func deleteAuditEntry(ctx context.Context, path string, ns namespace.Name, seq int64) error {
	conn, err := openRawDB(path)
	if err != nil {
		return fmt.Errorf("open sqlite database: %w", err)
	}

	defer func() {
		_ = conn.Close() //nolint:errcheck // Test helper: the Exec result below carries the verdict.
	}()

	_, execErr := conn.ExecContext(
		ctx, `DELETE FROM audit_entries WHERE namespace = ? AND sequence = ?`, ns.String(), seq,
	)
	if execErr != nil {
		return fmt.Errorf("delete audit entry: %w", execErr)
	}

	return nil
}

func TestSchemaConformance(t *testing.T) {
	t.Parallel()

	storetest.RunSchema(t, func(t *testing.T) storetest.SchemaStore {
		t.Helper()

		return open(t)
	})
}

// TestPingClosed checks Ping answers on an open store and returns exactly the
// sentinel text, with no driver text, after Close.
func TestPingClosed(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	if err = s.Ping(t.Context()); err != nil {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}

		t.Fatalf("ping open store: %v", err)
	}

	if err = s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	if err = s.Ping(t.Context()); err == nil || !errors.Is(err, namespace.ErrUnavailable) {
		t.Fatalf("ping closed store: err = %v, want ErrUnavailable", err)
	}

	if want := "ping sqlite database: database unavailable"; err.Error() != want {
		t.Fatalf("ping closed store error = %q, want %q", err, want)
	}
}

// TestUnknownStoredStateIsUnavailable plants a stored installation state
// outside the known set and proves GetState answers ErrUnavailable without
// the value, never ErrInvalidState: an unknown stored value is a server
// fault, not a bad request.
func TestUnknownStoredStateIsUnavailable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	s, openErr := sqlite.Open(t.Context(), path)
	if openErr != nil {
		t.Fatalf("open store: %v", openErr)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	conn, connErr := openRawDB(path)
	if connErr != nil {
		t.Fatalf("open raw database: %v", connErr)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	ctx := t.Context()

	if _, execErr := conn.ExecContext(ctx, `PRAGMA ignore_check_constraints = ON`); execErr != nil {
		t.Fatalf("ignore check constraints: %v", execErr)
	}

	if _, execErr := conn.ExecContext(ctx, `UPDATE installation SET state = 'offline'`); execErr != nil {
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

// TestMissingStateRowIsUnavailable deletes the seeded installation row and
// proves that GetState and SetState answer ErrUnavailable, and that
// SetState does not create the row again: the migration seeds the row, so a
// missing row is a server fault.
func TestMissingStateRowIsUnavailable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	s, openErr := sqlite.Open(t.Context(), path)
	if openErr != nil {
		t.Fatalf("open store: %v", openErr)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	conn, connErr := openRawDB(path)
	if connErr != nil {
		t.Fatalf("open raw database: %v", connErr)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	ctx := t.Context()

	if _, execErr := conn.ExecContext(ctx, `DELETE FROM installation`); execErr != nil {
		t.Fatalf("delete installation row: %v", execErr)
	}

	if _, err := s.GetState(ctx); !errors.Is(err, maintenance.ErrUnavailable) {
		t.Fatalf("get installation state without a row: err = %v, want ErrUnavailable", err)
	}

	if err := s.SetState(ctx, maintenance.StateMaintenance); !errors.Is(err, maintenance.ErrUnavailable) {
		t.Fatalf("set installation state without a row: err = %v, want ErrUnavailable", err)
	}

	var rows int64
	if scanErr := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM installation`).Scan(&rows); scanErr != nil {
		t.Fatalf("count installation rows: %v", scanErr)
	}

	if rows != 0 {
		t.Fatalf("installation rows after the writes = %d, want 0", rows)
	}
}

func TestSecretsConformance(t *testing.T) {
	t.Parallel()

	secrettest.Run(t, func(t *testing.T) secrettest.Store {
		t.Helper()

		return open(t)
	})
}

func TestItemKeysConformance(t *testing.T) {
	t.Parallel()

	secrettest.RunItemKeys(t, func(t *testing.T) secrettest.ItemKeyStore {
		t.Helper()

		return open(t)
	})
}

func TestItemSealerConformance(t *testing.T) {
	t.Parallel()

	secrettest.RunItemSealer(t, func(t *testing.T) *secrettest.SealerRig {
		t.Helper()

		blobs, err := blobfs.Open(t.TempDir())
		if err != nil {
			t.Fatalf("open blob store: %v", err)
		}

		t.Cleanup(func() { _ = blobs.Close() }) //nolint:errcheck // Test cleanup.

		return secrettest.NewSealerRig(t, open(t), blobs)
	})
}

func TestInstallationSecretsConformance(t *testing.T) {
	t.Parallel()

	secrettest.RunInstallation(t, func(t *testing.T) secrettest.InstallationStore {
		t.Helper()

		return open(t)
	})
}

func TestUpdatesConformance(t *testing.T) {
	t.Parallel()

	storetest.RunUpdates(t, func(t *testing.T) storetest.UpdatesStore {
		t.Helper()

		return openSQLUpdates(t)
	})

	// History fields of ADR 28 decision 4: creator, restore source and
	// checkpoint size.
	storetest.RunUpdateHistory(t, func(t *testing.T) storetest.HistoryStore {
		t.Helper()

		return open(t)
	})
}

func TestStateAtConformance(t *testing.T) {
	t.Parallel()

	storetest.RunStateAt(t, func(t *testing.T) storetest.StateAtStore {
		t.Helper()

		return open(t)
	})
}

func TestWorkspaceStateAtConformance(t *testing.T) {
	t.Parallel()

	storetest.RunWorkspaceStateAt(t, func(t *testing.T) storetest.WorkspaceStateAtStore {
		t.Helper()

		return openAuditedWorkspaces(t)
	})
}

func TestWorkspaceConformance(t *testing.T) {
	t.Parallel()

	storetest.RunWorkspace(t, func(t *testing.T) storetest.WorkspaceStore {
		t.Helper()

		return openAuditedWorkspaces(t)
	})
}

func TestIPAllowlistConformance(t *testing.T) {
	t.Parallel()

	storetest.RunIPAllowlist(t, func(t *testing.T) storetest.IPAllowlistStore {
		t.Helper()

		return open(t)
	})
}

// TestNamespacePagingConformance runs the namespace paging suite: paged,
// filtered reads of namespaces in name order with their counts.
func TestNamespacePagingConformance(t *testing.T) {
	t.Parallel()

	storetest.RunNamespacePaging(t, func(t *testing.T) storetest.PagingStore {
		t.Helper()

		return open(t)
	})
}

// TestUnitQueryConformance runs the unit page suite: one page of the units
// of a namespace in the order of the unit ID, with the filters in the query.
func TestUnitQueryConformance(t *testing.T) {
	t.Parallel()

	storetest.RunUnitQuery(t, func(t *testing.T) storetest.UnitQueryStore {
		t.Helper()

		return open(t)
	})
}

func TestBackupConformance(t *testing.T) {
	t.Parallel()

	storetest.RunBackup(t, func(t *testing.T) storetest.BackupStore {
		t.Helper()

		return openBackup(t)
	})
}

func TestBlobDeleteDuringBackup(t *testing.T) {
	t.Parallel()

	storetest.RunBlobDelete(t, func(t *testing.T) storetest.BlobDeleteStore {
		t.Helper()

		st, ok := openBackup(t).(storetest.BlobDeleteStore)
		if !ok {
			t.Fatal("the backup store is not a storetest.BlobDeleteStore")
		}

		return st
	})
}

func TestPortableExportConformance(t *testing.T) {
	t.Parallel()

	storetest.RunPortable(t, func(t *testing.T) storetest.PortableStore {
		t.Helper()

		ps, ok := openBackup(t).(storetest.PortableStore)
		if !ok {
			t.Fatal("the SQLite store does not export the portable format")
		}

		return ps
	})
}

func TestDevDataConformance(t *testing.T) {
	t.Parallel()

	storetest.RunDevData(t, func(t *testing.T, kind installation.Kind) storetest.DevDataStore {
		t.Helper()

		store, ok := openBackup(t).(storetest.DevDataStore)
		if !ok {
			t.Fatal("the SQLite store does not hold the dev data ports")
		}

		if kind != "" {
			if _, err := store.ClaimKind(t.Context(), kind); err != nil {
				t.Fatalf("claim the installation kind: %v", err)
			}
		}

		return store
	})
}

func TestRestoreConformance(t *testing.T) {
	t.Parallel()

	storetest.RunRestore(t, func(t *testing.T) storetest.RestoreStore {
		t.Helper()

		restorable, ok := openBackup(t).(storetest.RestoreStore)
		if !ok {
			t.Fatal("backup store keeps no restore ports")
		}

		return restorable
	})
}

// backupStore wraps a Store with its path for the close-and-reopen case of
// the backup suite: Close releases the handle and Reopen returns a new handle
// to the same database file.
type backupStore struct {
	*sqlite.Store

	t      *testing.T
	mu     sync.Mutex
	path   string
	closed bool
}

var (
	_ storetest.RestoreAreaDropper = (*backupStore)(nil)
	_ storetest.Reopener           = (*backupStore)(nil)
	_ storetest.StagingWitness     = (*backupStore)(nil)
	// Defense in depth: a store with no runner port fails to build here, so the runner round trip cannot be skipped.
	_ storetest.RunnerSeeder = (*backupStore)(nil)
)

// openBackup returns a fresh backup-capable store in a temporary directory,
// copied from the migrated template.
func openBackup(t *testing.T) storetest.BackupStore { //nolint:ireturn // Factory signature returns the interface.
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	b := &backupStore{Store: s, t: t, mu: sync.Mutex{}, path: path, closed: false}

	t.Cleanup(func() {
		if err := b.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	return b
}

// DropRestoreArea implements storetest.RestoreAreaDropper: it removes the
// staging file of a, and the journal files next to it, as the cleanup of the
// store does, with no record.
func (s *backupStore) DropRestoreArea(_ context.Context, a backup.StagingArea) error {
	staged := s.path + ".stage-" + a.Restore.String() + "-" + strconv.FormatInt(a.Token, 10)

	for _, name := range []string{staged, staged + "-wal", staged + "-shm", staged + "-journal"} {
		if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove %s: %w", name, err)
		}
	}

	return nil
}

// Close releases the handle. It runs once, so the cleanup and the reopen
// case can both call it.
func (s *backupStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true

	if err := s.Store.Close(); err != nil {
		return fmt.Errorf("close store: %w", err)
	}

	return nil
}

// Reopen returns a new handle to the same database file. It leaves running
// backup rows alone, as Open does: the server marks them failed at start.
func (s *backupStore) Reopen() storetest.BackupStore { //nolint:ireturn // Reopener returns a new handle as the interface.
	reopened, err := sqlite.Open(s.t.Context(), s.path)
	if err != nil {
		s.t.Fatalf("reopen store: %v", err)
	}

	b := &backupStore{Store: reopened, t: s.t, mu: sync.Mutex{}, path: s.path, closed: false}

	s.t.Cleanup(func() {
		if err := b.Close(); err != nil {
			s.t.Errorf("close store: %v", err)
		}
	})

	return b
}

// IdempotencyStore implements storetest.IdempotencyHolder.
func (s *backupStore) IdempotencyStore() idempotency.Store { //nolint:ireturn // The capability returns the port.
	return s.Idempotency(idempotencytest.TestWrapper(s.t, 1))
}

// EraseKindForTest implements storetest.KindEraser: it sets the stored
// installation kind back to NULL through a second handle, behind the port.
func (s *backupStore) EraseKindForTest(ctx context.Context) error {
	raw, err := sql.Open("sqlite", "file:"+s.path+"?_pragma=busy_timeout(10000)")
	if err != nil {
		return fmt.Errorf("open the database file: %w", err)
	}

	defer func() {
		_ = raw.Close() //nolint:errcheck // Test-only handle.
	}()

	if _, err := raw.ExecContext(ctx, `UPDATE installation SET kind = NULL`); err != nil {
		return fmt.Errorf("erase the kind: %w", err)
	}

	return nil
}

// CurrentStagingID implements storetest.StagingWitness: the path of the
// current staging file, or empty when none exists.
func (s *backupStore) CurrentStagingID() string {
	return s.StagingPathForTest()
}

// StagingAlive implements storetest.StagingWitness: whether the staging file
// with id still exists.
func (s *backupStore) StagingAlive(id string) bool {
	if id == "" {
		return false
	}

	_, err := os.Stat(id)

	return err == nil
}

// errUnknownEndedAtTable reports a table that the raw end-time reader does
// not know.
var errUnknownEndedAtTable = errors.New("unknown end-time table")

// StoredEndedAtMs implements storetest.NullEndedWitness: it reads the
// ended_at_ms column of one control row with a raw query.
func (s *backupStore) StoredEndedAtMs(ctx context.Context, table, id string) (int64, bool, error) {
	var query string

	switch table {
	case backup.TableBackups:
		query = `SELECT ended_at_ms FROM backups WHERE id = ?`
	case backup.TableRestores:
		query = `SELECT ended_at_ms FROM restores WHERE id = ?`
	case backup.TableStoreChecks:
		query = `SELECT ended_at_ms FROM store_checks WHERE id = ?`
	default:
		return 0, false, fmt.Errorf("read ended_at_ms: %w: %q", errUnknownEndedAtTable, table)
	}

	conn, err := openRawDB(s.path)
	if err != nil {
		return 0, false, fmt.Errorf("open raw database: %w", err)
	}

	var ms sql.NullInt64

	scanErr := conn.QueryRowContext(ctx, query, id).Scan(&ms)
	closeErr := conn.Close()

	if scanErr != nil {
		return 0, false, errors.Join(fmt.Errorf("read ended_at_ms of %s %s: %w", table, id, scanErr), closeErr)
	}

	if closeErr != nil {
		return 0, false, fmt.Errorf("close raw database: %w", closeErr)
	}

	return ms.Int64, ms.Valid, nil
}

// open returns a fresh store in a temporary directory, copied from the
// migrated template so only the first test of the binary runs every
// migration.
func open(t *testing.T) *sqlite.Store {
	t.Helper()

	return openTemplated(t)
}

// sqlUpdatesStore wraps a Store with its path, so a case can change its rows
// with direct SQL, as the audit case does to break audit appends.
type sqlUpdatesStore struct {
	storetest.UpdatesStore

	path string
}

var (
	_ storetest.AuditBreaker = (*sqlUpdatesStore)(nil)
	_ storetest.KeyEraser    = (*sqlUpdatesStore)(nil)
)

// errNoDatabaseClock reports a wrapped store that does not read the clock of
// the database.
var errNoDatabaseClock = errors.New("wrapped store does not read the clock of the database")

// errNoJournalCount reports a wrapped store without the journaled count.
var errNoJournalCount = errors.New("wrapped store does not count running journaled updates")

// openSQLUpdates returns a fresh store in a temporary directory that a case
// can change through SQL, copied from the migrated template.
func openSQLUpdates(t *testing.T) *sqlUpdatesStore {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	return &sqlUpdatesStore{UpdatesStore: s, path: path}
}

// DatabaseNow forwards to the wrapped store, whose method the embedded
// UpdatesStore interface hides.
func (s *sqlUpdatesStore) DatabaseNow(ctx context.Context) (time.Time, error) {
	clock, ok := s.UpdatesStore.(blob.DatabaseClock)
	if !ok {
		return time.Time{}, fmt.Errorf("database now: %w", errNoDatabaseClock)
	}

	now, err := clock.DatabaseNow(ctx)
	if err != nil {
		return time.Time{}, fmt.Errorf("database now: %w", err)
	}

	return now, nil
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
	return breakAuditAppends(ctx, s.path)
}

// EraseCheckpointKeys implements storetest.KeyEraser.
func (s *sqlUpdatesStore) EraseCheckpointKeys(ctx context.Context) (err error) {
	conn, err := openRawDB(s.path)
	if err != nil {
		return fmt.Errorf("open raw database: %w", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close raw database: %w", closeErr))
		}
	}()

	if _, err = conn.ExecContext(ctx, `UPDATE checkpoints SET blob_key = NULL`); err != nil {
		return fmt.Errorf("erase the checkpoint keys: %w", err)
	}

	return nil
}

// auditedWorkspaces is a store for the workspace suite that can break its
// audit appends. It embeds the store itself, so the suite also reads its
// audit list.
type auditedWorkspaces struct {
	*sqlite.Store

	path string
}

var _ storetest.AuditBreaker = (*auditedWorkspaces)(nil)

// BreakAuditAppends implements storetest.AuditBreaker.
func (s *auditedWorkspaces) BreakAuditAppends(ctx context.Context) error {
	return breakAuditAppends(ctx, s.path)
}

// openAuditedWorkspaces returns a fresh store in a temporary directory whose
// audit appends the suite can break, copied from the migrated template.
func openAuditedWorkspaces(t *testing.T) *auditedWorkspaces {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close store: %v", err)
		}
	})

	return &auditedWorkspaces{Store: s, path: path}
}

// breakAuditAppends creates a trigger in the database at path that aborts
// every insert of an audit entry.
func breakAuditAppends(ctx context.Context, path string) (err error) {
	conn, err := openRawDB(path)
	if err != nil {
		return fmt.Errorf("open raw database: %w", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close raw database: %w", closeErr))
		}
	}()

	_, err = conn.ExecContext(ctx, `CREATE TRIGGER break_audit_appends BEFORE INSERT ON audit_entries
BEGIN SELECT RAISE(ABORT, 'audit append broken by the test'); END`)
	if err != nil {
		return fmt.Errorf("create the trigger: %w", err)
	}

	return nil
}

// TestRenameKeepsTags checks that tags follow the stack ID through a rename:
// the renamed stack keeps its tags and ID, and the old name is gone.
func TestRenameKeepsTags(t *testing.T) {
	t.Parallel()

	s := open(t)
	ctx := t.Context()
	now := time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)

	ns := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           now,
		Name:                "acme",
		PrimaryInstallation: "eu-west",
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := s.CreateNamespace(ctx, ns); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	ref, parseErr := stack.ParseRef("acme", "web", "prod")
	if parseErr != nil {
		t.Fatalf("parse ref: %v", parseErr)
	}

	st := stack.Stack{
		NamespaceID: "",
		CreatedAt:   now,
		UpdatedAt:   now,
		Tags:        map[string]string{"team": "platform"},
		Ref:         ref,
		ID:          stack.ID(ident.NewID("stk-")),
		Version:     0,
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

// TestOpenRefusesBinaryBelowMinimum proves that a database whose schema
// records a minimum binary version above the version of the binary stops
// open, names both versions and leaves the database unchanged.
func TestOpenRefusesBinaryBelowMinimum(t *testing.T) {
	t.Parallel()

	path, wantVersions, wantTables := newerSchemaDatabase(t, "v0.3.0")

	_, err := sqlite.Open(t.Context(), path, sqlite.WithBinaryVersion("v0.2.0"))
	if !errors.Is(err, schema.ErrBinaryBelowMinimum) {
		t.Fatalf("open below the minimum: err = %v, want ErrBinaryBelowMinimum", err)
	}

	if !strings.Contains(err.Error(), "v0.2.0") || !strings.Contains(err.Error(), "v0.3.0") {
		t.Fatalf("open below the minimum: err = %v, want it to name v0.2.0 and v0.3.0", err)
	}

	afterVersions, afterTables := newerSchemaSnapshot(t, path)

	if !slices.Equal(afterVersions, wantVersions) {
		t.Fatalf("versions after refusal = %v, want %v", afterVersions, wantVersions)
	}

	if !slices.Equal(afterTables, wantTables) {
		t.Fatalf("tables after refusal = %v, want %v", afterTables, wantTables)
	}
}

// TestOpenUsesTheHighestMinimum records two future migrations whose
// minimums sort in the other order as text: v0.10.0 is the higher one. A
// binary between them refuses to open.
func TestOpenUsesTheHighestMinimum(t *testing.T) {
	t.Parallel()

	path, versions, _ := newerSchemaDatabase(t, "v0.10.0")
	insertFutureVersion(t, path, versions[len(versions)-1]+1, "v0.3.0")

	if _, err := sqlite.Open(t.Context(), path, sqlite.WithBinaryVersion("v0.5.0")); !errors.Is(
		err, schema.ErrBinaryBelowMinimum,
	) {
		t.Fatalf("open between two minimums: err = %v, want ErrBinaryBelowMinimum", err)
	}

	s, err := sqlite.Open(t.Context(), path, sqlite.WithBinaryVersion("v0.10.0"))
	if err != nil {
		t.Fatalf("open at the highest minimum: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
}

// TestOpenStartsOnNewerSchemaAtMinimum proves that a binary at or above the
// minimum opens a database that records a migration it does not embed, and
// applies nothing.
func TestOpenStartsOnNewerSchemaAtMinimum(t *testing.T) {
	t.Parallel()

	for _, binary := range []string{"v0.3.0", "v0.4.1", "dev"} {
		t.Run(binary, func(t *testing.T) {
			t.Parallel()
			openNewerSchemaAt(t, binary)
		})
	}
}

// openNewerSchemaAt opens a database with a future migration that sets
// minimum v0.3.0 as a binary of version binary. The open succeeds, applies
// nothing and reads the minimum back.
func openNewerSchemaAt(t *testing.T, binary string) {
	t.Helper()

	path, wantVersions, wantTables := newerSchemaDatabase(t, "v0.3.0")

	s, err := sqlite.Open(t.Context(), path, sqlite.WithBinaryVersion(binary))
	if err != nil {
		t.Fatalf("open at the minimum: %v", err)
	}

	applied, err := s.AppliedMigrations(t.Context())
	if closeErr := s.Close(); closeErr != nil {
		t.Fatalf("close store: %v", closeErr)
	}

	if err != nil {
		t.Fatalf("applied migrations: %v", err)
	}

	if len(applied) == 0 {
		t.Fatal("applied migrations are empty, want the recorded migrations")
	}

	if last := applied[len(applied)-1]; last.MinBinaryVersion != "v0.3.0" {
		t.Fatalf("newest applied migration %+v, want minimum v0.3.0", last)
	}

	afterVersions, afterTables := newerSchemaSnapshot(t, path)

	if !slices.Equal(afterVersions, wantVersions) {
		t.Fatalf("versions after open = %v, want %v", afterVersions, wantVersions)
	}

	if !slices.Equal(afterTables, wantTables) {
		t.Fatalf("tables after open = %v, want %v", afterTables, wantTables)
	}
}

// TestOpenBeforeMinimumColumn proves that a database from before the baseline
// is refused with the named error.
func TestOpenBeforeMinimumColumn(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	const create = `CREATE TABLE schema_migrations (version INTEGER NOT NULL PRIMARY KEY, name TEXT NOT NULL) STRICT`
	if _, execErr := conn.ExecContext(t.Context(), create); execErr != nil {
		t.Fatalf("create schema_migrations: %v", execErr)
	}

	const old = 76

	const record = `INSERT INTO schema_migrations (version, name) VALUES (?, ?)`
	if _, execErr := conn.ExecContext(t.Context(), record, old, "0076_sign_in_providers.sql"); execErr != nil {
		t.Fatalf("record migration before the baseline: %v", execErr)
	}

	if closeErr := conn.Close(); closeErr != nil {
		t.Fatalf("close raw database: %v", closeErr)
	}

	if _, err := sqlite.Open(t.Context(), path); !errors.Is(err, schema.ErrSchemaBeforeBaseline) {
		t.Fatalf("open a database from before the baseline: err = %v, want ErrSchemaBeforeBaseline", err)
	}
}

// newerSchemaDatabase migrates a database, then records one future migration
// that sets minimum. It returns the path with the recorded versions and the
// tables.
func newerSchemaDatabase(t *testing.T, minimum string) (string, []int64, []string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")

	copyTemplate(t, path)

	s, err := sqlite.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	if err := s.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}

	beforeVersions, _ := newerSchemaSnapshot(t, path)
	if len(beforeVersions) == 0 {
		t.Fatal("schema versions are empty, want the embedded migrations")
	}

	extra := beforeVersions[len(beforeVersions)-1] + 1

	insertFutureVersion(t, path, extra, minimum)

	wantVersions := append(slices.Clone(beforeVersions), extra)
	gotVersions, gotTables := newerSchemaSnapshot(t, path)

	if !slices.Equal(gotVersions, wantVersions) {
		t.Fatalf("seeded versions = %v, want %v", gotVersions, wantVersions)
	}

	return path, wantVersions, gotTables
}

// newerSchemaSnapshot reads the recorded versions and the table names of the
// database at path.
func newerSchemaSnapshot(t *testing.T, path string) ([]int64, []string) {
	t.Helper()

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	return readSchemaVersions(t, conn), readSchemaTables(t, conn)
}

// readSchemaVersions reads the recorded versions, ordered.
func readSchemaVersions(t *testing.T, conn *sql.DB) []int64 {
	t.Helper()

	rows, queryErr := conn.QueryContext(t.Context(), `SELECT version FROM schema_migrations ORDER BY version`)
	if queryErr != nil {
		t.Fatalf("read schema versions: %v", queryErr)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close schema versions: %v", closeErr)
		}
	}()

	var versions []int64

	for rows.Next() {
		var version int64
		if scanErr := rows.Scan(&version); scanErr != nil {
			t.Fatalf("scan schema version: %v", scanErr)
		}

		versions = append(versions, version)
	}

	if iterErr := rows.Err(); iterErr != nil {
		t.Fatalf("read schema versions: %v", iterErr)
	}

	return versions
}

// readSchemaTables reads the table names, ordered.
func readSchemaTables(t *testing.T, conn *sql.DB) []string {
	t.Helper()

	rows, queryErr := conn.QueryContext(
		t.Context(),
		`SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`,
	)
	if queryErr != nil {
		t.Fatalf("read tables: %v", queryErr)
	}

	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			t.Errorf("close tables: %v", closeErr)
		}
	}()

	var tables []string

	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			t.Fatalf("scan table: %v", scanErr)
		}

		tables = append(tables, name)
	}

	if iterErr := rows.Err(); iterErr != nil {
		t.Fatalf("read tables: %v", iterErr)
	}

	return tables
}

// insertFutureVersion records one version newer than the binary embeds.
func insertFutureVersion(t *testing.T, path string, version int64, minimum string) {
	t.Helper()

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	name := fmt.Sprintf("%04d_future.sql", version)

	const record = `INSERT INTO schema_migrations (version, name, min_binary_version) VALUES (?, ?, ?)`

	if _, execErr := conn.ExecContext(t.Context(), record, version, name, minimum); execErr != nil {
		t.Fatalf("record future version: %v", execErr)
	}
}

// BenchmarkPrincipalLists measures each principal and group list with its
// total, at the scale of principaltest.Bench.
func BenchmarkPrincipalLists(b *testing.B) {
	s, err := sqlite.Open(b.Context(), filepath.Join(b.TempDir(), "dustpan.db"))
	if err != nil {
		b.Fatalf("open store: %v", err)
	}

	b.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			b.Errorf("close store: %v", closeErr)
		}
	})

	principaltest.Bench(b, s)
}

// BenchmarkCallerGrants measures the first page of the grants of a caller
// at the scale of accesstest.BenchCallerGrants.
func BenchmarkCallerGrants(b *testing.B) {
	s, err := sqlite.Open(b.Context(), filepath.Join(b.TempDir(), "dustpan.db"))
	if err != nil {
		b.Fatalf("open store: %v", err)
	}

	b.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			b.Errorf("close store: %v", closeErr)
		}
	})

	accesstest.BenchCallerGrants(b, s)
}

// BenchmarkAuthorizer measures one decision and one list-objects at the
// scale of accesstest.Bench.
func BenchmarkAuthorizer(b *testing.B) {
	s, err := sqlite.Open(b.Context(), filepath.Join(b.TempDir(), "dustpan.db"))
	if err != nil {
		b.Fatalf("open store: %v", err)
	}

	b.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			b.Errorf("close store: %v", closeErr)
		}
	})

	accesstest.Bench(b, s)
}

// BenchmarkProtectedGroups measures the protected flag of 1000 groups at the
// scale of accesstest.Bench: one group at a time, and one page.
func BenchmarkProtectedGroups(b *testing.B) {
	s, err := sqlite.Open(b.Context(), filepath.Join(b.TempDir(), "dustpan.db"))
	if err != nil {
		b.Fatalf("open store: %v", err)
	}

	b.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			b.Errorf("close store: %v", closeErr)
		}
	})

	accesstest.BenchProtectedGroups(b, s)
}

// revocationStore adds to the store the two hooks of the revocation suite: the
// runner pools of the store, and a write to the revocation journal that the
// CHECK constraint of the table does not stop.
type revocationStore struct {
	*sqlite.Store

	path string
}

var _ storetest.RevocationStore = (*revocationStore)(nil)

// RunnerPools implements storetest.RevocationStore.
//
//nolint:ireturn // The method implements storetest.RevocationStore, which returns the port.
func (s *revocationStore) RunnerPools() runner.PoolStore {
	return s.Runners()
}

// RunnerPorts implements storetest.RevocationStore.
func (s *revocationStore) RunnerPorts() storetest.RunnerPorts {
	return storetest.RunnerPorts{Pools: s.Runners(), Members: s.Runners()}
}

// KeepsUnknownKinds implements storetest.RevocationStore. The restore copies
// the live journal into the staged file, whose table holds the CHECK on the
// kind, so a row of an unknown kind fails the copy and the restore.
func (s *revocationStore) KeepsUnknownKinds() bool { return false }

// InsertRawRevocation implements storetest.RevocationStore. It turns off the
// CHECK constraints for its own connection, as a write to the database file
// with another tool could.
func (s *revocationStore) InsertRawRevocation(ctx context.Context, row storetest.RawRevocation) (err error) {
	raw, err := openRawDB(s.path)
	if err != nil {
		return fmt.Errorf("open raw database: %w", err)
	}

	defer func() {
		if closeErr := raw.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close raw database: %w", closeErr))
		}
	}()

	conn, err := raw.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open raw connection: %w", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close raw connection: %w", closeErr))
		}
	}()

	if _, err = conn.ExecContext(ctx, `PRAGMA ignore_check_constraints = ON`); err != nil {
		return fmt.Errorf("turn off the check constraints: %w", err)
	}

	const insert = `INSERT INTO maintenance_revocations (kind, target, end_reason, actor, object, at_ms)
VALUES (?, ?, '', ?, ?, ?)`

	if _, err = conn.ExecContext(ctx, insert, row.Kind, row.Target, row.Actor, row.Object, row.AtMs); err != nil {
		return fmt.Errorf("insert the raw revocation: %w", err)
	}

	return nil
}

// TestRevocationConformance runs the suite of the writes that only reduce
// access: they run in maintenance mode, and a restore keeps them.
func TestRevocationConformance(t *testing.T) {
	t.Parallel()

	storetest.RunRevocations(t, func(t *testing.T) storetest.RevocationStore {
		t.Helper()

		path := filepath.Join(t.TempDir(), "dustpan.db")

		copyTemplate(t, path)

		s, err := sqlite.Open(t.Context(), path)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Errorf("close store: %v", err)
			}
		})

		return &revocationStore{Store: s, path: path}
	})
}

// errNoMarker reports a marker that the live marker suite hook does not know.
var errNoMarker = errors.New("no such marker")

// errMarkerRows reports a marker write that did not change exactly one row.
var errMarkerRows = errors.New("marker write did not change one row")

// liveMarkerStore adds to the store the hook of the live marker suite: a
// write of a marker value that the ports never write.
type liveMarkerStore struct {
	*revocationStore
}

var _ storetest.LiveMarkerStore = (*liveMarkerStore)(nil)

// SetMarker implements storetest.LiveMarkerStore. It writes to the database
// file with another handle, as a restored backup or another tool could.
func (s *liveMarkerStore) SetMarker(ctx context.Context, table, column, id string, value *int64) (err error) {
	statements := map[string]string{
		"principals/deleted_at_ms": `UPDATE principals SET deleted_at_ms = ? WHERE id = ?`,
		"grants/revoked_at_ms":     `UPDATE grants SET revoked_at_ms = ? WHERE id = ?`,
		"roles/deleted_at_ms":      `UPDATE roles SET deleted_at_ms = ? WHERE id = ?`,
		"workspaces/deleted_at_ms": `UPDATE workspaces SET deleted_at_ms = ? WHERE id = ?`,
	}

	statement, ok := statements[table+"/"+column]
	if !ok {
		return fmt.Errorf("%s.%s: %w", table, column, errNoMarker)
	}

	raw, err := openRawDB(s.path)
	if err != nil {
		return fmt.Errorf("open raw database: %w", err)
	}

	defer func() {
		if closeErr := raw.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close raw database: %w", closeErr))
		}
	}()

	res, err := raw.ExecContext(ctx, statement, value, id)
	if err != nil {
		return fmt.Errorf("set the marker: %w", err)
	}

	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("count the changed rows: %w", err)
	}

	if n != 1 {
		return fmt.Errorf("set the marker: %d rows changed: %w", n, errMarkerRows)
	}

	return nil
}

// TestLiveMarkerConformance runs the suite of the live row checks: only a
// NULL marker reads as live.
func TestLiveMarkerConformance(t *testing.T) {
	t.Parallel()

	storetest.RunLiveMarkers(t, func(t *testing.T) storetest.LiveMarkerStore {
		t.Helper()

		path := filepath.Join(t.TempDir(), "dustpan.db")

		copyTemplate(t, path)

		s, err := sqlite.Open(t.Context(), path)
		if err != nil {
			t.Fatalf("open store: %v", err)
		}

		t.Cleanup(func() {
			if err := s.Close(); err != nil {
				t.Errorf("close store: %v", err)
			}
		})

		return &liveMarkerStore{revocationStore: &revocationStore{Store: s, path: path}}
	})
}

// insertWithoutIDCase names one table and the two inserts of its ID guard.
type insertWithoutIDCase struct {
	name      string
	setup     []string
	withoutID string
	withID    string
}

// TestInsertWithoutIDIsRefused proves that an insert into namespaces, stacks
// or blob_purges with no ID column is refused, and that the same insert with
// a valid ID succeeds.
func TestInsertWithoutIDIsRefused(t *testing.T) {
	t.Parallel()

	cases := []insertWithoutIDCase{
		{
			name:  "namespaces",
			setup: nil,
			withoutID: `INSERT INTO namespaces (name, primary_installation, failover_version, created_at_ms)
VALUES ('ns-test', 'primary', 0, 0)`,
			withID: `INSERT INTO namespaces (name, primary_installation, failover_version, created_at_ms, id)
VALUES ('ns-test', 'primary', 0, 0, 'ns-1')`,
		},
		{
			name: "stacks",
			setup: []string{`INSERT INTO namespaces (name, primary_installation, failover_version, created_at_ms, id)
VALUES ('ns-test', 'primary', 0, 0, 'ns-1')`},
			withoutID: `INSERT INTO stacks (namespace, project, name, version, created_at_ms, updated_at_ms)
VALUES ('ns-test', 'project', 'stack', 1, 0, 0)`,
			withID: `INSERT INTO stacks (namespace, project, name, version, created_at_ms, updated_at_ms, id)
VALUES ('ns-test', 'project', 'stack', 1, 0, 0, 'stk-1')`,
		},
		{
			name:      "blob_purges",
			setup:     nil,
			withoutID: `INSERT INTO blob_purges (tool, unit_id, purged_at_ms) VALUES ('pulumi', 'unit-1', 1)`,
			withID:    `INSERT INTO blob_purges (tool, unit_id, purged_at_ms, namespace_id) VALUES ('pulumi', 'unit-1', 1, 'ns-1')`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			checkInsertWithoutID(t, tc)
		})
	}
}

// checkInsertWithoutID runs the two inserts of one case on a new database.
func checkInsertWithoutID(t *testing.T, tc insertWithoutIDCase) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.db")
	copyTemplate(t, path)

	conn, err := openRawDB(path)
	if err != nil {
		t.Fatalf("open raw database: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close raw database: %v", closeErr)
		}
	}()

	for _, statement := range tc.setup {
		if _, execErr := conn.ExecContext(t.Context(), statement); execErr != nil {
			t.Fatalf("%s: %v", statement, execErr)
		}
	}

	_, execErr := conn.ExecContext(t.Context(), tc.withoutID)
	if execErr == nil || !strings.Contains(execErr.Error(), "NOT NULL constraint failed") {
		t.Fatalf("insert into %s without an ID = %v, want a NOT NULL constraint error", tc.name, execErr)
	}

	if _, execErr := conn.ExecContext(t.Context(), tc.withID); execErr != nil {
		t.Fatalf("insert into %s with a valid ID: %v", tc.name, execErr)
	}
}
