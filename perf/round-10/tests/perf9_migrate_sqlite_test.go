package sqlite_test

import (
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

func TestPerf9Migrate(t *testing.T) {
	const n = 20
	var fresh, again []time.Duration
	for i := 0; i < n; i++ {
		path := filepath.Join(t.TempDir(), "p.db")
		s0 := time.Now()
		s, err := sqlite.Open(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		fresh = append(fresh, time.Since(s0))
		s.Close()
		s1 := time.Now()
		s, err = sqlite.Open(t.Context(), path)
		if err != nil {
			t.Fatal(err)
		}
		again = append(again, time.Since(s1))
		s.Close()
	}
	p := func(d []time.Duration, q float64) time.Duration {
		sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
		return d[int(float64(len(d)-1)*q)]
	}
	t.Log(fmt.Sprintf("sqlite fresh-migrate n=%d p50=%s p95=%s p99=%s", n, p(fresh, .5), p(fresh, .95), p(fresh, .99)))
	t.Log(fmt.Sprintf("sqlite reopen(no-op) n=%d p50=%s p95=%s p99=%s", n, p(again, .5), p(again, .95), p(again, .99)))
}
