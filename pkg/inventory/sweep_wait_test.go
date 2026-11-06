package inventory_test

import (
	"fmt"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/backup/blobguard"
	"opendustpan.org/opendustpan/pkg/inventory"
)

// waitFixture returns a sweep fixture with one blob purge record older than
// the grace, whose blob delete returns err.
func waitFixture(t *testing.T, err error) sweepFixture {
	t.Helper()

	old := sweepNow().Add(-inventory.BlobPurgeGrace - 1)
	f := newSweepFixture(t, nil, []inventory.BlobPurge{
		{PurgedAt: old, NamespaceID: "ns-acme", Tool: "pulumi", UnitID: "stk-gone"},
	})
	f.tool.blobErrs["stk-gone"] = fmt.Errorf("delete blobs: %w", err)

	return f
}

// sweepOnce runs one sweep and fails the test on an error.
func sweepOnce(t *testing.T, f sweepFixture) inventory.SweepResult {
	t.Helper()

	res, err := f.sweeper.Sweep(t.Context(), sweepEntry())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}

	return res
}

// requireWaiting checks that res shows one pending record that waits for
// work: no failure, no finished record, an INFO line that names the work.
func requireWaiting(t *testing.T, f sweepFixture, res inventory.SweepResult, work blobguard.Work) {
	t.Helper()

	if res.Failed != 0 || res.PendingBlobPurges != 1 || res.WaitingBlobPurges != 1 ||
		res.WaitingFor != work.String() || res.FinishedBlobPurges != 0 {
		t.Fatalf("sweep = %+v, want one pending record that waits for %q and no failure", res, work)
	}

	if len(f.records.finished) != 0 {
		t.Fatalf("finished = %v, want none while %s runs", f.records.finished, work)
	}

	log := f.log.String()
	if !strings.Contains(log, work.String()) || strings.Contains(log, "level=WARN") {
		t.Fatalf("log = %q, want an INFO line that names %q and no warning", log, work)
	}
}

// TestSweepWaitsForRunningWork proves that a blob purge record which is older
// than the grace stays pending while the tool reports a running backup or
// restore: the sweep counts it as waiting, names the work, counts no failure,
// and finishes the record after the work ended.
func TestSweepWaitsForRunningWork(t *testing.T) {
	t.Parallel()

	for name, work := range map[string]blobguard.Work{
		"a backup":  {Kind: blobguard.Backup, ID: "bkp-1"},
		"a restore": {Kind: blobguard.Restore, ID: "rst-1"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := waitFixture(t, &blobguard.WaitError{Work: work})

			requireWaiting(t, f, sweepOnce(t, f), work)

			// The work ended: the next sweep deletes and finishes.
			delete(f.tool.blobErrs, "stk-gone")

			res := sweepOnce(t, f)
			if res.FinishedBlobPurges != 1 || res.PendingBlobPurges != 0 || res.WaitingBlobPurges != 0 ||
				res.Failed != 0 {
				t.Fatalf("second sweep = %+v, want the record finished", res)
			}
		})
	}
}

// TestSweepCountsAGuardFailureAsAFailure proves that every outcome of the
// guard except "work runs" is a failure, not a wait: a failed read, an answer
// with an unknown kind and a tool with no guard. The record stays pending.
func TestSweepCountsAGuardFailureAsAFailure(t *testing.T) {
	t.Parallel()

	for name, guardErr := range map[string]error{
		"a failed read":  fmt.Errorf("read running backup or restore: %w", errDriver),
		"an unknown one": blobguard.ErrUnknown,
		"no guard":       blobguard.ErrNoWatch,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			f := waitFixture(t, guardErr)

			res := sweepOnce(t, f)

			if res.Failed != 1 || res.PendingBlobPurges != 1 || res.WaitingBlobPurges != 0 || res.WaitingFor != "" {
				t.Fatalf("sweep = %+v, want one failure and one pending record that does not wait", res)
			}

			if !strings.Contains(f.log.String(), "error_kind=store") || strings.Contains(f.log.String(), "10.0.0.7") {
				t.Fatalf("log = %q, want a warning with an error kind and no driver text", f.log.String())
			}
		})
	}
}
