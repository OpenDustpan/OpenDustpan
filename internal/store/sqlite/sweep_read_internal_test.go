package sqlite

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
)

// TestSweepInactiveFailsClosedWhenARead proves that the inactivity sweep
// returns an error and no result when a read of its transaction fails. The
// sweep reads temp_admins in every statement that selects its records, so a
// hidden table makes its first read fail. The error comes from the first
// statement of the transaction, so the test shows the fail-closed return of
// the sweep and not the text of the later statement that the failure would
// reach.
func TestSweepInactiveFailsClosedWhenARead(t *testing.T) {
	t.Parallel()

	s := openSeededKeepStore(t)

	_, err := s.conn.ExecContext(t.Context(), `ALTER TABLE temp_admins RENAME TO temp_admins_hidden`)
	if err != nil {
		t.Fatalf("hide the temp_admins table: %v", err)
	}

	sw := accesstoken.Sweep{Inactivity: time.Hour, Tokens: true, Accounts: true}
	entry := func(time.Time, string) audit.Entry {
		return audit.Entry{
			At: time.Time{}, Namespace: "", Subject: "", IDPSubject: "", TokenID: "", Action: "", Object: "",
			Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: "",
			Kind: "", Outcome: "", RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
			FenceVersion: 0, PrincipalKind: "", JobID: "", Reason: "", Origin: nil,
		}
	}

	res, err := s.SweepInactive(t.Context(), sw, entry)
	if err == nil {
		t.Fatalf("sweep with a failed read = %+v, nil; want an error", res)
	}

	if len(res.Tokens) != 0 || len(res.Principals) != 0 {
		t.Fatalf("sweep with a failed read returned %+v, want an empty result", res)
	}
}
