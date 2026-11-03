package postgres_test

import (
	"fmt"
	"os"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"opendustpan.org/opendustpan/pkg/notify"

	"opendustpan.org/opendustpan/internal/authgate"
	"opendustpan.org/opendustpan/pkg/audit"
)

func TestPerf9NameLocks(t *testing.T) {
	base := os.Getenv(envURL)
	if base == "" {
		t.Skip("no url")
	}
	url := isolatedURL(t, base)
	s := openURL(t, url)
	ctx := t.Context()
	rows := 100000
	if v := os.Getenv("PERF9_ROWS"); v != "" {
		fmt.Sscan(v, &rows)
	}
	const workers = 16
	var mu sync.Mutex
	var lat []time.Duration
	var wg sync.WaitGroup
	start := time.Now()
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var local []time.Duration
			for i := w; i < rows; i += workers {
				name := fmt.Sprintf("n%08d", i)
				t0 := time.Now()
				// one failed sign-in: the name+source count and the name count
				if _, err := s.AddNameFailure(ctx, authgate.NameFailure{Events: perf9Events(), Scope: authgate.ScopePassword, Name: name, Sub: "192.0.2.1", Rule: authgate.PasswordRule()}); err != nil {
					t.Errorf("add: %v", err)
					return
				}
				if _, err := s.AddNameFailure(ctx, authgate.NameFailure{Events: perf9Events(), Scope: authgate.ScopePasswordName, Name: name, Sub: "", Rule: authgate.PasswordNameRule()}); err != nil {
					t.Errorf("add: %v", err)
					return
				}
				local = append(local, time.Since(t0))
			}
			mu.Lock()
			lat = append(lat, local...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	wall := time.Since(start)
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	q := func(p float64) time.Duration { return lat[int(float64(len(lat)-1)*p)] }
	t.Logf("failed sign-ins=%d (2 counts each) workers=%d wall=%s rate=%.0f/s add-pair p50=%s p95=%s p99=%s",
		rows, workers, wall, float64(rows)/wall.Seconds(), q(.5), q(.95), q(.99))

	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	var n int64
	var size int64
	_ = conn.QueryRow(ctx, "select count(*), pg_total_relation_size('name_locks') from name_locks").Scan(&n, &size)
	t.Logf("name_locks rows=%d total_size=%.1f MiB", n, float64(size)/1048576)

	// age every row past the prune age (48h) so the prune can delete it
	if _, err := conn.Exec(ctx, "update name_locks set last_failure_ms = last_failure_ms - 172800000*2, locked_until_ms=0, last_lock_end_ms=0"); err != nil {
		t.Fatal(err)
	}
	entry := audit.Entry{
		At: time.Now().UTC(), Namespace: audit.ServerScope, Subject: "server", Action: "sign-in.name-locks-pruned",
		Object: "name-locks", Channel: audit.ChannelAPI, PrincipalKind: audit.PrincipalKindService, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		Reason: "perf round 9", SourceIP: "", UserAgent: "",
	}
	var runs []time.Duration
	var total int64
	for {
		t0 := time.Now()
		d, err := s.PruneNameLocks(ctx, authgate.NamePruneAge, authgate.MaxNamePruneRows, authgate.MaxNamePruneEnded, entry)
		if err != nil {
			t.Fatalf("prune: %v", err)
		}
		runs = append(runs, time.Since(t0))
		total += d
		if d == 0 {
			break
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i] < runs[j] })
	var sum time.Duration
	for _, r := range runs {
		sum += r
	}
	t.Logf("prune: deleted=%d runs=%d (limit %d each) per-run p50=%s max=%s total=%s", total, len(runs), authgate.MaxNamePruneRows,
		runs[len(runs)/2], runs[len(runs)-1], sum)
	_ = conn.QueryRow(ctx, "select count(*) from name_locks").Scan(&n)
	t.Logf("name_locks rows after prune=%d", n)
}

func perf9Events() *authgate.LockEvents {
	e := func(a string) audit.Entry { return audit.Entry{Action: a} }
	return &authgate.LockEvents{Locked: e(authgate.ActionNameLocked), Ended: e(authgate.ActionNameLockEnded),
		Alert: notify.Notification{Type: notify.SignInLocked}}
}
