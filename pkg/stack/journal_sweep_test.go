package stack_test

import (
	"crypto/sha256"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/pulumijournal"
	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/namespace"
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
			plan: stack.Plan{Kind: stack.KindPreview, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
		},
		{
			name: "a dry run update start",
			plan: stack.Plan{Kind: stack.KindUpdate, DryRun: true, CreatedBy: "", RestoredFrom: 0, GitHead: ""},
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

// TestSweepUpdateBlobsKeepsJournalBlobs checks that the sweep of one update,
// which a checkpoint save of the update starts, keeps an old blob that only a
// journal row names, and deletes an old blob under the same update prefix that
// no row names. The test fails when the keep set leaves out the journal keys.
func TestSweepUpdateBlobsKeepsJournalBlobs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		batches int
	}{
		{name: "no journal row", batches: 0},
		{name: "one journal row", batches: 1},
		{name: "two journal rows", batches: 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			clk := newClock()
			f := newBuildFixture(t, clk, pulumijournal.NewReplayer(clk.Now, ""))

			plan := stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""}

			u, err := f.svc.Create(t.Context(), f.ref, plan)
			if err != nil {
				t.Fatalf("create update: %v", err)
			}

			started, err := f.svc.Start(t.Context(), f.ref, u.ID)
			if err != nil {
				t.Fatalf("start update: %v", err)
			}

			dir := "namespaces/" + f.nsID.String() + "/pulumi/" + f.stackID.String() + "/" + u.ID.String() + "/"
			named := f.putJournalRows(t, started.Update, dir, tc.batches)

			orphan := blob.Key(dir + "lost-save")
			if putErr := f.blobs.Put(t.Context(), orphan, strings.NewReader("orphan")); putErr != nil {
				t.Fatalf("put orphan blob: %v", putErr)
			}

			f.blobs.ageAll(90 * time.Minute)

			in := stack.CheckpointInput{
				Features: nil, Deployment: f.base, SchemaVersion: 3, Sequence: 0, Invalid: false,
			}
			if saveErr := f.svc.SaveCheckpoint(t.Context(), f.ref, u.ID, started.Token, in); saveErr != nil {
				t.Fatalf("save checkpoint: %v", saveErr)
			}

			f.requireStored(t, named, orphan)
		})
	}
}

// putJournalRows stores n journal rows of u whose blobs sit under dir, and
// returns their keys. The rows come from the fake store: the update has
// journal version 0, which the real stores refuse, so the test drives the
// keep set of the sweep directly.
func (f *buildFixture) putJournalRows(t *testing.T, u stack.Update, dir string, n int) []blob.Key {
	t.Helper()

	keys := make([]blob.Key, 0, n)

	for i := range n {
		key := blob.Key(dir + "journal/" + strconv.Itoa(i+1))
		if err := f.blobs.Put(t.Context(), key, strings.NewReader("batch")); err != nil {
			t.Fatalf("put journal blob: %v", err)
		}

		row := stack.JournalRow{
			Ref:          f.ref,
			Update:       u.ID,
			Lease:        u.Lease.Digest,
			BlobKey:      key,
			SHA256:       [sha256.Size]byte{byte(i + 1)},
			MD5:          [16]byte{byte(i + 1)},
			Size:         5,
			SequenceIDs:  []int64{int64(i + 1)},
			OperationIDs: nil,
			HasRebuild:   false,
			Verify:       func([]int64, bool) (bool, error) { return false, nil },

			AttemptStartedAt: time.Now(),
		}
		fence := namespace.Fence{Namespace: f.ref.Namespace, FailoverVersion: 1}
		if _, err := f.store.PutJournalRow(t.Context(), row, fence); err != nil {
			t.Fatalf("put journal row: %v", err)
		}

		keys = append(keys, key)
	}

	return keys
}
