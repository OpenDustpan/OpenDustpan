package sqlite_test

import (
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// TestNameLockPruneRunLetsOtherWritesCommit seeds old counts for eight full
// batches, runs the prune, and writes to the same store from a second
// goroutine all the time. SQLite has one writer, so a run that goes from one
// batch to the next with no pause keeps the write lock. The test needs that
// every write commits without an error, and that writes commit between the
// batches of the run. It also needs that the run deletes every old count and
// returns no error.
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

	var (
		committed atomic.Int64
		done      atomic.Bool
		writeErr  atomic.Value
		wg        sync.WaitGroup
	)

	at := time.Date(2026, time.November, 14, 12, 0, 0, 0, time.UTC)

	wg.Go(func() {
		for i := 0; !done.Load(); i++ {
			id := "corp-" + strconv.Itoa(i)
			p := session.ProviderConfig{
				CreatedAt: at, UpdatedAt: at, ID: id, Mode: session.ModeOIDC, Issuer: "https://" + id + ".idp.example",
				DisplayName: id, ClientID: "dustpan-" + id, SealedSecret: []byte("sealed"), Scopes: []string{"profile"},
				OAuth: session.OAuthSettings{
					AuthorizationURL: "", TokenURL: "", UserinfoURL: "", SubjectClaim: "",
					NameClaim: "", UsernameClaim: "", EmailClaim: "",
				},
				Version: 1, AutoCreate: true, Enabled: false,
			}

			werr := s.CreateProviderConfig(t.Context(), p, storetest.ServerChange(at, "sign-in-provider:"+id))
			// A busy answer after the busy timeout means that one batch ran longer
			// than the timeout on a loaded machine. The test counts commits, so it
			// tries again. Any other error fails the test.
			if werr != nil && strings.Contains(werr.Error(), "database is busy") {
				continue
			}

			if werr != nil {
				writeErr.Store(werr)

				return
			}

			committed.Add(1)
		}
	})

	res, err := localusersvc.PruneLocks(t.Context(), s, localusersvc.NewPruneClock(time.Now))

	done.Store(true)
	wg.Wait()

	if err != nil {
		t.Fatalf("prune: %v", err)
	}

	if e, ok := writeErr.Load().(error); ok {
		t.Fatalf("a concurrent write failed during the run: %v", e)
	}

	if res.Deleted != int64(batches*full)+1 || res.Batches != batches+1 ||
		res.StopReason != localusersvc.PruneStopShortBatch {
		t.Fatalf(
			"result = %+v, want %d deleted in %d batches, stopped by a short batch",
			res,
			batches*full+1,
			batches+1,
		)
	}

	// The run pauses after each full batch. In each pause, a write can commit.
	if got := committed.Load(); got < int64(batches) {
		t.Fatalf("writes committed during the run = %d, want at least %d (one for each pause)", got, batches)
	}
}
