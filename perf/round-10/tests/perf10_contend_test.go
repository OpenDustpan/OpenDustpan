package servertest_test

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/servertest"
	"opendustpan.org/opendustpan/pkg/dustpan"
)

// TestPerf10Contend: 16 clients lock the same workspace at once, 60 rounds (the shape of the load harness
// workspaceLocks flow). Latency is split into the winner (200) and the refused (409).
func TestPerf10Contend(t *testing.T) {
	h := newRoundTripHarness(t, traceOff, dustpan.WithPublicURL("https://dustpan.example.com"))
	h.workspace = h.createWorkspace("web")
	url := h.cluster.URL(0) + "/api/v2/workspaces/" + h.workspace + "/actions/lock"
	var win, lose []time.Duration
	for round := range 70 {
		var wg sync.WaitGroup
		var mu sync.Mutex
		start := make(chan struct{})
		for range 16 {
			wg.Go(func() {
				req, _ := http.NewRequestWithContext(t.Context(), http.MethodPost, url,
					strings.NewReader(`{"data":{"attributes":{"reason":"contend"}}}`))
				req.Header.Set("Authorization", "Bearer "+servertest.Token)
				req.Header.Set("Content-Type", "application/json")
				<-start
				t0 := time.Now()
				resp, err := h.client.Do(req)
				d := time.Since(t0)
				if err != nil {
					t.Errorf("lock: %v", err)
					return
				}
				io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				mu.Lock()
				defer mu.Unlock()
				if round < 10 {
					return
				}
				switch resp.StatusCode {
				case 200:
					win = append(win, d)
				case 409:
					lose = append(lose, d)
				default:
					t.Errorf("status %d", resp.StatusCode)
				}
			})
		}
		close(start)
		wg.Wait()
		h.unlock()
	}
	perf10Report(t, "contended-lock-winner", win)
	perf10Report(t, "contended-lock-refused", lose)
	all := append(append([]time.Duration{}, win...), lose...)
	perf10Report(t, "contended-lock-all", all)
}
