package sqlite_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// batchCounter is a name lock counter that runs the real batch of the store
// and counts the batches that returned.
type batchCounter struct {
	authgate.NameLockCounter

	batches int
}

func (c *batchCounter) PruneNameLocks(
	ctx context.Context, age time.Duration, limit, ended int, e audit.Entry,
) (int64, error) {
	n, err := c.NameLockCounter.PruneNameLocks(ctx, age, limit, ended, e)
	c.batches++

	return n, err
}

// TestNameLockPruneRunLetsOtherWritesCommit seeds old counts for eight full
// batches and runs the prune. In each pause between two batches, the test
// does one write to the same store, in the same goroutine, after the real
// pause. SQLite has one writer, so a run that keeps the write lock between
// two batches makes that write wait. The test needs that each write commits
// with no error, one for each pause, and that the run deletes every old count
// and returns no error. No timer or busy handler sets the result: each write
// starts when no batch runs.
func TestNameLockPruneRunLetsOtherWritesCommit(t *testing.T) {
	t.Parallel()

	const batches = 8

	s := open(t)
	full := authgate.MaxNamePruneRows

	// Counts with no lock and a last failure at the start of 1970: all are old.
	_, err := sqlite.DB(s).ExecContext(t.Context(), `WITH RECURSIVE n(i) AS (
			SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
		INSERT INTO name_locks (scope, name, sub, failures, last_failure_ms)
		SELECT 'password', 'old-' || i, 's', 1, 1000 FROM n`, batches*full+1)
	if err != nil {
		t.Fatalf("seed the counts: %v", err)
	}

	at := time.Date(2026, time.November, 14, 12, 0, 0, 0, time.UTC)
	timer := localusersvc.NewPruneClock(time.Now)
	counter := &batchCounter{NameLockCounter: s, batches: 0}
	committed := 0

	clock := localusersvc.PruneClock{
		Now: timer.Now,
		Pause: func(ctx context.Context, d time.Duration) bool {
			ok := timer.Pause(ctx, d)

			id := "corp-" + strconv.Itoa(committed)
			p := session.ProviderConfig{
				CreatedAt: at, UpdatedAt: at, ID: id, Mode: session.ModeOIDC, Issuer: "https://" + id + ".idp.example",
				DisplayName: id, ClientID: "dustpan-" + id, SealedSecret: []byte("sealed"), Scopes: []string{"profile"},
				OAuth: session.OAuthSettings{
					AuthorizationURL: "", TokenURL: "", UserinfoURL: "", SubjectClaim: "",
					NameClaim: "", UsernameClaim: "", EmailClaim: "",
				},
				Version: 1, AutoCreate: true, Enabled: false,
			}

			if werr := s.CreateProviderConfig(ctx, p, storetest.ServerChange(at, "sign-in-provider:"+id)); werr != nil {
				t.Errorf("write in pause %d: %v", committed, werr)

				return ok
			}

			committed++

			return ok
		},
	}

	res, err := localusersvc.PruneLocks(t.Context(), counter, clock)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}

	if res.Deleted != int64(batches*full)+1 || res.Batches != batches+1 || counter.batches != batches+1 ||
		res.StopReason != localusersvc.PruneStopShortBatch {
		t.Fatalf(
			"result = %+v after %d batches, want %d deleted in %d batches, stopped by a short batch",
			res, counter.batches, batches*full+1, batches+1,
		)
	}

	// The run pauses after each full batch, and one write commits in each pause.
	if committed != batches {
		t.Fatalf("writes committed in the pauses = %d, want %d (one for each pause)", committed, batches)
	}
}
