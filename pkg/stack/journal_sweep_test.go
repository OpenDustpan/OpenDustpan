package stack_test

import (
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/stack"
)

// TestJournalSweepKeepsNamedBlobs checks that the sweep of a start on the stack
// keeps the journal batch blobs that rows still name, however old they are, and
// still deletes an old blob that no row names. The journaled update runs for
// more than the grace period while the start happens, so the sweep sees every
// batch blob as old.
func TestJournalSweepKeepsNamedBlobs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		plan stack.Plan
	}{
		{
			name: "a preview start",
			plan: stack.Plan{Kind: stack.KindPreview, DryRun: false, CreatedBy: "", RestoredFrom: 0},
		},
		{
			name: "a dry run update start",
			plan: stack.Plan{Kind: stack.KindUpdate, DryRun: true, CreatedBy: "", RestoredFrom: 0},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			clk := newClock()
			f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))
			running := f.startJournaled(t)

			named := f.journalKeys(t, running.Update.ID)
			if len(named) != 2 {
				t.Fatalf("journal rows name %d blobs, want 2", len(named))
			}

			// A blob that no row names, in the directory of an update that
			// never saved again.
			orphan := blob.Key(strings.Replace(
				named[0].String(), "/"+running.Update.ID.String()+"/", "/lost-update/", 1,
			))
			if err := f.blobs.Put(t.Context(), orphan, strings.NewReader("orphan")); err != nil {
				t.Fatalf("put orphan blob: %v", err)
			}

			f.runFor(t, running, 64*time.Minute)
			f.startPlan(t, tc.plan)

			f.requireStored(t, named, orphan)

			_, err := f.svc.Complete(t.Context(), f.ref, running.Update.ID, running.Token, stack.StatusSucceeded)
			if err != nil {
				t.Fatalf("complete journaled update: %v", err)
			}
		})
	}
}

// runFor advances the clock by total, with a lease renewal every 4 minutes.
func (f *buildFixture) runFor(t *testing.T, s stack.Started, total time.Duration) {
	t.Helper()

	const step = 4 * time.Minute

	for elapsed := time.Duration(0); elapsed < total; elapsed += step {
		f.clock.Advance(step)

		if _, err := f.svc.Renew(t.Context(), f.ref, s.Update.ID, s.Token, stack.MaxLease); err != nil {
			t.Fatalf("renew: %v", err)
		}
	}
}

// startPlan creates and starts an update of plan on the stack of the fixture.
func (f *buildFixture) startPlan(t *testing.T, plan stack.Plan) {
	t.Helper()

	u, err := f.svc.Create(t.Context(), f.ref, plan)
	if err != nil {
		t.Fatalf("create %s: %v", plan.Kind, err)
	}

	if _, err := f.svc.Start(t.Context(), f.ref, u.ID); err != nil {
		t.Fatalf("start %s: %v", plan.Kind, err)
	}
}

// requireStored fails the test unless every key of named is stored and orphan
// is gone.
func (f *buildFixture) requireStored(t *testing.T, named []blob.Key, orphan blob.Key) {
	t.Helper()

	for _, key := range named {
		if _, err := f.blobs.Get(t.Context(), key); err != nil {
			t.Errorf("journal blobs that rows still name are missing: %v", err)
		}
	}

	if _, err := f.blobs.Get(t.Context(), orphan); err == nil {
		t.Error("orphan blob that no row names is still stored")
	}
}
