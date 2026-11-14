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
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/pgtest"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/access/accesstest"
	"opendustpan.org/opendustpan/pkg/accesstoken/accesstokentest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/audit/audittest"
	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob"
	blobfs "opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/principal/principaltest"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/secret/secrettest"
	"opendustpan.org/opendustpan/pkg/session/sessiontest"
	"opendustpan.org/opendustpan/pkg/stack"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/schema"
	"opendustpan.org/opendustpan/pkg/store/storetest"
	"opendustpan.org/opendustpan/pkg/tempadmin/tempadmintest"
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

// TestAccessConcurrency runs the cases of a grant or role check that races
// a write on a second handle of the same schema.
func TestAccessConcurrency(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL concurrency suite", envURL)
	}

	accesstest.RunConcurrent(t, func(t *testing.T) (access.Store, access.Store) {
		t.Helper()

		schemaURL := isolatedURL(t, base)

		return openURL(t, schemaURL), openURL(t, schemaURL)
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

// TestSignInFailuresConformance runs the shared failure-budget suite: both
// handles open one schema, as two servers open one database. The store reads
// its window from its own clock, so it ignores skew.
func TestSignInFailuresConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunSignInFailures(t, func(t *testing.T) storetest.SignInFailureHarness {
		t.Helper()

		schemaURL := isolatedURL(t, base)

		return storetest.SignInFailureHarness{
			Open: func(t *testing.T, _ time.Duration) authgate.FailureCounter {
				t.Helper()

				return openURL(t, schemaURL)
			},
		}
	})
}

// TestAuditCheckConformance runs the shared suite of the audit chain check
// state: both handles open one schema, as two servers open one database.
func TestAuditCheckConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunAuditCheck(t, func(t *testing.T) storetest.AuditCheckHarness {
		t.Helper()

		schemaURL := isolatedURL(t, base)

		return storetest.AuditCheckHarness{
			Open: func(t *testing.T) audit.CheckStore {
				t.Helper()

				return openURL(t, schemaURL)
			},
		}
	})
}

// TestSessionFreshConformance runs the session cases that change a policy
// on one handle and use a session on the other: two pools on one schema.
func TestSessionFreshConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	sessiontest.RunFresh(t, func(t *testing.T) (sessiontest.Store, sessiontest.Store) {
		t.Helper()

		schemaURL := isolatedURL(t, base)

		return openURL(t, schemaURL), openURL(t, schemaURL)
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

// TestTempAdminConformance runs the temporary administrator store suite.
func TestTempAdminConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	tempadmintest.Run(t, func(t *testing.T) tempadmintest.Store {
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

func TestItemKeysConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	secrettest.RunItemKeys(t, func(t *testing.T) secrettest.ItemKeyStore {
		t.Helper()

		return open(t, base)
	})
}

func TestItemSealerConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	secrettest.RunItemSealer(t, func(t *testing.T) *secrettest.SealerRig {
		t.Helper()

		blobs, err := blobfs.Open(t.TempDir())
		if err != nil {
			t.Fatalf("open blob store: %v", err)
		}

		t.Cleanup(func() { _ = blobs.Close() }) //nolint:errcheck // Test cleanup.

		return secrettest.NewSealerRig(t, open(t, base), blobs)
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

// TestShipperRefusesLeaselessStore proves that a shipper with a store-backed
// feed and no lease store is refused at construction.
func TestShipperRefusesLeaselessStore(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	s, err := postgres.Open(t.Context(), isolatedURL(t, base))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	audittest.CheckShipperRefusesLeaselessStore(t, s)
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

// TestAuditCheckpointEpochChange runs the case of a checkpoint write after
// an epoch change on PostgreSQL entries.
func TestAuditCheckpointEpochChange(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	s, err := postgres.Open(t.Context(), isolatedURL(t, base))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

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

// chainApprovalStore wraps a Store with its schema URL, so the approval
// cases can break audit appends with direct SQL. It embeds the store, so the
// suite also reads its chain state, list and durable operations.
type chainApprovalStore struct {
	*postgres.Store

	url string
}

var (
	_ audittest.ChainApprover   = (*chainApprovalStore)(nil)
	_ audittest.ChainBreaker    = (*chainApprovalStore)(nil)
	_ audittest.ChainOperations = (*chainApprovalStore)(nil)
)

// BreakAuditAppends implements audittest.ChainBreaker.
func (s *chainApprovalStore) BreakAuditAppends(ctx context.Context) error {
	return breakAuditAppends(ctx, s.url)
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
			Store: &chainApprovalStore{Store: s, url: schemaURL},
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
			DeleteChainState: func(t *testing.T) {
				t.Helper()

				execSchemaSQL(t, schemaURL, `DELETE FROM audit_chain_state`)
			},
		}
	})
}

// execSchemaSQL runs one statement on a new connection to schemaURL.
func execSchemaSQL(t *testing.T, schemaURL, statement string) {
	t.Helper()

	conn, err := pgx.Connect(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	if _, execErr := conn.Exec(t.Context(), statement); execErr != nil {
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

// tamperValue answers the parameter of the tamper hook for column: at_ms,
// sequence, fence_version and value_count are numbers, every other column
// is text.
func tamperValue(column string) string {
	if column == "at_ms" || column == "sequence" || column == "fence_version" || column == "value_count" {
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

func TestStateAtConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunStateAt(t, func(t *testing.T) storetest.StateAtStore {
		t.Helper()

		return open(t, base)
	})
}

func TestWorkspaceStateAtConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunWorkspaceStateAt(t, func(t *testing.T) storetest.WorkspaceStateAtStore {
		t.Helper()

		return openAuditedWorkspaces(t, base)
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

// TestUnitQueryConformance runs the unit page suite: one page of the units
// of a namespace in the order of the unit ID, with the filters in the query.
func TestUnitQueryConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunUnitQuery(t, func(t *testing.T) storetest.UnitQueryStore {
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

func TestBlobDeleteDuringBackup(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL blob delete suite", envURL)
	}

	storetest.RunBlobDelete(t, func(t *testing.T) storetest.BlobDeleteStore {
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

func TestDevDataConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("%s is not set", envURL)
	}

	storetest.RunDevData(t, func(t *testing.T, kind installation.Kind) storetest.DevDataStore {
		t.Helper()

		var store storetest.DevDataStore = openBackup(t, base)

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

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunRestore(t, func(t *testing.T) storetest.RestoreStore {
		t.Helper()

		return openBackup(t, base)
	})
}

// TestItemKeyBackupRestore proves that a backup copies the wrapped item key
// blobs and that content opens after a restore brought back a lost blob.
func TestItemKeyBackupRestore(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL restore suite", envURL)
	}

	storetest.RunItemKeyBackup(t, func(t *testing.T, in storetest.ItemKeyBackupInput) *storetest.ItemKeyBackupRig {
		t.Helper()

		st := openBackup(t, base)

		if _, err := st.ClaimKind(t.Context(), installation.KindProduction); err != nil {
			t.Fatalf("claim the installation kind: %v", err)
		}

		live, _ := openRestoreAuditBlobs(t)
		backups, restores := newRestoreAuditServices(t, st, live, in.Target, in.Key)

		return &storetest.ItemKeyBackupRig{Store: st, Backups: backups, Restores: restores, Live: live}
	})
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

	if _, err := st.ClaimKind(ctx, installation.KindProduction); err != nil {
		t.Fatalf("claim the installation kind: %v", err)
	}

	live, target := openRestoreAuditBlobs(t)
	backups, restores := newRestoreAuditServices(t, st, live, target, nil)

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
	t *testing.T, st *backupStore, live *blobfs.Store, target blob.Store, key secret.Wrapper,
) (*backup.Service, *backup.RestoreService) {
	t.Helper()

	backups, err := backup.NewService(backup.Config{
		Exporter:    st,
		Kinds:       st,
		Registry:    st,
		Blobs:       live,
		StateOpener: nil,
		Operations:  st.DurableOperationsPort(),
		Owner:       "",
		Lease:       0,
		Logger:      nil,
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
		Live:         st,
		Operations:   st.DurableOperationsPort(),
		Backups:      backups,
		Maintenance:  storetest.StateMaintenance{State: st, Hold: false},
		Blobs:        live,
		Target:       target,
		Key:          key,
		Installation: "test",
		AfterApply:   nil,
		Admit:        nil,

		AllowDevBackups: false,
		AllowDevHint:    "",
		Owner:           "",
		Lease:           0,
		CleanupLockWait: 0,
		RetryBase:       time.Millisecond,
		Now:             nil,
		Logger:          nil,
	})
	if err != nil {
		t.Fatalf("new restore service: %v", err)
	}

	return backups, restores
}

// waitRestoreSucceeded polls the restore id until it succeeds, failing on
// any other end state. The wait ends on the state change. The limit
// storetest.StuckTestLimit only ends a stuck test.
func waitRestoreSucceeded(t *testing.T, restores *backup.RestoreService, id backup.RestoreID) {
	t.Helper()

	ctx := t.Context()
	deadline := time.Now().Add(storetest.StuckTestLimit)

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

		// No worker runs in this test: the wait runs the due step, as a
		// worker does.
		if runErr := restores.RunOnce(ctx); runErr != nil && !errors.Is(runErr, durable.ErrNoWork) {
			t.Logf("run a restore operation: %v", runErr)
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

// IdempotencyStore implements storetest.IdempotencyHolder.
func (s *backupStore) IdempotencyStore() idempotency.Store { //nolint:ireturn // The capability returns the port.
	return s.Idempotency(idempotencytest.TestWrapper(s.t, 1))
}

var (
	_ storetest.Reopener       = (*backupStore)(nil)
	_ storetest.StagingWitness = (*backupStore)(nil)
	// Defense in depth: a store with no runner port fails to build here, so the runner round trip cannot be skipped.
	_ storetest.RunnerSeeder = (*backupStore)(nil)
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

// DropRestoreArea implements storetest.RestoreAreaDropper: it drops the
// staging schema of a, as the cleanup of the store does, with no record.
func (s *backupStore) DropRestoreArea(ctx context.Context, a backup.StagingArea) error {
	if _, err := backup.NewStagingArea(a.Restore, a.Token); err != nil {
		return err
	}

	area := "restore_stage_" + strings.TrimPrefix(a.Restore.String(), "rst-") + "_" + strconv.FormatInt(a.Token, 10)

	conn, err := pgx.Connect(ctx, s.url)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			s.t.Errorf("close: %v", closeErr)
		}
	}()

	if _, err := conn.Exec(ctx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{area}.Sanitize()+" CASCADE"); err != nil {
		return fmt.Errorf("drop schema %s: %w", area, err)
	}

	return nil
}

var _ storetest.RestoreAreaDropper = (*backupStore)(nil)

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

// EraseKindForTest implements storetest.KindEraser: it sets the stored
// installation kind back to NULL through a second connection, behind the port.
func (s *backupStore) EraseKindForTest(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, s.url)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	defer func() {
		_ = conn.Close(ctx) //nolint:errcheck // Test-only connection.
	}()

	var live string
	if err := conn.QueryRow(ctx, `SELECT current_schema()`).Scan(&live); err != nil {
		return fmt.Errorf("read the schema: %w", err)
	}

	// The installation table lives in the control schema of the live schema.
	control := pgx.Identifier{live + "_control"}.Sanitize()
	if _, err := conn.Exec(ctx, `UPDATE `+control+`.installation SET kind = NULL`); err != nil {
		return fmt.Errorf("erase the kind: %w", err)
	}

	return nil
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

// errUnknownEndedAtTable reports a table that the raw end-time reader does
// not know.
var errUnknownEndedAtTable = errors.New("unknown end-time table")

// StoredEndedAtMs implements storetest.NullEndedWitness: it reads the
// ended_at_ms column of one control row with a raw query. The control tables
// live in the control schema of the live schema.
func (s *backupStore) StoredEndedAtMs(
	ctx context.Context, table, id string,
) (int64, bool, error) {
	switch table {
	case backup.TableBackups, backup.TableRestores, backup.TableStoreChecks:
	default:
		return 0, false, fmt.Errorf("read ended_at_ms: %w: %q", errUnknownEndedAtTable, table)
	}

	conn, err := pgx.Connect(ctx, s.url)
	if err != nil {
		return 0, false, fmt.Errorf("read ended_at_ms: connect: %w", err)
	}

	var live string
	if err := conn.QueryRow(ctx, `SELECT current_schema()`).Scan(&live); err != nil {
		return 0, false, errors.Join(fmt.Errorf("read ended_at_ms: read the schema: %w", err), conn.Close(ctx))
	}

	query := `SELECT ended_at_ms FROM ` +
		pgx.Identifier{live + "_control", table}.Sanitize() + ` WHERE id = $1`

	var ms pgtype.Int8

	scanErr := conn.QueryRow(ctx, query, id).Scan(&ms)
	closeErr := conn.Close(ctx)

	if scanErr != nil {
		return 0, false, errors.Join(fmt.Errorf("read ended_at_ms of %s %s: %w", table, id, scanErr), closeErr)
	}

	if closeErr != nil {
		return 0, false, fmt.Errorf("read ended_at_ms: close: %w", closeErr)
	}

	return ms.Int64, ms.Valid, nil
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

var (
	_ storetest.AuditBreaker = (*sqlUpdatesStore)(nil)
	_ storetest.KeyEraser    = (*sqlUpdatesStore)(nil)
	_ storetest.UpdateLocker = (*sqlUpdatesStore)(nil)
)

// HoldUpdate implements storetest.UpdateLocker: it locks the row of the
// update FOR UPDATE in a transaction of its own connection, which release
// commits.
func (s *sqlUpdatesStore) HoldUpdate(ctx context.Context, stackID stack.ID, id stack.UpdateID) (func(), error) {
	conn, err := pgx.Connect(ctx, s.url)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}

	bg := context.WithoutCancel(ctx)
	closeConn := func() {
		if closeErr := conn.Close(bg); closeErr != nil {
			s.t.Errorf("close the lock connection: %v", closeErr)
		}
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		closeConn()

		return nil, fmt.Errorf("begin: %w", err)
	}

	const lock = "SELECT 1 FROM updates WHERE stack_id = $1 AND id = $2 FOR UPDATE"

	_, err = tx.Exec(ctx, lock, stackID.String(), id.String())
	if err != nil {
		closeConn()

		return nil, fmt.Errorf("lock the update: %w", err)
	}

	release := func() {
		if commitErr := tx.Commit(bg); commitErr != nil {
			s.t.Errorf("release the lock of the update: %v", commitErr)
		}

		closeConn()
	}

	return release, nil
}

// errNoDatabaseClock reports a wrapped store that does not read the clock of
// the database.
var errNoDatabaseClock = errors.New("wrapped store does not read the clock of the database")

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
	return breakAuditAppends(ctx, s.url)
}

// EraseCheckpointKeys implements storetest.KeyEraser.
func (s *sqlUpdatesStore) EraseCheckpointKeys(ctx context.Context) (err error) {
	conn, err := pgx.Connect(ctx, s.url)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close: %w", closeErr))
		}
	}()

	if _, err = conn.Exec(ctx, `UPDATE checkpoints SET blob_key = NULL`); err != nil {
		return fmt.Errorf("erase the checkpoint keys: %w", err)
	}

	return nil
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

	parsed, parseErr := url.Parse(schemaURL)
	if parseErr != nil {
		t.Fatalf("parse schema URL: %v", parseErr)
	}

	live := parsed.Query().Get("search_path")
	control := live + "_control"
	setPath := "SET search_path TO " + pgx.Identifier{live}.Sanitize() + ", " + pgx.Identifier{control}.Sanitize()

	if _, execErr := conn.Exec(ctx, setPath); execErr != nil {
		t.Fatalf("set search path: %v", execErr)
	}

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

// TestMissingStateRowIsUnavailable deletes the seeded installation row and
// proves that GetState and SetState answer ErrUnavailable, and that
// SetState does not create the row again: the migration seeds the row, so a
// missing row is a server fault.
func TestMissingStateRowIsUnavailable(t *testing.T) {
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

	parsed, parseErr := url.Parse(schemaURL)
	if parseErr != nil {
		t.Fatalf("parse schema URL: %v", parseErr)
	}

	live := parsed.Query().Get("search_path")
	control := live + "_control"
	setPath := "SET search_path TO " + pgx.Identifier{live}.Sanitize() + ", " + pgx.Identifier{control}.Sanitize()

	if _, execErr := conn.Exec(ctx, setPath); execErr != nil {
		t.Fatalf("set search path: %v", execErr)
	}

	if _, execErr := conn.Exec(ctx, `DELETE FROM installation`); execErr != nil {
		t.Fatalf("delete installation row: %v", execErr)
	}

	if _, err := s.GetState(ctx); !errors.Is(err, maintenance.ErrUnavailable) {
		t.Fatalf("get installation state without a row: err = %v, want ErrUnavailable", err)
	}

	if err := s.SetState(ctx, maintenance.StateMaintenance); !errors.Is(err, maintenance.ErrUnavailable) {
		t.Fatalf("set installation state without a row: err = %v, want ErrUnavailable", err)
	}

	var rows int64
	if scanErr := conn.QueryRow(ctx, `SELECT COUNT(*) FROM installation`).Scan(&rows); scanErr != nil {
		t.Fatalf("count installation rows: %v", scanErr)
	}

	if rows != 0 {
		t.Fatalf("installation rows after the writes = %d, want 0", rows)
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

	if err := s.RenameStack(ctx, ref, "renamed", ns.Fence(), renameTestEntry(ns.Name, ref)); err != nil {
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

// isolatedURL returns a URL for a migrated schema in a database of its own,
// cloned from the template that TestMain migrates one time. The clone is
// dropped when the test ends. A test that needs to see the migrations run, or
// to plant data before them, calls emptySchemaURL.
func isolatedURL(tb testing.TB, base string) string {
	tb.Helper()

	return pgtest.MigratedURL(tb, base)
}

// emptySchemaURL creates a fresh, empty schema and returns base with
// search_path set to it.
func emptySchemaURL(tb testing.TB, base string) string {
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

// TestOpenUsesTheHighestMinimum records two future migrations whose
// minimums sort in the other order as text: v0.10.0 is the higher one. A
// binary between them refuses to open.
func TestOpenUsesTheHighestMinimum(t *testing.T) {
	t.Parallel()

	migrated, versions, _ := newerSchemaDatabase(t, "v0.10.0")
	insertFutureVersion(t, migrated, versions[len(versions)-1]+1, "v0.3.0")

	if _, err := postgres.Open(t.Context(), migrated, postgres.WithBinaryVersion("v0.5.0")); !errors.Is(
		err, schema.ErrBinaryBelowMinimum,
	) {
		t.Fatalf("open between two minimums: err = %v, want ErrBinaryBelowMinimum", err)
	}

	s, err := postgres.Open(t.Context(), migrated, postgres.WithBinaryVersion("v0.10.0"))
	if err != nil {
		t.Fatalf("open at the highest minimum: %v", err)
	}

	s.Close()
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

	if len(applied) == 0 {
		t.Fatal("applied migrations are empty, want the recorded migrations")
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

// BenchmarkProtectedGroups measures the protected flag of 1000 groups at the
// scale of accesstest.Bench: one group at a time, and one page.
func BenchmarkProtectedGroups(b *testing.B) {
	base := os.Getenv(envURL)
	if base == "" {
		b.Skipf("set %s to run the PostgreSQL benchmarks", envURL)
	}

	accesstest.BenchProtectedGroups(b, open(b, base))
}

// revocationStore adds to the store the two hooks of the revocation suite: the
// runner pools of the store, and a write to the revocation journal that the
// CHECK constraint of the table does not stop.
type revocationStore struct {
	*postgres.Store

	url string
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

// KeepsUnknownKinds implements storetest.RevocationStore. The journal lives in
// the control schema, which the swap never copies, so a row of an unknown
// kind reaches the replay.
func (s *revocationStore) KeepsUnknownKinds() bool { return true }

// InsertRawRevocation implements storetest.RevocationStore. It drops the CHECK
// constraint of the kind in the schema of this store, as a write to the
// database with another tool could, and then inserts the row.
func (s *revocationStore) InsertRawRevocation(ctx context.Context, row storetest.RawRevocation) (err error) {
	u, err := url.Parse(s.url)
	if err != nil {
		return fmt.Errorf("parse the schema URL: %w", err)
	}

	table := pgx.Identifier{u.Query().Get("search_path") + "_control", "maintenance_revocations"}.Sanitize()

	conn, err := pgx.Connect(ctx, s.url)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close: %w", closeErr))
		}
	}()

	drop := "ALTER TABLE " + table + " DROP CONSTRAINT IF EXISTS maintenance_revocations_kind"
	if _, err = conn.Exec(ctx, drop); err != nil {
		return fmt.Errorf("drop the check constraint: %w", err)
	}

	insert := "INSERT INTO " + table + " (kind, target, end_reason, actor, object, at_ms) VALUES ($1, $2, '', $3, $4, $5)"
	if _, err = conn.Exec(ctx, insert, row.Kind, row.Target, row.Actor, row.Object, row.AtMs); err != nil {
		return fmt.Errorf("insert the raw revocation: %w", err)
	}

	return nil
}

// TestRevocationConformance runs the suite of the writes that only reduce
// access: they run in maintenance mode, and a restore keeps them.
func TestRevocationConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunRevocations(t, func(t *testing.T) storetest.RevocationStore {
		t.Helper()

		schemaURL := isolatedURL(t, base)

		return &revocationStore{Store: openURL(t, schemaURL), url: schemaURL}
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

// SetMarker implements storetest.LiveMarkerStore. It writes to the schema of
// this store with another connection, as a restored backup or another tool
// could.
func (s *liveMarkerStore) SetMarker(ctx context.Context, table, column, id string, value *int64) (err error) {
	statements := map[string]string{
		"principals/deleted_at_ms": `UPDATE principals SET deleted_at_ms = $1 WHERE id = $2`,
		"grants/revoked_at_ms":     `UPDATE grants SET revoked_at_ms = $1 WHERE id = $2`,
		"roles/deleted_at_ms":      `UPDATE roles SET deleted_at_ms = $1 WHERE id = $2`,
		"workspaces/deleted_at_ms": `UPDATE workspaces SET deleted_at_ms = $1 WHERE id = $2`,
	}

	statement, ok := statements[table+"/"+column]
	if !ok {
		return fmt.Errorf("%s.%s: %w", table, column, errNoMarker)
	}

	conn, err := pgx.Connect(ctx, s.url)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	defer func() {
		if closeErr := conn.Close(ctx); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close: %w", closeErr))
		}
	}()

	tag, err := conn.Exec(ctx, statement, value, id)
	if err != nil {
		return fmt.Errorf("set the marker: %w", err)
	}

	if tag.RowsAffected() != 1 {
		return fmt.Errorf("set the marker: %d rows changed: %w", tag.RowsAffected(), errMarkerRows)
	}

	return nil
}

// TestLiveMarkerConformance runs the suite of the live row checks: only a
// NULL marker reads as live.
func TestLiveMarkerConformance(t *testing.T) {
	t.Parallel()

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

	storetest.RunLiveMarkers(t, func(t *testing.T) storetest.LiveMarkerStore {
		t.Helper()

		schemaURL := isolatedURL(t, base)

		return &liveMarkerStore{revocationStore: &revocationStore{Store: openURL(t, schemaURL), url: schemaURL}}
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

	base := os.Getenv(envURL)
	if base == "" {
		t.Skipf("set %s to run the PostgreSQL conformance suite", envURL)
	}

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

			checkInsertWithoutID(t, base, tc)
		})
	}
}

// checkInsertWithoutID runs the two inserts of one case on a new schema.
func checkInsertWithoutID(t *testing.T, base string, tc insertWithoutIDCase) {
	t.Helper()

	schemaURL := isolatedURL(t, base)

	conn, err := pgx.Connect(t.Context(), schemaURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(t.Context()); closeErr != nil {
			t.Errorf("close: %v", closeErr)
		}
	}()

	for _, statement := range tc.setup {
		if _, execErr := conn.Exec(t.Context(), statement); execErr != nil {
			t.Fatalf("%s: %v", statement, execErr)
		}
	}

	// SQLSTATE 23502 is not_null_violation.
	_, execErr := conn.Exec(t.Context(), tc.withoutID)
	if execErr == nil || !strings.Contains(execErr.Error(), "SQLSTATE 23502") {
		t.Fatalf("insert into %s without an ID = %v, want a NOT NULL violation", tc.name, execErr)
	}

	if _, execErr := conn.Exec(t.Context(), tc.withID); execErr != nil {
		t.Fatalf("insert into %s with a valid ID: %v", tc.name, execErr)
	}
}
