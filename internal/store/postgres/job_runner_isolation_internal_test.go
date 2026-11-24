package postgres

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/internal/store/jobstore"
	"opendustpan.org/opendustpan/internal/store/postgres/internal/db"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// openRepeatableReadStore opens a store on a database whose session default is
// REPEATABLE READ, and creates the namespace acme in it.
func openRepeatableReadStore(t *testing.T) (*Store, namespace.Namespace) {
	t.Helper()

	u, err := url.Parse(membershipURL(t))
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}

	q := u.Query()
	q.Set("default_transaction_isolation", "repeatable read")
	u.RawQuery = strings.ReplaceAll(q.Encode(), "+", "%20")

	s, err := Open(t.Context(), u.String())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(s.Close)

	at := time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC)
	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: at, Name: "acme", PrimaryInstallation: "eu-west",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
	if createErr := s.CreateNamespace(t.Context(), ns); createErr != nil {
		t.Fatalf("create namespace: %v", createErr)
	}

	// The default of the session is REPEATABLE READ, so a plain transaction is
	// not a proof: the test reads the level inside the write of the job runner.
	var plain string
	if scanErr := s.pool.QueryRow(t.Context(), "SHOW default_transaction_isolation").Scan(&plain); scanErr != nil ||
		plain != "repeatable read" {
		t.Fatalf("default_transaction_isolation = %q, %v; want repeatable read", plain, scanErr)
	}

	return s, ns
}

// TestJobRunnerWriteIsReadCommitted proves that a write transaction of the job
// stores runs at READ COMMITTED when the server default is REPEATABLE READ. The
// write holds the namespace row FOR UPDATE, so reserves of one namespace run
// one after the other. Only under READ COMMITTED does the later reserve see the
// rows that the earlier one committed, so the caps of the log hold. Under
// REPEATABLE READ the snapshot is taken before the wait for the row lock, and
// the later reserve does not see the unconfirmed chunk of the earlier one.
//
// Mutation: change the options of jobRunner.Write back to BeginFunc. The
// transaction then runs at the default, and the test fails.
func TestJobRunnerWriteIsReadCommitted(t *testing.T) {
	t.Parallel()

	s, ns := openRepeatableReadStore(t)

	var level string

	fence := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion}

	err := jobRunner{s: s}.Write(t.Context(), fence, func(tx jobstore.Tx) error {
		jt, ok := tx.(*jobTx)
		if !ok {
			t.Fatalf("transaction is %T, want *jobTx", tx)
		}

		return jt.tx.QueryRow(t.Context(), "SHOW transaction_isolation").Scan(&level)
	})
	if err != nil {
		t.Fatalf("write: %v", err)
	}

	if level != "read committed" {
		t.Fatalf("transaction_isolation in jobRunner.Write = %q, want read committed", level)
	}
}

// TestRunTxIsReadCommitted proves that the transaction of the fenced and
// control write helpers runs at READ COMMITTED when the server default is
// REPEATABLE READ. inTx, inControlTx, inMinimumTx, inFencedTx and the helpers
// built on them all start their transaction in runTx. The lock of the scope
// @server and the shared write lock come before the rows that a write counts
// or checks, and only under READ COMMITTED does a write that waited see the rows
// that the write before it committed.
//
// Mutation: change runTx back to pgx.BeginFunc. The transaction then runs at
// the default, and the test fails.
func TestRunTxIsReadCommitted(t *testing.T) {
	t.Parallel()

	s, _ := openRepeatableReadStore(t)

	var level string

	read := func(tx pgx.Tx) error {
		return tx.QueryRow(t.Context(), "SHOW transaction_isolation").Scan(&level)
	}

	noop := func(*db.Queries) error { return nil }

	if err := s.runTx(t.Context(), noop, read); err != nil {
		t.Fatalf("runTx: %v", err)
	}

	if level != "read committed" {
		t.Fatalf("transaction_isolation in runTx = %q, want read committed", level)
	}
}
