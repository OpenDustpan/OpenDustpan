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

	// The key is unique in the namespace, so the new start can name another
	// unit. The clear entry must name the unit of the old job, not that one.
	nextUnit, err := identity.UnitObject("opentofu", testNamespace, "other")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	nextUnit = nextUnit.WithUnitID("unt-2")

	j := testJob(nextUnit, clock)
	j.ID = "job-new"
	j.IdempotencyKey = "start-1"

	start := startEntry(nextUnit, clock)
	start.TokenID, start.IDPSubject = "tok-1", "idp-1"
	start.SourceIP, start.UserAgent, start.RequestID = "192.0.2.1", "test-agent", "req-1"
	start.PrincipalKind = audit.PrincipalKindPerson

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

	if got.Object != unit.String() || got.ObjectID != "" {
		t.Errorf("clear entry names object %q with ID %q, want the unit of the old job %q with no object ID",
			got.Object, got.ObjectID, unit.String())
	}

	if got.RequestID != "" {
		t.Errorf("clear entry copies request %q, want none", got.RequestID)
	}

	checkClearEntryActor(t, *got, start)

	if err := got.Validate(); err != nil {
		t.Errorf("clear entry is not valid: %v", err)
	}
}

// checkClearEntryActor checks that the clear entry carries the actor fields of
// the start entry and no other data of the start.
func checkClearEntryActor(t *testing.T, got, start audit.Entry) {
	t.Helper()

	if got.Origin != nil || got.ObjectID != "" || got.RequestID != "" {
		t.Errorf("clear entry copies the start: origin %v, object ID %q, request %q",
			got.Origin, got.ObjectID, got.RequestID)
	}

	for _, f := range []struct {
		name, got, want string
	}{
		{"namespace", string(got.Namespace), string(start.Namespace)},
		{"subject", got.Subject, start.Subject},
		{"IDP subject", got.IDPSubject, start.IDPSubject},
		{"token", got.TokenID, start.TokenID},
		{"source IP", got.SourceIP, start.SourceIP},
		{"user agent", got.UserAgent, start.UserAgent},
		{"channel", string(got.Channel), string(start.Channel)},
		{"principal kind", got.PrincipalKind, start.PrincipalKind},
	} {
		if f.got != f.want {
			t.Errorf("clear entry %s = %q, want the actor of the start %q", f.name, f.got, f.want)
		}
	}
}

// TestCreateKeyRetentionBreakGlassClearEntry proves that the clear of an
// expired key writes its own entry with the actor of the start and none of the
// other data of a break-glass start.
func TestCreateKeyRetentionBreakGlassClearEntry(t *testing.T) {
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

	nextUnit, err := identity.UnitObject("opentofu", testNamespace, "other")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	nextUnit = nextUnit.WithUnitID("unt-2")

	j := testJob(nextUnit, clock)
	j.ID = "job-new"
	j.IdempotencyKey = "start-1"

	origin, err := audit.NewOrigin(audit.OriginArgs{
		Run: "run-1", Session: "", Tool: "", ArgsDigest: "", BreakGlass: true, SecretStage: "", DelegationID: "",
	})
	if err != nil {
		t.Fatalf("origin: %v", err)
	}

	start := startEntry(nextUnit, clock)
	start.Subject = "break-glass-person"
	start.IDPSubject, start.TokenID = "idp-1", "tok-1"
	start.PrincipalKind = audit.PrincipalKindPerson
	start.SourceIP, start.UserAgent, start.Channel = "192.0.2.1", "test-agent", audit.ChannelConsole
	start.RequestID, start.ObjectID = "req-1", "unt-2"
	start.Kind, start.Origin = audit.KindOverride, origin

	if _, _, err := s.Create(t.Context(), j, nil, testFence(), []audit.Entry{start}); err != nil {
		t.Fatalf("Create past the retention: %v", err)
	}

	var cleared []audit.Entry

	for i := range tx.audits {
		if tx.audits[i].Action == jobstore.KeyExpiredAction {
			cleared = append(cleared, tx.audits[i])
		}
	}

	if len(cleared) != 1 {
		t.Fatalf("Create wrote %d job:key-expired entries, want 1", len(cleared))
	}

	got := cleared[0]
	if got.Kind != audit.KindChange || got.Outcome != audit.OutcomeSuccess {
		t.Errorf("clear entry kind %q and outcome %q, want %q and %q",
			got.Kind, got.Outcome, audit.KindChange, audit.OutcomeSuccess)
	}

	checkClearEntryActor(t, got, start)

	if err := got.Validate(); err != nil {
		t.Errorf("clear entry is not valid: %v", err)
	}
}

// TestCreateKeyRetentionClearValidatesEntry proves that an entry of the clear
// that fails validation refuses the start before the key is cleared.
func TestCreateKeyRetentionClearValidatesEntry(t *testing.T) {
	t.Parallel()

	clock := time.UnixMilli(testClockMs).UTC()
	unit := testUnit(t)
	tx := newFakeTx(clock)
	s := jobstore.New(fakeRunner{tx: tx}, idempotency.JobStartRetention())

	// The reason of the clear holds the ID of the old job, so a hidden
	// character in that ID makes the entry of the clear invalid. A row that
	// another version of the server wrote can hold it.
	old := testJob(unit, clock)
	old.ID = "job-old\u200b1"
	old.IdempotencyKey = "start-1"
	old.CreatedAt = clock.Add(-idempotency.MinRetention - time.Millisecond)
	tx.put(t, &old)

	j := testJob(unit, clock)
	j.ID = "job-new"
	j.IdempotencyKey = "start-1"

	_, _, err := s.Create(t.Context(), j, nil, testFence(), []audit.Entry{startEntry(unit, clock)})
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("Create with an invalid clear entry = %v, want ErrInvalidEntry", err)
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
