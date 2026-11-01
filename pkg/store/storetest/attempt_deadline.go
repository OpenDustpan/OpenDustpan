package storetest

import (
	"errors"
	"slices"
	"strconv"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/blob"
	"opendustpan.org/opendustpan/pkg/stack"
)

// attemptWindowCase is one start time of a write attempt and the answer that
// the store gives for its commit, by the clock of the database.
type attemptWindowCase struct {
	name    string
	started func() time.Time
	want    error
}

// attemptWindowCases returns the start times that the store must refuse, and
// one that it must accept. A margin of one minute keeps each case clear of the
// edge of the window, so a slow test run does not change the answer.
func attemptWindowCases() []attemptWindowCase {
	const margin = time.Minute

	return []attemptWindowCase{
		{name: "a fresh attempt", started: time.Now, want: nil},
		{
			name:    "an attempt just past the deadline",
			started: func() time.Time { return time.Now().Add(-blob.AttemptDeadline - margin) },
			want:    blob.ErrAttemptExpired,
		},
		{
			name:    "an attempt older than the grace period of the sweep",
			started: func() time.Time { return time.Now().Add(-blob.UnnamedGracePeriod - margin) },
			want:    blob.ErrAttemptExpired,
		},
		{
			name:    "an attempt ahead of the database clock by more than the skew",
			started: func() time.Time { return time.Now().Add(blob.AttemptSkew + margin) },
			want:    blob.ErrAttemptExpired,
		},
		{
			name:    "an attempt with no start time",
			started: func() time.Time { return time.Time{} },
			want:    blob.ErrAttemptExpired,
		},
	}
}

// testCheckpointAttemptWindow checks that the store refuses the checkpoint of
// an attempt outside its time window, and that the refusal writes no row: no
// row names the blob of the attempt, so the sweep may delete it. Each case ends
// its update, so the next case can start one on the same stack.
func testCheckpointAttemptWindow(t *testing.T, f fixture) {
	t.Helper()

	for i, tc := range attemptWindowCases() {
		u := f.run(t, f.ref, "u"+strconv.Itoa(i), mutating())
		checkpointAttemptCase(t, f, u, tc)
		f.end(t, u, stack.StatusFailed)
	}
}

// checkpointAttemptCase runs one case of testCheckpointAttemptWindow on the
// running update u.
func checkpointAttemptCase(t *testing.T, f fixture, u stack.Update, tc attemptWindowCase) {
	t.Helper()

	cp := f.checkpoint(t, u, 1, `{"n":1}`)
	cp.AttemptStartedAt = tc.started()

	known, err := f.s.PutCheckpoint(t.Context(), cp, f.fence)
	if known || !errors.Is(err, tc.want) || (tc.want == nil) != (err == nil) {
		t.Errorf("%s: known = %v, err = %v; want false, %v", tc.name, known, err, tc.want)

		return
	}

	_, readErr := f.s.CheckpointByUpdate(t.Context(), f.ref, u.ID)

	switch {
	case tc.want == nil && readErr != nil:
		t.Errorf("%s: the accepted checkpoint has no row: %v", tc.name, readErr)
	case tc.want != nil && !errors.Is(readErr, stack.ErrNoState):
		t.Errorf("%s: checkpoint by update after the refusal: err = %v, want ErrNoState", tc.name, readErr)
	default:
	}

	if tc.want != nil && keyNamed(t, f, cp.BlobKey) {
		t.Errorf("%s: a row names the blob of the refused attempt", tc.name)
	}
}

// testCheckpointReplayAfterDeadline checks that a replay of a committed
// checkpoint is refused when its attempt is past the deadline, and that the
// row keeps the key of the first write.
func testCheckpointReplayAfterDeadline(t *testing.T, f fixture) {
	t.Helper()

	u := f.run(t, f.ref, "u1", mutating())
	first := f.attemptCheckpoint(t, u, 2, `{"n":2}`, "attempt0001")

	if _, err := f.s.PutCheckpoint(t.Context(), first, f.fence); err != nil {
		t.Fatalf("first write: %v", err)
	}

	late := f.attemptCheckpoint(t, u, 2, `{"n":2}`, "attempt0002")
	late.AttemptStartedAt = time.Now().Add(-blob.UnnamedGracePeriod)

	known, err := f.s.PutCheckpoint(t.Context(), late, f.fence)
	if known || !errors.Is(err, blob.ErrAttemptExpired) {
		t.Fatalf("late replay: known = %v, err = %v; want false, ErrAttemptExpired", known, err)
	}

	assertCheckpoint(t, f, f.ref, first)
}

// testBuiltCheckpointAttemptWindow checks that the store refuses a built
// checkpoint of an attempt past its deadline, and that the refusal keeps the
// journal of the update.
func testBuiltCheckpointAttemptWindow(t *testing.T, f fixture) {
	t.Helper()

	u := f.runJournaled(t, f.ref, "u1")

	batch := journalRow(t, f.s, u, 1, 1)
	if _, err := f.s.PutJournalRow(t.Context(), batch, f.fence); err != nil {
		t.Fatalf("journal batch: %v", err)
	}

	cp := f.checkpoint(t, u, 0, "{}")
	cp.AttemptStartedAt = time.Now().Add(-blob.UnnamedGracePeriod)

	removed, err := f.s.PutBuiltCheckpoint(t.Context(), cp, f.fence)
	if !errors.Is(err, blob.ErrAttemptExpired) || len(removed) != 0 {
		t.Fatalf("late build: keys %v, err %v; want no keys, ErrAttemptExpired", removed, err)
	}

	if _, readErr := f.s.CheckpointByUpdate(t.Context(), f.ref, u.ID); !errors.Is(readErr, stack.ErrNoState) {
		t.Fatalf("checkpoint after the refused build: err = %v, want ErrNoState", readErr)
	}

	keys, err := f.s.JournalKeys(t.Context(), mustStackID(t, f.s, f.ref), u.ID)
	if err != nil || len(keys) != 1 || keys[0] != batch.BlobKey {
		t.Fatalf("journal after the refused build = %v, %v; want the one batch", keys, err)
	}
}

// testJournalAttemptWindow checks that the store refuses the row of a journal
// batch of an attempt outside its time window, also for a retry of a stored
// batch, and that the refusal stores no row that names the blob. Each case ends
// its update, so the next case can start one on the same stack.
func testJournalAttemptWindow(t *testing.T, f fixture) {
	t.Helper()

	for i, tc := range attemptWindowCases() {
		u := f.runJournaled(t, f.ref, "u"+strconv.Itoa(i))
		journalAttemptCase(t, f, u, tc)
		f.end(t, u, stack.StatusFailed)
	}
}

// journalAttemptCase runs one case of testJournalAttemptWindow on the running
// journaled update u.
func journalAttemptCase(t *testing.T, f fixture, u stack.Update, tc attemptWindowCase) {
	t.Helper()

	row := journalRow(t, f.s, u, 1, 1)
	row.AttemptStartedAt = tc.started()

	retried, err := f.s.PutJournalRow(t.Context(), row, f.fence)
	if retried || !errors.Is(err, tc.want) || (tc.want == nil) != (err == nil) {
		t.Errorf("%s: retried = %v, err = %v; want false, %v", tc.name, retried, err, tc.want)

		return
	}

	keys, err := f.s.JournalKeys(t.Context(), mustStackID(t, f.s, f.ref), u.ID)
	if err != nil {
		t.Fatalf("%s: journal keys: %v", tc.name, err)
	}

	if tc.want != nil {
		if len(keys) != 0 {
			t.Errorf("%s: journal keys after the refusal = %v, want none", tc.name, keys)
		}

		return
	}

	if len(keys) != 1 {
		t.Errorf("%s: journal keys = %v, want the one batch", tc.name, keys)
	}

	// A retry of the stored batch past the deadline is refused too.
	late := journalRowAttempt(t, f.s, u, journalWrite{sha: 1, attempt: "attempt0009", seqs: []int64{1}})
	late.AttemptStartedAt = time.Now().Add(-blob.UnnamedGracePeriod)

	if again, lateErr := f.s.PutJournalRow(t.Context(), late, f.fence); again ||
		!errors.Is(lateErr, blob.ErrAttemptExpired) {
		t.Errorf("%s: late retry: retried = %v, err = %v; want false, ErrAttemptExpired", tc.name, again, lateErr)
	}
}

// keyNamed reports whether a row of the stack of f names key.
func keyNamed(t *testing.T, f fixture, key blob.Key) bool {
	t.Helper()

	named, err := f.s.ReferencedBlobKeys(t.Context(), mustStackID(t, f.s, f.ref))
	if err != nil {
		t.Fatalf("referenced blob keys: %v", err)
	}

	return slices.Contains(named, key)
}
