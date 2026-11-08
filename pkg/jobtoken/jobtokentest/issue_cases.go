package jobtokentest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/jobtoken"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func issueCases() []suiteCase {
	return []suiteCase{
		{name: "issue on a live claim succeeds", test: testLiveClaim},
		{name: "issue then read gives the record and the live claim", test: testRoundTrip},
		{name: "issue on a claim lease that ends at the issue time returns not live", test: testLeaseAtNow},
		{
			name: "issue on a claim lease that ends one millisecond after the issue time succeeds",
			test: testLeaseAfterNow,
		},
		{name: "issue after a takeover returns not live", test: testTakeover},
		{name: "issue after the claim ended returns not live", test: testEndedClaim},
		{name: "issue after the claim was released returns not live", test: testReleasedClaim},
		{name: "issue on an ended job returns not live", test: testEndedJob},
		{name: "issue on a queued job returns not live", test: testQueuedJob},
		{name: "issue after the job moved to another operation returns not live", test: testMovedJob},
		{name: "issue of a hard-expired token returns invalid", test: testHardExpired},
		{name: "issue of a token with a lifetime above the maximum returns invalid", test: testLifetimeTooLong},
		{name: "issue for another unit than the job unit returns not live", test: testUnitMismatch},
		{name: "issue with a disabled starter returns not live", test: testDisabledStarter},
		{name: "issue with a deleted starter returns not live", test: testDeletedStarter},
		{name: "issue with a starter that is not the job starter returns invalid", test: testStarterMismatch},
		{name: "issue for a job that does not exist returns not found", test: testMissingJob},
		{name: "issue for a claim operation that does not exist returns not found", test: testMissingClaim},
		{name: "issue with an invalid token returns invalid", test: testInvalidToken},
		{
			name: "issue with a fence of another namespace returns fenced and writes nothing",
			test: testFenceOtherNamespace,
		},
		{
			name: "issue and read of a unit with the same name in another namespace keep the unit ID of the token",
			test: testSameNameUnit,
		},
		{name: "issue then rename of a unit keeps the token of the unit ID", test: testRenamedUnit},
		{name: "issue with a stale fence returns fenced and writes nothing", test: testStaleFence},
		{name: "issue with a refused audit entry writes nothing", test: testRefusedEntry},
		{name: "issue with an audit entry of another namespace writes nothing", test: testEntryOtherNamespace},
	}
}

func testLiveClaim(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-live")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	mustIssue(t, h, tok, fence)
}

func testRoundTrip(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-round-trip")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	mustIssue(t, h, tok, fence)

	m := mustRead(t, h, tok.Digest)
	if m.Token != tok {
		t.Fatalf("read token = %+v, want %+v", m.Token, tok)
	}

	want := jobtoken.Claim{
		OperationState: durable.StateRunning, LeaseEndsAt: op.LeaseEnd, JobID: j.ID, JobState: job.StateRunning,
		JobOperation: op.ID, JobUnitID: unit.UnitID(), JobStarterID: starter.ID, Starter: starter.Principal,
		StarterRecordID: starter.ID, OperationClaimToken: op.Token, StarterEnabled: true,
	}
	if m.Claim != want {
		t.Fatalf("read claim = %+v, want %+v", m.Claim, want)
	}

	if got := len(h.Audit(t, fence.Namespace)); got != 1 {
		t.Fatalf("audit entries = %d, want 1", got)
	}
}

func testLeaseAtNow(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-lease-now")
	op.LeaseEnd = issueTime()
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	if err := tryIssue(t, h, tok, fence); !errors.Is(err, jobtoken.ErrNotLive) {
		t.Fatalf("Issue = %v, want ErrNotLive", err)
	}

	wantNothing(t, h, tok.Digest, fence.Namespace)
}

func testLeaseAfterNow(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-lease-after")
	op.LeaseEnd = issueTime().Add(oneMillisecond)
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	mustIssue(t, h, tok, fence)
}

func testTakeover(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-takeover")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	mustIssue(t, h, tok, fence)
	leaseEnd := h.Takeover(t, j.ID)

	next := liveToken(t, unit, starter, j.ID, op)
	if err := tryIssue(t, h, next, fence); !errors.Is(err, jobtoken.ErrNotLive) {
		t.Fatalf("Issue after a takeover = %v, want ErrNotLive", err)
	}

	m := mustRead(t, h, tok.Digest)

	want := jobtoken.Claim{
		OperationState: durable.StateRunning, LeaseEndsAt: leaseEnd, JobID: j.ID, JobState: job.StateRunning,
		JobOperation: op.ID, JobUnitID: unit.UnitID(), JobStarterID: starter.ID, Starter: starter.Principal,
		StarterRecordID: starter.ID, OperationClaimToken: op.Token + 1, StarterEnabled: true,
	}
	if m.Claim != want {
		t.Fatalf("read claim = %+v, want %+v", m.Claim, want)
	}
}

func testEndedClaim(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-ended-claim")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	mustIssue(t, h, tok, fence)
	h.EndClaim(t, j.ID)

	next := liveToken(t, unit, starter, j.ID, op)
	if err := tryIssue(t, h, next, fence); !errors.Is(err, jobtoken.ErrNotLive) {
		t.Fatalf("Issue after the claim ended = %v, want ErrNotLive", err)
	}

	if m := mustRead(t, h, tok.Digest); m.Claim.OperationState != durable.StateSucceeded {
		t.Fatalf("operation state = %q, want %q", m.Claim.OperationState, durable.StateSucceeded)
	}
}

func testReleasedClaim(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-released")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	h.ReleaseClaim(t, j.ID)

	if err := tryIssue(t, h, tok, fence); !errors.Is(err, jobtoken.ErrNotLive) {
		t.Fatalf("Issue after a release = %v, want ErrNotLive", err)
	}

	wantNothing(t, h, tok.Digest, fence.Namespace)
}

func testEndedJob(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-ended-job")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	mustIssue(t, h, tok, fence)
	h.EndJob(t, j.ID)

	next := liveToken(t, unit, starter, j.ID, op)
	if err := tryIssue(t, h, next, fence); !errors.Is(err, jobtoken.ErrNotLive) {
		t.Fatalf("Issue on an ended job = %v, want ErrNotLive", err)
	}

	if m := mustRead(t, h, tok.Digest); !m.Claim.JobState.Terminal() {
		t.Fatalf("job state = %q, want a terminal state", m.Claim.JobState)
	}
}

func testQueuedJob(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-queued-job")
	j := h.Job(t, unit, starter, op)

	h.SetJobState(t, j.ID, job.StateQueued)

	tok := liveToken(t, unit, starter, j.ID, op)
	if err := tryIssue(t, h, tok, fence); !errors.Is(err, jobtoken.ErrNotLive) {
		t.Fatalf("Issue on a queued job = %v, want ErrNotLive", err)
	}

	wantNothing(t, h, tok.Digest, fence.Namespace)
}

func testMovedJob(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-moved-from")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	mustIssue(t, h, tok, fence)
	h.MoveJob(t, j.ID, "op-moved-to")

	next := liveToken(t, unit, starter, j.ID, op)
	if err := tryIssue(t, h, next, fence); !errors.Is(err, jobtoken.ErrNotLive) {
		t.Fatalf("Issue after a move = %v, want ErrNotLive", err)
	}

	if m := mustRead(t, h, tok.Digest); m.Claim.JobOperation != "op-moved-to" {
		t.Fatalf("job operation = %q, want %q", m.Claim.JobOperation, "op-moved-to")
	}
}

func testHardExpired(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-expired")
	j := h.Job(t, unit, starter, op)
	tok := tokenFor(t, &tokenSpec{Unit: unit, Starter: starter, JobID: j.ID, Operation: op, ExpiresAt: issueTime()})
	tok.CreatedAt = issueTime().Add(-time.Minute)

	if err := tryIssue(t, h, tok, fence); !errors.Is(err, jobtoken.ErrInvalid) {
		t.Fatalf("Issue of a hard-expired token = %v, want ErrInvalid", err)
	}

	wantNothing(t, h, tok.Digest, fence.Namespace)
}

func testLifetimeTooLong(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-lifetime")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)
	tok.ExpiresAt = tok.CreatedAt.Add(jobtoken.MaxLifetime + oneMillisecond)

	if err := tryIssue(t, h, tok, fence); !errors.Is(err, jobtoken.ErrInvalid) {
		t.Fatalf("Issue of a token with a long lifetime = %v, want ErrInvalid", err)
	}

	wantNothing(t, h, tok.Digest, fence.Namespace)
}

func testUnitMismatch(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	jobUnit := h.Unit(t, "acme", "pulumi", "web/prod")
	other := h.Unit(t, "acme", "pulumi", "web/staging")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-unit-mismatch")
	j := h.Job(t, jobUnit, starter, op)
	tok := liveToken(t, other, starter, j.ID, op)

	if err := tryIssue(t, h, tok, fence); !errors.Is(err, jobtoken.ErrNotLive) {
		t.Fatalf("Issue for another unit = %v, want ErrNotLive", err)
	}

	wantNothing(t, h, tok.Digest, fence.Namespace)
}

func testDisabledStarter(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-disabled")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	mustIssue(t, h, tok, fence)
	h.Disable(t, starter)

	next := liveToken(t, unit, starter, j.ID, op)
	if err := tryIssue(t, h, next, fence); !errors.Is(err, jobtoken.ErrNotLive) {
		t.Fatalf("Issue with a disabled starter = %v, want ErrNotLive", err)
	}

	if m := mustRead(t, h, tok.Digest); m.Claim.StarterEnabled {
		t.Fatal("read claim marks a disabled starter as enabled")
	}
}

func testDeletedStarter(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-deleted")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	mustIssue(t, h, tok, fence)
	h.Delete(t, starter)

	if _, err := h.Store.GetByDigest(t.Context(), tok.Digest); !errors.Is(err, jobtoken.ErrNotFound) {
		t.Fatalf("GetByDigest with a deleted starter = %v, want ErrNotFound", err)
	}

	next := liveToken(t, unit, starter, j.ID, op)
	if err := tryIssue(t, h, next, fence); !errors.Is(err, jobtoken.ErrNotLive) {
		t.Fatalf("Issue with a deleted starter = %v, want ErrNotLive", err)
	}
}

func testStarterMismatch(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	alice := h.Principal(t, "alice")
	bob := h.Principal(t, "bob")
	op := liveOperation("op-starter-mismatch")
	j := h.Job(t, unit, alice, op)
	tok := liveToken(t, unit, bob, j.ID, op)

	if err := tryIssue(t, h, tok, fence); !errors.Is(err, jobtoken.ErrInvalid) {
		t.Fatalf("Issue with another starter = %v, want ErrInvalid", err)
	}

	wantNothing(t, h, tok.Digest, fence.Namespace)
}

func testMissingJob(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-missing-job")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)
	tok.JobID = "job-missing"

	if err := tryIssue(t, h, tok, fence); !errors.Is(err, jobtoken.ErrNotFound) {
		t.Fatalf("Issue for a missing job = %v, want ErrNotFound", err)
	}

	wantNothing(t, h, tok.Digest, fence.Namespace)
}

func testMissingClaim(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-missing-claim")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)
	tok.ClaimOperation = "op-missing"

	if err := tryIssue(t, h, tok, fence); !errors.Is(err, jobtoken.ErrNotFound) {
		t.Fatalf("Issue for a missing claim = %v, want ErrNotFound", err)
	}

	wantNothing(t, h, tok.Digest, fence.Namespace)
}

func testInvalidToken(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	h.Namespace(t, "beta")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	otherUnit := h.Unit(t, "beta", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-invalid")
	j := h.Job(t, unit, starter, op)

	for _, tc := range []struct {
		name   string
		mutate func(*jobtoken.Token)
	}{
		{name: "no digest", mutate: func(tok *jobtoken.Token) { tok.Digest = jobtoken.Digest{} }},
		{name: "no starter", mutate: func(tok *jobtoken.Token) { tok.StarterID = "" }},
		{name: "no job", mutate: func(tok *jobtoken.Token) { tok.JobID = "" }},
		{name: "no namespace", mutate: func(tok *jobtoken.Token) { tok.Namespace = "" }},
		{name: "no unit ID", mutate: func(tok *jobtoken.Token) { tok.Unit = unit.WithUnitID("") }},
		{name: "unit in another namespace", mutate: func(tok *jobtoken.Token) { tok.Unit = otherUnit }},
		{name: "no claim operation", mutate: func(tok *jobtoken.Token) { tok.ClaimOperation = "" }},
		{name: "no claim token", mutate: func(tok *jobtoken.Token) { tok.ClaimToken = 0 }},
		{name: "a negative claim token", mutate: func(tok *jobtoken.Token) { tok.ClaimToken = -1 }},
		{name: "no creation time", mutate: func(tok *jobtoken.Token) { tok.CreatedAt = time.Time{} }},
		{name: "expiry at the creation time", mutate: func(tok *jobtoken.Token) { tok.ExpiresAt = tok.CreatedAt }},
		{name: "expiry before the creation time", mutate: func(tok *jobtoken.Token) {
			tok.ExpiresAt = tok.CreatedAt.Add(-time.Hour)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			tok := liveToken(t, unit, starter, j.ID, op)
			tc.mutate(&tok)

			if err := tryIssue(t, h, tok, fence); !errors.Is(err, jobtoken.ErrInvalid) {
				t.Fatalf("Issue of an invalid token = %v, want ErrInvalid", err)
			}

			wantNothing(t, h, tok.Digest, fence.Namespace)
		})
	}
}

func testFenceOtherNamespace(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	otherFence := h.Namespace(t, "beta")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-fence-namespace")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	err := h.Store.Issue(t.Context(), tok, otherFence, auditEntry(otherFence.Namespace))
	if !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Issue with a fence of another namespace = %v, want ErrFenced", err)
	}

	wantNothing(t, h, tok.Digest, fence.Namespace)
}

func testSameNameUnit(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	otherFence := h.Namespace(t, "beta")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	otherUnit := h.Unit(t, "beta", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-same-name")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	mustIssue(t, h, tok, fence)

	m := mustRead(t, h, tok.Digest)
	if m.Token.Unit != unit || m.Token.Unit.UnitID() == otherUnit.UnitID() {
		t.Fatalf("read token unit = %+v, want the unit of the token", m.Token.Unit)
	}

	if m.Claim.JobUnitID != unit.UnitID() {
		t.Fatalf("job unit ID = %q, want %q", m.Claim.JobUnitID, unit.UnitID())
	}

	other := liveToken(t, otherUnit, starter, j.ID, op)
	if err := tryIssue(t, h, other, otherFence); !errors.Is(err, jobtoken.ErrNotLive) {
		t.Fatalf("Issue for the other unit = %v, want ErrNotLive", err)
	}

	wantNothing(t, h, other.Digest, otherFence.Namespace)
}

func testRenamedUnit(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-renamed")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	mustIssue(t, h, tok, fence)
	renamed := h.Rename(t, unit, "web/renamed")

	if renamed.UnitID() != unit.UnitID() || renamed.UnitName() != "web/renamed" {
		t.Fatalf("renamed unit = %+v, want the same unit ID and the new name", renamed)
	}

	m := mustRead(t, h, tok.Digest)
	if m.Token.Unit.UnitID() != unit.UnitID() {
		t.Fatalf("token unit ID = %q, want %q", m.Token.Unit.UnitID(), unit.UnitID())
	}

	if m.Claim.JobUnitID != unit.UnitID() {
		t.Fatalf("job unit ID = %q, want %q", m.Claim.JobUnitID, unit.UnitID())
	}
}

func testStaleFence(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-stale")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	if err := tryIssue(t, h, tok, staleFence(fence)); !errors.Is(err, namespace.ErrFenced) {
		t.Fatalf("Issue with a stale fence = %v, want ErrFenced", err)
	}

	wantNothing(t, h, tok.Digest, fence.Namespace)
}

func testRefusedEntry(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-refused-entry")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	err := h.Store.Issue(t.Context(), tok, fence, invalidEntry(fence.Namespace))
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("Issue with a refused entry = %v, want ErrInvalidEntry", err)
	}

	wantNothing(t, h, tok.Digest, fence.Namespace)
}

func testEntryOtherNamespace(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-entry-namespace")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	e := auditEntry(fence.Namespace)
	e.Namespace = "beta"

	err := h.Store.Issue(t.Context(), tok, fence, e)
	if !errors.Is(err, audit.ErrInvalidEntry) {
		t.Fatalf("Issue with an entry of another namespace = %v, want ErrInvalidEntry", err)
	}

	wantNothing(t, h, tok.Digest, fence.Namespace)
}
