package servertest_test

import (
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/backup"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
)

func perf10Pct(d []time.Duration) (p50, p95, p99 time.Duration) {
	s := slices.Clone(d)
	slices.Sort(s)
	at := func(q float64) time.Duration { return s[min(len(s)-1, int(q*float64(len(s))))] }
	return at(0.50), at(0.95), at(0.99)
}

func perf10Report(t *testing.T, name string, d []time.Duration) {
	p50, p95, p99 := perf10Pct(d)
	t.Logf("PERF10 %s n=%d p50=%.3fms p95=%.3fms p99=%.3fms", name, len(d), ms(p50), ms(p95), ms(p99))
}

func ms(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }

// TestPerf10Latency measures the latency of single requests on PostgreSQL.
func TestPerf10Latency(t *testing.T) {
	h := newRoundTripHarness(t, traceOff, dustpan.WithPublicURL("https://dustpan.example.com"))
	h.workspace = h.createWorkspace("web")
	h.createStack("base")

	// OpenTofu lock and unlock.
	for range 20 {
		h.lock()
		h.unlock()
	}
	var lk, ul []time.Duration
	for range 400 {
		t0 := time.Now()
		h.lock()
		lk = append(lk, time.Since(t0))
		t0 = time.Now()
		h.unlock()
		ul = append(ul, time.Since(t0))
	}
	perf10Report(t, "tofu-lock", lk)
	perf10Report(t, "tofu-unlock", ul)

	// Pulumi checkpoint write (the verbatim route of the CLI). No state.write gate runs on it.
	h.newStack()
	run := h.startUpdate()
	for i := 1; i <= 20; i++ {
		h.save(run, i)
	}
	var cp []time.Duration
	for i := 21; i < 421; i++ {
		t0 := time.Now()
		h.save(run, i)
		cp = append(cp, time.Since(t0))
	}
	perf10Report(t, "pulumi-checkpoint-save", cp)
	h.complete(run)

	// Pulumi update cycle: start, two saves, complete.
	var cyc []time.Duration
	for i := range 120 {
		h.newStack()
		t0 := time.Now()
		r := h.startUpdate()
		h.save(r, 1)
		h.save(r, 2)
		h.complete(r)
		d := time.Since(t0)
		if i >= 20 {
			cyc = append(cyc, d)
		}
	}
	perf10Report(t, "pulumi-update-cycle", cyc)

	// Pulumi stack rm: gated at state.write from the new commit on (old: no gate).
	var rm []time.Duration
	for i := range 220 {
		name := "rm" + strconv.Itoa(i)
		h.createStack(name)
		t0 := time.Now()
		h.call(http.MethodDelete, "/api/stacks/acme/web/"+name, "", http.StatusNoContent)
		d := time.Since(t0)
		if i >= 20 {
			rm = append(rm, d)
		}
	}
	perf10Report(t, "pulumi-stack-rm", rm)
}

// TestPerf10Backup times a backup and a restore of the round-trip data sets.
func TestPerf10Backup(t *testing.T) {
	for _, units := range []int{3, 30} {
		for run := range 5 {
			target, err := fs.Open(t.TempDir())
			if err != nil {
				t.Fatalf("open backup target: %v", err)
			}
			h := newRoundTripHarness(t, traceBackup, dustpan.WithBackupTarget(target, "test"),
				dustpan.WithOperationLease(roundTripLease))
			h.seedUnits(units)

			t0 := time.Now()
			var started struct {
				ID string `json:"id"`
			}
			h.decode(h.call(http.MethodPost, "/api/server/backups", "", http.StatusAccepted), &started)
			h.settle("/api/server/backups/"+started.ID, string(backup.StateRunning))
			h.settleOperations()
			tb := time.Since(t0)

			t0 = time.Now()
			var restore struct {
				ID string `json:"id"`
			}
			h.decode(h.call(http.MethodPost, "/api/server/restores", `{"backupID":"`+started.ID+`"}`, http.StatusAccepted), &restore)
			h.settle("/api/server/restores/"+restore.ID, "running", "checking", "applying")
			h.settleOperations()
			tr := time.Since(t0)
			t.Logf("PERF10 backup-restore units=%d run=%d backup=%.1fms restore=%.1fms", units, run, ms(tb), ms(tr))
		}
	}
}
