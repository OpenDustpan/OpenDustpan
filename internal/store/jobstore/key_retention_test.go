package jobstore_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/jobstore"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/identity"
)

// startEntry returns the audit entry of a start of a job of unit, as the
// service gives it to Create.
func startEntry(unit identity.Object, at time.Time) audit.Entry {
	return audit.Entry{
		At: at, Namespace: testNamespace, Subject: "alice", IDPSubject: "", TokenID: "", Action: "job:start",
		Object: unit.String(), Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "",
		UserAgent: "", Channel: audit.ChannelAPI, Kind: audit.KindChange, Outcome: audit.OutcomeSuccess,
		RequestID: "", ObjectID: "", PrevHash: "", Hash: "", Sequence: 0, FenceVersion: 0, PrincipalKind: "",
		JobID: "", Reason: "", Origin: nil,
	}
}

// TestCreateKeyRetentionAtTheAge proves that a job of exactly the retention
// age still answers the replay, and stores no second job.
func TestCreateKeyRetentionAtTheAge(t *testing.T) {
	t.Parallel()

	clock := time.UnixMilli(testClockMs).UTC()
	unit := testUnit(t)
	tx := newFakeTx(clock)
	s := jobstore.New(fakeRunner{tx: tx}, idempotency.JobStartRetention())

	first := testJob(unit, clock)
	first.ID = "job-first"
	first.IdempotencyKey = "start-1"
	first.CreatedAt = clock.Add(-idempotency.MinRetention)
	tx.put(t, &first)

	j := testJob(unit, clock)
	j.ID = "job-new"
	j.IdempotencyKey = "start-1"

	got, created, err := s.Create(t.Context(), j, nil, testFence(), nil)
	if err != nil || created || got.ID != first.ID {
		t.Fatalf("Create at the retention age = %s, created %v, error %v; want the job %s", got.ID, created,
			err, first.ID)
	}

	if _, ok, getErr := tx.Job(t.Context(), j.ID); getErr != nil || ok {
		t.Fatalf("Create stored job %s: ok %t, error %v; want no job", j.ID, ok, getErr)
	}
}

// TestCreateKeyRetentionPastTheAge proves that a job one millisecond older
// than the retention gives its key to the new job.
func TestCreateKeyRetentionPastTheAge(t *testing.T) {
	t.Parallel()

	clock := time.UnixMilli(testClockMs).UTC()
	unit := testUnit(t)
	tx := newFakeTx(clock)
	s := jobstore.New(fakeRunner{tx: tx}, idempotency.JobStartRetention())

	old := testJob(unit, clock)
	old.ID = "job-old"
	old.IdempotencyKey = "start-1"
	old.CreatedAt = clock.Add(-idempotency.MinRetention - time.Millisecond)
	tx.put(t, &old)

	j := testJob(unit, clock)
	j.ID = "job-new"
	j.IdempotencyKey = "start-1"

	got, created, err := s.Create(t.Context(), j, nil, testFence(), []audit.Entry{startEntry(unit, clock)})
	if err != nil || !created || got.ID != j.ID {
		t.Fatalf("Create past the retention = %s, created %v, error %v; want the job %s", got.ID, created,
			err, j.ID)
	}

	row, ok, getErr := tx.Job(t.Context(), old.ID)
	if getErr != nil || !ok {
		t.Fatalf("get the old job: ok %t, error %v", ok, getErr)
	}

	if row.Key != "" {
		t.Errorf("the old job keeps the key %q, want no key", row.Key)
	}
}

// TestCreateKeyRetentionClearIsAudited proves that the clear of an expired
// key appends its own audit entry, with the ID of the old job, in the
// transaction of the new start.
func TestCreateKeyRetentionClearIsAudited(t *testing.T) {
	t.Parallel()

	clock := time.UnixMilli(testClockMs).UTC()
	unit := testUnit(t)
	tx := newFakeTx(clock)
	s := jobstore.New(fakeRunner{tx: tx}, idempotency.JobStartRetention())

	old := testJob(unit, clock)
	old.ID = "job-old"
	old.IdempotencyKey = "start-1"
	old.CreatedAt = clock.Add(-idempotency.MinRetention - time.Millisecond)
	tx.put(t, &old)

	j := testJob(unit, clock)
	j.ID = "job-new"
	j.IdempotencyKey = "start-1"

	start := startEntry(unit, clock)

	if _, _, err := s.Create(t.Context(), j, nil, testFence(), []audit.Entry{start}); err != nil {
		t.Fatalf("Create past the retention: %v", err)
	}

	if len(tx.audits) != 2 {
		t.Fatalf("Create appended %d audit entries, want 2 (the clear and the start)", len(tx.audits))
	}

	got := &tx.audits[0]
	if got.Action != jobstore.KeyExpiredAction || got.JobID != string(old.ID) || got.Subject != start.Subject ||
		got.Reason == "" {
		t.Errorf("clear entry = action %q, job %q, subject %q, reason %q; want %q, %q, %q and a reason",
			got.Action, got.JobID, got.Subject, got.Reason, jobstore.KeyExpiredAction, old.ID, start.Subject)
	}

	if err := got.Validate(); err != nil {
		t.Errorf("clear entry is not valid: %v", err)
	}
}

// TestCreateKeyRetentionClearNeedsAudit proves that a start that gives no
// audit entry cannot clear an expired key: it gets ErrInvalidEntry, the old
// job keeps the key and no job is stored.
func TestCreateKeyRetentionClearNeedsAudit(t *testing.T) {
	t.Parallel()

	clock := time.UnixMilli(testClockMs).UTC()
	unit := testUnit(t)
	tx := newFakeTx(clock)
	s := jobstore.New(fakeRunner{tx: tx}, idempotency.JobStartRetention())

	old := testJob(unit, clock)
	old.ID = "job-old"
	old.IdempotencyKey = "start-1"
	old.CreatedAt = clock.Add(-idempotency.MinRetention - time.Millisecond)
	tx.put(t, &old)

	j := testJob(unit, clock)
	j.ID = "job-new"
	j.IdempotencyKey = "start-1"

	_, _, err := s.Create(t.Context(), j, nil, testFence(), nil)
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("Create with an expired key and no audit entry = %v, want ErrInvalidEntry", err)
	}

	row, ok, getErr := tx.Job(t.Context(), old.ID)
	if getErr != nil || !ok {
		t.Fatalf("get the old job: ok %t, error %v", ok, getErr)
	}

	if row.Key != old.IdempotencyKey {
		t.Errorf("the old job key = %q, want %q", row.Key, old.IdempotencyKey)
	}

	if len(tx.audits) != 0 {
		t.Errorf("Create appended %d audit entries, want 0", len(tx.audits))
	}

	if _, ok, getErr := tx.Job(t.Context(), j.ID); getErr != nil || ok {
		t.Fatalf("Create stored job %s: ok %t, error %v; want no job", j.ID, ok, getErr)
	}
}

// TestCreateKeyRetentionZeroValue proves that the zero retention refuses a
// start whose key names a job and stores no job.
func TestCreateKeyRetentionZeroValue(t *testing.T) {
	t.Parallel()

	clock := time.UnixMilli(testClockMs).UTC()
	unit := testUnit(t)
	tx := newFakeTx(clock)
	s := jobstore.New(fakeRunner{tx: tx}, idempotency.Retention{})

	stored := testJob(unit, clock)
	stored.ID = "job-old"
	stored.IdempotencyKey = "start-1"
	tx.put(t, &stored)

	j := testJob(unit, clock)
	j.ID = "job-new"
	j.IdempotencyKey = "start-1"

	_, _, err := s.Create(t.Context(), j, nil, testFence(), nil)
	if !errors.Is(err, idempotency.ErrInvalid) {
		t.Fatalf("Create with the zero retention = %v, want ErrInvalid", err)
	}

	if _, ok, getErr := tx.Job(t.Context(), j.ID); getErr != nil || ok {
		t.Fatalf("Create stored job %s: ok %t, error %v; want no job", j.ID, ok, getErr)
	}
}

// TestCreateKeyRetentionLostClear proves that a clear that finds a newer
// version of the old row runs one more attempt, which replays the new job.
func TestCreateKeyRetentionLostClear(t *testing.T) {
	t.Parallel()

	clock := time.UnixMilli(testClockMs).UTC()
	unit := testUnit(t)
	tx := newFakeTx(clock)
	s := jobstore.New(fakeRunner{tx: tx}, idempotency.JobStartRetention())

	expired := testJob(unit, clock)
	expired.ID = "job-old"
	expired.IdempotencyKey = "start-1"
	expired.CreatedAt = clock.Add(-25 * time.Hour)
	expired.Version = 5
	tx.put(t, &expired)
	stale := tx.rows[expired.ID]

	// The other writer cleared the key of the old row and inserted the
	// new job. The read of this attempt still sees the old row.
	cleared := expired
	cleared.IdempotencyKey = ""
	cleared.Version = 6
	tx.put(t, &cleared)

	next := testJob(unit, clock)
	next.ID = "job-new"
	next.IdempotencyKey = "start-1"
	next.CreatedAt = clock
	tx.put(t, &next)

	tx.keyRead = &stale

	j := testJob(unit, clock)
	j.ID = "job-later"
	j.IdempotencyKey = "start-1"

	got, created, err := s.Create(t.Context(), j, nil, testFence(), []audit.Entry{startEntry(unit, clock)})
	if err != nil || created || got.ID != next.ID {
		t.Fatalf("Create after the lost clear = %s, created %v, error %v; want the job %s", got.ID, created,
			err, next.ID)
	}

	if _, ok, getErr := tx.Job(t.Context(), j.ID); getErr != nil || ok {
		t.Fatalf("Create stored job %s: ok %t, error %v; want no job", j.ID, ok, getErr)
	}
}
