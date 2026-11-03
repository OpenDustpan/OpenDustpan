package postgres_test

import (
	"fmt"
	"os"
	"sort"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/store/postgres"
)

func TestPerf9Migrate(t *testing.T) {
	base := os.Getenv(envURL)
	if base == "" {
		t.Skip("no url")
	}
	const n = 20
	var fresh, again []time.Duration
	for i := 0; i < n; i++ {
		u := emptySchemaURL(t, base)
		s0 := time.Now()
		s, err := postgres.Open(t.Context(), u)
		if err != nil {
			t.Fatal(err)
		}
		fresh = append(fresh, time.Since(s0))
		s.Close()
		s1 := time.Now()
		s, err = postgres.Open(t.Context(), u)
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
	t.Log(fmt.Sprintf("postgres fresh-migrate n=%d p50=%s p95=%s p99=%s", n, p(fresh, .5), p(fresh, .95), p(fresh, .99)))
	t.Log(fmt.Sprintf("postgres reopen(no-op) n=%d p50=%s p95=%s p99=%s", n, p(again, .5), p(again, .95), p(again, .99)))
}
