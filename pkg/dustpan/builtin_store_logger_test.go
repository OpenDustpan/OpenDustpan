package dustpan_test

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/trigger"
)

// storeLogRecords is a slog.Handler that keeps every record in memory, so a
// test reads the line that an adapter writes. It is safe for concurrent use.
type storeLogRecords struct {
	mu      sync.Mutex
	records []slog.Record
}

// Enabled accepts every level, so no line escapes the capture.
func (r *storeLogRecords) Enabled(context.Context, slog.Level) bool { return true }

// Handle records rec.
func (r *storeLogRecords) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.records = append(r.records, rec)

	return nil
}

// WithAttrs returns the same recorder: attributes arrive on the record.
func (r *storeLogRecords) WithAttrs([]slog.Attr) slog.Handler { return r }

// WithGroup returns the same recorder: the checked line holds no group.
func (r *storeLogRecords) WithGroup(string) slog.Handler { return r }

// hasMessage reports whether a record holds the exact message.
func (r *storeLogRecords) hasMessage(message string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	for i := range r.records {
		if r.records[i].Message == message {
			return true
		}
	}

	return false
}

// TestWithStoreLoggerReachesTheAdapter proves that WithStoreLogger passes the
// logger to the SQLite adapter. A put of a trigger visits a stored follows
// trigger that fails its checks, and the adapter writes the warning through
// the logger that OpenStore gave it.
func TestWithStoreLoggerReachesTheAdapter(t *testing.T) {
	t.Parallel()

	const message = "stored trigger left out of the loop check: it fails its checks"

	path := filepath.Join(t.TempDir(), "dustpan.db")
	recorder := &storeLogRecords{mu: sync.Mutex{}, records: nil}

	st, err := dustpan.OpenStore(t.Context(), "sqlite:"+path, dustpan.WithStoreLogger(slog.New(recorder)))
	if err != nil {
		t.Fatalf("open the store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close the store: %v", closeErr)
		}
	})

	sqliteStore, ok := st.StorePorts.(*sqlite.Store)
	if !ok {
		t.Fatalf("built-in store = %T, want *sqlite.Store", st.StorePorts)
	}

	ns := namespace.Namespace{
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		ID:                  namespace.NewID(),
		Name:                "acme",
		PrimaryInstallation: "eu-west",
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
		FailoverVersion:     1,
	}

	if err := sqliteStore.CreateNamespace(t.Context(), ns); err != nil {
		t.Fatalf("create the namespace: %v", err)
	}

	plantStoredFollowTrigger(t.Context(), t, path)

	tr := trigger.Trigger{
		ID:          "trg-new",
		Unit:        graph.Unit{Namespace: "acme", Tool: "pulumi", ID: "web"},
		Kind:        trigger.KindReviewRequestPush,
		Frequency:   trigger.Frequency{Preset: "", Cron: ""},
		Source:      graph.Unit{Namespace: "", Tool: "", ID: ""},
		Enabled:     true,
		LastSaver:   "user:ada",
		LastSaverID: "prn-user:ada",
	}

	if err := sqliteStore.Triggers().Put(t.Context(), tr, ns.Fence(), triggerLogEntry()); err != nil {
		t.Fatalf("put the trigger: %v", err)
	}

	if !recorder.hasMessage(message) {
		t.Fatal("the logger got no record from the SQLite adapter")
	}
}

// plantStoredFollowTrigger writes a follows trigger row that fails its checks:
// its source names another namespace than its unit. No port writes such a row,
// so the test writes it as a restore of another version would. The store holds
// the database file, so the write opens the same file once, writes the row and
// releases the file.
func plantStoredFollowTrigger(ctx context.Context, t *testing.T, path string) {
	t.Helper()

	conn, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(10000)")
	if err != nil {
		t.Fatalf("open the database file: %v", err)
	}

	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			t.Errorf("close the database file: %v", closeErr)
		}
	}()

	const plant = `INSERT INTO triggers (
    namespace_id, id, unit_tool, unit_id, kind, preset, cron, source_namespace,
    source_tool, source_unit_id, enabled, last_saver_subject, last_saver_id,
    created_at_ms, updated_at_ms
)
SELECT id, 'trg-stored', 'pulumi', 'api', 'follows', '', '', 'elsewhere',
    'pulumi', 'source', 1, 'user:ada', 'prn-user:ada', 1, 1
FROM namespaces
WHERE name = 'acme'`

	if _, err := conn.ExecContext(ctx, plant); err != nil {
		t.Fatalf("plant the stored trigger: %v", err)
	}
}

// triggerLogEntry returns a valid audit entry for a write of a trigger in acme.
func triggerLogEntry() audit.Entry {
	return audit.Entry{
		At: time.UnixMilli(1), Namespace: "acme", Subject: "user:ada", IDPSubject: "", TokenID: "",
		Action: "trigger:put", Object: "pulumi:acme/web", Version: "", ValuePath: "", ValueCount: nil,
		Changes: nil, SourceIP: "192.0.2.1", UserAgent: "", Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "",
		PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "",
	}
}
