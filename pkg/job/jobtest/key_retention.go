package jobtest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/job"
)

// keyExpiredAction is the action of the audit entry of a clear of an expired
// job-start key.
const keyExpiredAction = "job:key-expired"

// keyRetentionCases returns the cases of the retention of a job-start key: a
// key names its job for MinRetention from the creation of the job. Each case
// needs a harness with AgeJob.
func keyRetentionCases() []suiteCase {
	return []suiteCase{
		{"a key of a job inside the retention age replays", testKeyInsideRetentionReplays},
		{
			"a key of a job older than the retention starts a new job that holds the key",
			testKeyPastRetentionStartsNewJob,
		},
		{"a key clear writes a job:key-expired entry that names the old job", testKeyClearAudits},
		{"another request under an expired key starts a new job", testExpiredKeyWithAnotherRequestStarts},
		{"a retry after the new job replays the new job", testRetryAfterNewJobReplays},
		{"a failed start under an expired key keeps the old key and writes no entry", testFailedExpiredStartKeepsKey},
	}
}

// requireAgeJob skips a case whose harness cannot move the creation time of a
// stored job.
func requireAgeJob(t *testing.T, h Harness) {
	t.Helper()

	if h.AgeJob == nil {
		t.Skip("the harness cannot move the creation time of a job")
	}
}

// testKeyInsideRetentionReplays proves that a job inside the retention age
// still answers the start with its key. The store reads the database clock, and
// the clock moves between the age and the start, so the case uses an age with
// a margin. The exact end of the retention is a case of Retention.Expired.
func testKeyInsideRetentionReplays(t *testing.T, h Harness) {
	t.Helper()

	requireAgeJob(t, h)
	f := setUp(t, h)
	first := create(t, h, f, newJob(f.unit, job.OperationPlan, "start-retention"), nil)

	h.AgeJob(t, first.ID, idempotency.MinRetention-time.Hour)

	again, created, err := h.Jobs.Create(t.Context(), *newJob(f.unit, job.OperationPlan, "start-retention"), nil,
		f.fence, []audit.Entry{entry(f.unit, job.ActionUnitRunPlan)})
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("Create at the retention age = %s, created %v, error %v; want the first job %s", again.ID, created,
			err, first.ID)
	}
}

// testKeyPastRetentionStartsNewJob proves that a key of an older job starts a
// new job, which holds the key, and that the old job loses it.
func testKeyPastRetentionStartsNewJob(t *testing.T, h Harness) {
	t.Helper()

	requireAgeJob(t, h)
	f := setUp(t, h)
	old := create(t, h, f, newJob(f.unit, job.OperationPlan, "start-expired"), nil)

	h.AgeJob(t, old.ID, idempotency.MinRetention+time.Millisecond)

	next, created, err := h.Jobs.Create(t.Context(), *newJob(f.unit, job.OperationPlan, "start-expired"), nil,
		f.fence, []audit.Entry{entry(f.unit, job.ActionUnitRunPlan)})
	if err != nil || !created || next.ID == old.ID {
		t.Fatalf("Create past the retention = %s, created %v, error %v; want a new job", next.ID, created, err)
	}

	if next.IdempotencyKey != "start-expired" {
		t.Fatalf("the new job holds key %q, want start-expired", next.IdempotencyKey)
	}

	got, err := h.Jobs.Get(t.Context(), old.ID)
	if err != nil {
		t.Fatalf("Get the old job: %v", err)
	}

	if got.IdempotencyKey != "" {
		t.Fatalf("the old job holds key %q, want no key", got.IdempotencyKey)
	}
}

// testKeyClearAudits proves that the clear of an expired key appends a
// job:key-expired entry that names the old job, in the transaction of the
// start.
func testKeyClearAudits(t *testing.T, h Harness) {
	t.Helper()

	requireAgeJob(t, h)
	f := setUp(t, h)
	old := create(t, h, f, newJob(f.unit, job.OperationPlan, "start-entry"), nil)

	h.AgeJob(t, old.ID, idempotency.MinRetention+time.Millisecond)

	next := create(t, h, f, newJob(f.unit, job.OperationPlan, "start-entry"), nil)
	if next.ID == old.ID {
		t.Fatal("the expired start answered the old job")
	}

	var cleared *audit.Entry

	entries := allEntries(t, h, f)
	for i := range entries {
		if entries[i].Action == keyExpiredAction {
			cleared = &entries[i]

			break
		}
	}

	if cleared == nil {
		t.Fatalf("the clear of the key of job %s wrote no job:key-expired entry", old.ID)
	}

	if cleared.JobID != string(old.ID) {
		t.Fatalf("the job:key-expired entry names job %q, want %q", cleared.JobID, old.ID)
	}
}

// testExpiredKeyWithAnotherRequestStarts proves that the store compares an
// expired key with no request, so another body starts a new job.
func testExpiredKeyWithAnotherRequestStarts(t *testing.T, h Harness) {
	t.Helper()

	requireAgeJob(t, h)
	f := setUp(t, h)
	old := create(t, h, f, newJob(f.unit, job.OperationPlan, "start-other"), nil)

	h.AgeJob(t, old.ID, idempotency.MinRetention+time.Millisecond)

	next, created, err := h.Jobs.Create(t.Context(), *newJob(f.unit, job.OperationApply, "start-other"), nil,
		f.fence, []audit.Entry{entry(f.unit, job.ActionUnitRunApply)})
	if err != nil || !created || next.ID == old.ID {
		t.Fatalf("Create with another request = %s, created %v, error %v; want a new job", next.ID, created, err)
	}
}

// testRetryAfterNewJobReplays proves that a key that an expired start took
// names the new job for its own retention.
func testRetryAfterNewJobReplays(t *testing.T, h Harness) {
	t.Helper()

	requireAgeJob(t, h)
	f := setUp(t, h)
	old := create(t, h, f, newJob(f.unit, job.OperationPlan, "start-again"), nil)

	h.AgeJob(t, old.ID, idempotency.MinRetention+time.Millisecond)

	next := create(t, h, f, newJob(f.unit, job.OperationPlan, "start-again"), nil)
	if next.ID == old.ID {
		t.Fatal("the expired start answered the old job")
	}

	again, created, err := h.Jobs.Create(t.Context(), *newJob(f.unit, job.OperationPlan, "start-again"), nil,
		f.fence, []audit.Entry{entry(f.unit, job.ActionUnitRunPlan)})
	if err != nil || created || again.ID != next.ID {
		t.Fatalf("the retry after the new job = %s, created %v, error %v; want the new job %s", again.ID, created,
			err, next.ID)
	}
}

// testFailedExpiredStartKeepsKey proves that a start that fails after the
// clear rolls back, so the old job keeps its key.
func testFailedExpiredStartKeepsKey(t *testing.T, h Harness) {
	t.Helper()

	requireAgeJob(t, h)
	f := setUp(t, h)
	old := create(t, h, f, newJob(f.unit, job.OperationPlan, "start-busy"), nil)

	h.AgeJob(t, old.ID, idempotency.MinRetention+time.Millisecond)

	// The apply job holds the unit for a change, so the next dispatch is busy.
	runApplyPhase(t, h, f)

	j := newJob(f.unit, job.OperationRefresh, "start-busy")

	_, _, err := h.Jobs.Create(t.Context(), *j, dispatchOf(j), f.fence,
		[]audit.Entry{entry(f.unit, job.ActionUnitRunApply)})
	if !errors.Is(err, job.ErrUnitBusy) {
		t.Fatalf("Create under an expired key on a busy unit = %v, want ErrUnitBusy", err)
	}

	got, err := h.Jobs.Get(t.Context(), old.ID)
	if err != nil {
		t.Fatalf("Get the old job: %v", err)
	}

	if got.IdempotencyKey != "start-busy" {
		t.Fatalf("the old job holds key %q after the rollback, want start-busy", got.IdempotencyKey)
	}

	entries := allEntries(t, h, f)
	for i := range entries {
		if entries[i].Action == keyExpiredAction {
			t.Fatal("the failed start left a job:key-expired entry")
		}
	}
}
