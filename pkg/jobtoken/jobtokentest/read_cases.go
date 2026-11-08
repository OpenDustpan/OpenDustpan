package jobtokentest

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/jobtoken"
)

func readCases() []suiteCase {
	return []suiteCase{
		{name: "read of an unknown digest returns not found", test: testUnknownDigest},
		{name: "read of a raw restored row gives the record and the live claim", test: testRawRestoredRow},
		{name: "read of a duplicate issue keeps the first record", test: testDuplicateDigest},
		{name: "read of a raw row whose job does not exist returns not found", test: testRawMissingJob},
		{
			name: "read of a raw row whose claim operation does not exist returns not found",
			test: testRawMissingClaim,
		},
		{
			name: "read of a raw row whose starter is another principal gives the job starter of the job",
			test: testRawStarterAnotherPrincipal,
		},
		{name: "read of a raw row whose starter is deleted returns not found", test: testRawMissingStarter},
		{
			name: "read of a raw row whose starter is disabled clears the enabled flag",
			test: testRawDisabledStarter,
		},
		{
			name: "read of a raw row whose namespace differs from its unit returns not found",
			test: testRawNamespaceMismatch,
		},
		{name: "read of a raw row of a queued job gives the queued state", test: testRawQueuedJob},
		{
			name: "a token of a namespace that is deleted after the issue is not found",
			test: testNamespaceDeleted,
		},
		{
			name: "a token of a namespace that is renamed after the issue is not found",
			test: testNamespaceRenamed,
		},
	}
}

func testUnknownDigest(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-unknown")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	if _, err := h.Store.GetByDigest(t.Context(), tok.Digest); !errors.Is(err, jobtoken.ErrNotFound) {
		t.Fatalf("GetByDigest of an unknown digest = %v, want ErrNotFound", err)
	}
}

func testRawRestoredRow(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-raw")
	j := h.Job(t, unit, starter, op)
	tok := tokenFor(t, &tokenSpec{Unit: unit, Starter: starter, JobID: j.ID, Operation: op, ExpiresAt: issueTime()})
	tok.CreatedAt = issueTime().Add(-time.Minute)

	h.WriteRaw(t, tok)

	m := mustRead(t, h, tok.Digest)
	if m.Token != tok {
		t.Fatalf("read token = %+v, want %+v", m.Token, tok)
	}

	want := jobtoken.Claim{
		OperationState: op.State, LeaseEndsAt: op.LeaseEnd, JobID: j.ID, JobState: job.StateRunning,
		JobOperation: op.ID, JobUnitID: unit.UnitID(), JobStarterID: starter.ID, Starter: starter.Principal,
		StarterRecordID: starter.ID, OperationClaimToken: op.Token, StarterEnabled: true,
	}
	if m.Claim != want {
		t.Fatalf("read claim = %+v, want %+v", m.Claim, want)
	}

	if len(h.Audit(t, fence.Namespace)) != 0 {
		t.Fatal("a raw write added an audit entry")
	}
}

func testDuplicateDigest(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-duplicate")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	mustIssue(t, h, tok, fence)

	if err := tryIssue(t, h, tok, fence); !errors.Is(err, jobtoken.ErrAlreadyExists) {
		t.Fatalf("Issue of a duplicate digest = %v, want ErrAlreadyExists", err)
	}

	if got := len(h.Audit(t, fence.Namespace)); got != 1 {
		t.Fatalf("audit entries = %d, want 1", got)
	}

	if m := mustRead(t, h, tok.Digest); m.Token != tok {
		t.Fatalf("read token = %+v, want the first record %+v", m.Token, tok)
	}
}

func testRawMissingJob(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-raw-missing-job")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)
	tok.JobID = "job-missing"

	h.WriteRaw(t, tok)

	if _, err := h.Store.GetByDigest(t.Context(), tok.Digest); !errors.Is(err, jobtoken.ErrNotFound) {
		t.Fatalf("GetByDigest of a row with no job = %v, want ErrNotFound", err)
	}
}

func testRawMissingClaim(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-raw-missing-claim")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)
	tok.ClaimOperation = "op-missing"

	h.WriteRaw(t, tok)

	if _, err := h.Store.GetByDigest(t.Context(), tok.Digest); !errors.Is(err, jobtoken.ErrNotFound) {
		t.Fatalf("GetByDigest of a row with no claim = %v, want ErrNotFound", err)
	}
}

func testRawStarterAnotherPrincipal(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	alice := h.Principal(t, "alice")
	bob := h.Principal(t, "bob")
	op := liveOperation("op-raw-other-starter")
	j := h.Job(t, unit, alice, op)
	tok := liveToken(t, unit, bob, j.ID, op)

	h.WriteRaw(t, tok)

	m := mustRead(t, h, tok.Digest)
	if m.Claim.JobStarterID != alice.ID {
		t.Fatalf("job starter ID = %q, want %q", m.Claim.JobStarterID, alice.ID)
	}

	if m.Claim.StarterRecordID != bob.ID {
		t.Fatalf("starter record ID = %q, want %q", m.Claim.StarterRecordID, bob.ID)
	}
}

func testRawMissingStarter(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-raw-missing-starter")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	h.WriteRaw(t, tok)
	h.Delete(t, starter)

	if _, err := h.Store.GetByDigest(t.Context(), tok.Digest); !errors.Is(err, jobtoken.ErrNotFound) {
		t.Fatalf("GetByDigest of a row with a deleted starter = %v, want ErrNotFound", err)
	}
}

func testRawDisabledStarter(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-raw-disabled-starter")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	h.WriteRaw(t, tok)
	h.Disable(t, starter)

	m := mustRead(t, h, tok.Digest)
	if m.Claim.StarterEnabled {
		t.Fatal("read claim marks a disabled starter as enabled")
	}
}

func testRawNamespaceMismatch(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, "acme")
	otherFence := h.Namespace(t, "beta")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-raw-namespace")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)
	tok.Namespace = otherFence.Namespace

	h.WriteRaw(t, tok)

	if _, err := h.Store.GetByDigest(t.Context(), tok.Digest); !errors.Is(err, jobtoken.ErrNotFound) {
		t.Fatalf("GetByDigest of a row in another namespace = %v, want ErrNotFound", err)
	}
}

func testRawQueuedJob(t *testing.T, h Harness) {
	t.Helper()

	h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-raw-queued")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	h.WriteRaw(t, tok)
	h.SetJobState(t, j.ID, job.StateQueued)

	m := mustRead(t, h, tok.Digest)
	if m.Claim.JobState != job.StateQueued {
		t.Fatalf("job state = %q, want %q", m.Claim.JobState, job.StateQueued)
	}
}

func testNamespaceDeleted(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-deleted-namespace")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	mustIssue(t, h, tok, fence)
	h.DeleteNamespace(t, fence.Namespace)

	if _, err := h.Store.GetByDigest(t.Context(), tok.Digest); !errors.Is(err, jobtoken.ErrNotFound) {
		t.Fatalf("GetByDigest of a token of a deleted namespace = %v, want ErrNotFound", err)
	}
}

func testNamespaceRenamed(t *testing.T, h Harness) {
	t.Helper()

	fence := h.Namespace(t, "acme")
	unit := h.Unit(t, "acme", "pulumi", "web/prod")
	starter := h.Principal(t, "alice")
	op := liveOperation("op-renamed-namespace")
	j := h.Job(t, unit, starter, op)
	tok := liveToken(t, unit, starter, j.ID, op)

	mustIssue(t, h, tok, fence)
	h.RenameNamespace(t, fence.Namespace, "gamma")

	if _, err := h.Store.GetByDigest(t.Context(), tok.Digest); !errors.Is(err, jobtoken.ErrNotFound) {
		t.Fatalf("GetByDigest of a token of a renamed namespace = %v, want ErrNotFound", err)
	}
}
