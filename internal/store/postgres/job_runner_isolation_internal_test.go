package postgres

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/jobstore"
	"opendustpan.org/opendustpan/pkg/namespace"
)

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

	ns := namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Date(2026, time.November, 1, 12, 0, 0, 0, time.UTC), Name: "acme", PrimaryInstallation: "eu-west",
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

	var level string

	fence := namespace.Fence{Namespace: ns.Name, FailoverVersion: ns.FailoverVersion}

	err = jobRunner{s: s}.Write(t.Context(), fence, func(tx jobstore.Tx) error {
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
