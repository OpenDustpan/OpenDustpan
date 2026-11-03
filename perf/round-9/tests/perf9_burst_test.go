package argon2id_test

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/localuser"
	"opendustpan.org/opendustpan/pkg/localuser/argon2id"
)

func TestPerf9Burst(t *testing.T) {
	p := localuser.DefaultHashParams()
	b, _ := argon2id.NewBudget(localuser.DefaultHashMemoryBudgetKiB)
	h, _ := argon2id.New(b)
	ctx := context.Background()
	stored, err := h.Hash(ctx, "correct horse battery", p)
	if err != nil {
		t.Fatal(err)
	}
	for _, conc := range []int{4, 8, 16, 32} {
		var lat, wall []time.Duration
		var mu sync.Mutex
		var errs int
		for burst := 0; burst < 30; burst++ {
			var wg sync.WaitGroup
			start := time.Now()
			for i := 0; i < conc; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					s := time.Now()
					_, err := h.Verify(ctx, "correct horse battery", stored, p)
					d := time.Since(s)
					mu.Lock()
					lat = append(lat, d)
					if err != nil {
						errs++
					}
					mu.Unlock()
				}()
			}
			wg.Wait()
			wall = append(wall, time.Since(start))
		}
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		sort.Slice(wall, func(i, j int) bool { return wall[i] < wall[j] })
		q := func(d []time.Duration, x float64) time.Duration { return d[int(float64(len(d)-1)*x)] }
		t.Logf("burst conc=%d bursts=30 samples=%d errs=%d verify p50=%s p95=%s p99=%s burst-wall p50=%s p95=%s", conc, len(lat), errs,
			q(lat, .5), q(lat, .95), q(lat, .99), q(wall, .5), q(wall, .95))
	}
}
