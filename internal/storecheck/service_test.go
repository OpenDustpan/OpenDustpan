package storecheck_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/storecheck"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/durable"
)

var (
	errMaintenance = errors.New("installation is in maintenance mode")
	errRestoreRuns = errors.New("restore runs")
)

func TestReportMeasuresAndFindsTheCapabilities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		enforce bool
		want    storecheck.ConditionalWrites
	}{
		{name: "a store that enforces conditional writes", enforce: true, want: storecheck.ConditionalWritesEnforced},
		{name: "a store that ignores conditional writes", enforce: false, want: storecheck.ConditionalWritesIgnored},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, nil)
			r.blobs.enforce = tc.enforce

			id := r.start(t, "blobs")
			r.runOne(t)

			rec, report := r.report(t, id)
			if rec.State != storecheck.StateSucceeded || report == nil || !report.Passed {
				t.Fatalf("state = %q, report = %+v", rec.State, report)
			}

			checkMeasured(t, id, report)

			if report.ConditionalWrites != tc.want {
				t.Fatalf("ConditionalWrites = %q, want %q", report.ConditionalWrites, tc.want)
			}

			if left := r.blobs.keys(); len(left) != 0 {
				t.Fatalf("the check left %d objects", len(left))
			}
		})
	}
}

// checkMeasured fails unless report holds a figure for each measurement, the
// cases of the suite and the prefix of check id.
func checkMeasured(t *testing.T, id storecheck.ID, report *storecheck.Report) {
	t.Helper()

	if report.ThrottlingSeen || !report.PrefixDeleted {
		t.Fatalf("ThrottlingSeen = %v, PrefixDeleted = %v", report.ThrottlingSeen, report.PrefixDeleted)
	}

	if report.PutBytesPerSecond <= 0 || report.GetBytesPerSecond <= 0 || report.DeletesPerSecond <= 0 ||
		report.PutLatency.P99 < report.PutLatency.P50 {
		t.Fatalf("figures = %+v", report)
	}

	if len(report.Cases) != 11 ||
		report.Cases[len(report.Cases)-1].Name != "delete of the prefix removes every object" {
		t.Fatalf("cases = %+v", report.Cases)
	}

	if report.Prefix.String() != strings.TrimSuffix(id.Prefix(), "/") || report.Target != storecheck.TargetBlobs {
		t.Fatalf("prefix = %q, target = %q", report.Prefix, report.Target)
	}
}

func TestCheckWritesOnlyUnderItsPrefix(t *testing.T) {
	t.Parallel()

	r := newRig(t, nil)
	r.blobs.seed("namespaces/ns-x/data", "backups/bkp-x/manifest.json")

	id := r.start(t, "blobs")
	r.runOne(t)

	for _, call := range r.blobs.calls {
		verb, key, _ := strings.Cut(call, " ")
		if verb == "put" || verb == "delete" || verb == "get" {
			if !strings.HasPrefix(key, id.Prefix()) {
				t.Fatalf("call %q is outside %q", call, id.Prefix())
			}
		}
	}

	if got := r.blobs.keys(); len(got) != 2 {
		t.Fatalf("store holds %v, want the 2 seeded objects", got)
	}
}

func TestCheckReadsTheBackupTarget(t *testing.T) {
	t.Parallel()

	r := newRig(t, nil)
	r.start(t, "backup")
	r.runOne(t)

	if r.backup.callCount() == 0 || r.blobs.callCount() != 0 {
		t.Fatalf("backup calls = %d, blob calls = %d", r.backup.callCount(), r.blobs.callCount())
	}
}

func TestStartRefusesATargetThatIsNotOnTheList(t *testing.T) {
	t.Parallel()

	for _, target := range []string{"", "Blobs", "s3://bucket", "https://example.test", "blobs ", "../backup", "all"} {
		t.Run(target, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, nil)
			before := len(r.audit.all())

			_, err := r.svc.Start(t.Context(), r.request(target))
			if !errors.Is(err, storecheck.ErrInvalidTarget) {
				t.Fatalf("Start(%q) err = %v, want ErrInvalidTarget", target, err)
			}

			entries := r.audit.all()
			if len(entries) != before+1 || entries[before].Outcome != audit.OutcomeDenied {
				t.Fatalf("entries = %+v", entries)
			}

			if strings.Contains(fmt.Sprint(entries[before].Changes), target) && target != "" {
				t.Fatalf("the denial entry holds the target %q", target)
			}
		})
	}
}

func TestStartRefusesABackupTargetThatIsNotSet(t *testing.T) {
	t.Parallel()

	r := newRig(t, func(c *storecheck.Config) { c.Backup = nil })

	if _, err := r.svc.Start(t.Context(), r.request("backup")); !errors.Is(err, storecheck.ErrNoTarget) {
		t.Fatalf("err = %v, want ErrNoTarget", err)
	}
}

func TestOneCheckRunsAtATime(t *testing.T) {
	t.Parallel()

	r := newRig(t, nil)
	r.start(t, "blobs")

	for _, target := range []string{"blobs", "backup"} {
		_, err := r.svc.Start(t.Context(), r.request(target))
		if !errors.Is(err, storecheck.ErrRunning) {
			t.Fatalf("second start on %q err = %v, want ErrRunning", target, err)
		}
	}

	denied := 0

	for _, e := range r.audit.all() {
		if e.Outcome == audit.OutcomeDenied {
			denied++
		}
	}

	if denied != 2 {
		t.Fatalf("%d denial entries, want 2", denied)
	}
}

func TestCooldownFollowsTheEndOfACheckOnTheSameTarget(t *testing.T) {
	t.Parallel()

	r := newRig(t, nil)
	r.start(t, "blobs")
	r.runOne(t)

	// The check ended now. The same target waits, the other target does not.
	if _, err := r.svc.Start(t.Context(), r.request("blobs")); !errors.Is(err, storecheck.ErrCooldown) {
		t.Fatalf("start at once err = %v, want ErrCooldown", err)
	}

	*r.clock = r.clock.Add(storecheck.Cooldown - time.Second)

	if _, err := r.svc.Start(t.Context(), r.request("blobs")); !errors.Is(err, storecheck.ErrCooldown) {
		t.Fatalf("start before the end of the cooldown err = %v, want ErrCooldown", err)
	}

	if _, err := r.svc.Start(t.Context(), r.request("backup")); err != nil {
		t.Fatalf("start on the other target: %v", err)
	}

	r.runOne(t)

	*r.clock = r.clock.Add(2 * time.Second)

	if _, err := r.svc.Start(t.Context(), r.request("blobs")); err != nil {
		t.Fatalf("start after the cooldown: %v", err)
	}
}

func TestStartRefusedDuringMaintenanceOrARestore(t *testing.T) {
	t.Parallel()

	r := newRig(t, nil)
	r.admit = errMaintenance

	_, err := r.svc.Start(t.Context(), r.request("blobs"))
	if !errors.Is(err, storecheck.ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused", err)
	}

	if rows := r.listRows(t); len(rows) != 0 {
		t.Fatalf("a refused start stored %d rows", len(rows))
	}

	// A check that waits in the queue when maintenance starts waits too.
	r.admit = nil
	id := r.start(t, "blobs")
	r.admit = errRestoreRuns

	if err := r.svc.RunOnce(t.Context()); err == nil {
		t.Fatal("RunOnce during maintenance returned no error")
	}

	if rec, _ := r.report(t, id); rec.State != storecheck.StateRunning || r.blobs.callCount() != 0 {
		t.Fatalf("state = %q, store calls = %d", rec.State, r.blobs.callCount())
	}
}

func TestGetOfAnUnknownIDIsNotFound(t *testing.T) {
	t.Parallel()

	r := newRig(t, nil)

	if _, err := r.svc.Get(t.Context(), "chk-unknownunknown"); !errors.Is(err, storecheck.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestBudgetStopsTheCheck(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		limits storecheck.Limits
	}{
		{
			name:   "objects",
			limits: storecheck.Limits{MaxObjects: 5, MaxTotalBytes: 0, MaxObjectBytes: 0, Timeout: 0, Cooldown: 0},
		},
		{
			name: "total bytes",
			limits: storecheck.Limits{
				MaxObjects:     0,
				MaxTotalBytes:  100 << 10,
				MaxObjectBytes: 0,
				Timeout:        0,
				Cooldown:       0,
			},
		},
		{
			name: "one object",
			limits: storecheck.Limits{
				MaxObjects:     0,
				MaxTotalBytes:  0,
				MaxObjectBytes: 1 << 20,
				Timeout:        0,
				Cooldown:       0,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, func(c *storecheck.Config) { c.Limits = tc.limits })
			id := r.start(t, "blobs")
			r.runOne(t)

			rec, report := r.report(t, id)
			if rec.State != storecheck.StateFailed || rec.Error != "the check budget ended" || report == nil {
				t.Fatalf("state = %q, error = %q, report = %v", rec.State, rec.Error, report)
			}

			if left := r.blobs.keys(); len(left) != 0 {
				t.Fatalf("a check that ran out of budget left %v", left)
			}

			if report.Passed {
				t.Fatal("a check that ran out of budget passed")
			}
		})
	}
}

func TestBudgetDefaults(t *testing.T) {
	t.Parallel()

	if storecheck.MaxObjects != 1000 || storecheck.MaxTotalBytes != 256<<20 || storecheck.MaxObjectBytes != 64<<20 ||
		storecheck.Timeout != 10*time.Minute || storecheck.Cooldown != 15*time.Minute {
		t.Fatal("the budget is not 1,000 objects, 256 MiB, 64 MiB, 10 minutes and 15 minutes")
	}
}

func TestCheckStopsAtTheTimeLimit(t *testing.T) {
	t.Parallel()

	r := newRig(t, func(c *storecheck.Config) {
		c.Limits = storecheck.Limits{
			MaxObjects:     0,
			MaxTotalBytes:  0,
			MaxObjectBytes: 0,
			Timeout:        time.Nanosecond,
			Cooldown:       0,
		}
	})

	id := r.start(t, "blobs")
	r.runOne(t)

	rec, _ := r.report(t, id)
	if rec.State != storecheck.StateFailed || !strings.Contains(rec.Error, "time limit") {
		t.Fatalf("state = %q, error = %q", rec.State, rec.Error)
	}

	if left := r.blobs.keys(); len(left) != 0 {
		t.Fatalf("a check that ran out of time left %v", left)
	}
}

func TestReportSaysWhenThrottlingWasSeen(t *testing.T) {
	t.Parallel()

	var once sync.Once

	r := newRig(t, nil)
	r.blobs.putFail = func(key blob.Key) error {
		var err error

		if strings.Contains(key.String(), "measure/") {
			once.Do(func() { err = blob.ErrThrottled })
		}

		return err
	}

	id := r.start(t, "blobs")
	r.runOne(t)

	_, report := r.report(t, id)
	if report == nil || !report.ThrottlingSeen {
		t.Fatalf("report = %+v, want ThrottlingSeen", report)
	}
}

func TestEntriesNameTheTargetThePrincipalAndTheResult(t *testing.T) {
	t.Parallel()

	r := newRig(t, nil)
	r.start(t, "blobs")
	r.runOne(t)

	entries := r.audit.all()
	if len(entries) != 2 {
		t.Fatalf("%d entries, want the start and the end", len(entries))
	}

	for _, e := range entries {
		if e.Subject != "admin" || e.Action != "installation:store-check" || e.Namespace != audit.ServerScope {
			t.Fatalf("entry = %+v", e)
		}
	}

	if got := fmt.Sprint(entries[1].Changes); !strings.Contains(got, "passed") || !strings.Contains(got, "blobs") {
		t.Fatalf("end entry changes = %s", got)
	}
}

func TestStartNeedsItsDependencies(t *testing.T) {
	t.Parallel()

	_, err := storecheck.NewService(storecheck.Config{
		Records: nil, Operations: nil, Blobs: nil, Backup: nil, Audit: nil, Admit: nil,
		Limits: storecheck.Limits{MaxObjects: 0, MaxTotalBytes: 0, MaxObjectBytes: 0, Timeout: 0, Cooldown: 0},
		Now:    nil, Owner: "", Lease: 0, Logger: nil,
	})
	if !errors.Is(err, storecheck.ErrMissingDependency) {
		t.Fatalf("err = %v, want ErrMissingDependency", err)
	}
}

func TestRunOnceWithNoWork(t *testing.T) {
	t.Parallel()

	r := newRig(t, nil)

	if err := r.svc.RunOnce(context.Background()); !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("err = %v, want ErrNoWork", err)
	}
}

var errPutRefused = errors.New("put refused")

// TestReaperGuardsTheTargetOfTheRow proves that the prefix reaper deletes on
// the store of the target that the row names, and never on the other store: a
// check of the blob store leaves the backup target alone, also for the exact
// prefix of that check.
func TestReaperGuardsTheTargetOfTheRow(t *testing.T) {
	t.Parallel()

	r := newRig(t, nil)
	id := r.start(t, "blobs")
	r.runOne(t)

	r.backup.seed(id.Prefix()+"x", "backups/bkp-x/manifest.json")
	r.blobs.seed(id.Prefix() + "left")

	_, err := r.svc.ReapPrefix(t.Context(), storecheck.TargetBackup, id, id.Prefix())
	if !errors.Is(err, storecheck.ErrNotFound) {
		t.Fatalf("ReapPrefix on the other target err = %v, want ErrNotFound", err)
	}

	if got := len(r.backup.keys()); got != 2 {
		t.Fatalf("the backup target holds %d objects after the refusal, want 2", got)
	}

	if _, err := r.svc.ReapPrefix(t.Context(), storecheck.TargetBlobs, id, id.Prefix()); err != nil {
		t.Fatalf("ReapPrefix on the target of the row: %v", err)
	}

	if got := len(r.blobs.keys()); got != 0 {
		t.Fatalf("the blob store holds %d objects after the reap, want 0", got)
	}

	if got := len(r.backup.keys()); got != 2 {
		t.Fatalf("the reap on the blob store changed the backup target: %d objects", got)
	}
}

// TestFailingRunsCallTheStoreOnlyUnderTheirPrefix proves that a check whose
// store fails, in each way that the suite meets, still sends every request to
// its own prefix: no put, get, delete or list leaves it.
func TestFailingRunsCallTheStoreOnlyUnderTheirPrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(r *rig)
	}{
		{"every get fails", func(r *rig) { r.blobs.getErr = errAdapter }},
		{"every delete fails", func(r *rig) { r.blobs.deleteErr = errObjectLock }},
		{"every put fails", func(r *rig) { r.blobs.putFail = func(blob.Key) error { return errPutRefused } }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, nil)
			r.blobs.seed("namespaces/ns-x/data", "backups/bkp-x/manifest.json")
			tc.mutate(r)

			id := r.start(t, "blobs")
			r.runOne(t)

			if len(r.blobs.calls) == 0 {
				t.Fatal("the check sent no request")
			}

			for _, call := range r.blobs.calls {
				_, key, _ := strings.Cut(call, " ")
				if !strings.HasPrefix(key, id.Prefix()) {
					t.Fatalf("call %q leaves the prefix %q", call, id.Prefix())
				}
			}

			if got := r.blobs.keys(); len(got) < 2 || got[0] != "backups/bkp-x/manifest.json" {
				t.Fatalf("the objects outside the prefix changed: %v", got)
			}
		})
	}
}

const listCaseName = "list returns the objects under a prefix ordered by key"

func TestListCaseFindsAStoreThatBreaksThePagingRules(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		fault func(req blob.PageRequest, all []blob.Object) blob.Page
	}{
		{"ignores After", func(req blob.PageRequest, all []blob.Object) blob.Page {
			return blob.PageOf(all, blob.PageRequest{Prefix: req.Prefix, After: "", Limit: req.Limit})
		}},
		{"ignores the limit", func(_ blob.PageRequest, all []blob.Object) blob.Page {
			return blob.Page{Objects: all, Next: ""}
		}},
		{"gives a wrong Next", func(req blob.PageRequest, all []blob.Object) blob.Page {
			page := blob.PageOf(all, req)
			if page.Next != "" && len(page.Objects) > 0 {
				page.Next = page.Objects[0].Key
			}

			return page
		}},
		{"gives keys out of order", func(req blob.PageRequest, all []blob.Object) blob.Page {
			page := blob.PageOf(all, req)
			slices.Reverse(page.Objects)

			return page
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, nil)
			r.blobs.pageFault = tc.fault

			id := r.start(t, "blobs")
			r.runOne(t)

			_, report := r.report(t, id)
			if report == nil {
				t.Fatal("no report")
			}

			for _, c := range report.Cases {
				if c.Name == listCaseName && c.Passed {
					t.Fatalf("the list case passed on a store that %s", tc.name)
				}
			}
		})
	}
}

func TestListCasePassesOnAStoreThatFollowsThePagingRules(t *testing.T) {
	t.Parallel()

	r := newRig(t, nil)
	id := r.start(t, "blobs")
	r.runOne(t)

	_, report := r.report(t, id)
	if report == nil {
		t.Fatal("no report")
	}

	for _, c := range report.Cases {
		if !c.Passed {
			t.Errorf("case %q failed: %s", c.Name, c.Message)
		}
	}
}
