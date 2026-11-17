package job_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/job/jobtest"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// maxClaimSkips is the bound of pkg/job/claimer.go: a claim ends or gives back
// this many operations before it returns durable.ErrNoWork. The bound is
// unexported, so the test states the number that the code holds.
const maxClaimSkips = 64

// skippingOperations is a durable.Store that claims one operation on each call.
// Each operation names a job that does not exist, so the claimer ends it failed
// and looks for another one. Any other method of the port panics on the nil
// embedded interface, which proves the claimer claims nothing else.
type skippingOperations struct {
	durable.Store

	claims atomic.Int64
}

// newSkippingOperations returns a store that claims one operation on each call.
func newSkippingOperations() *skippingOperations {
	return &skippingOperations{Store: nil, claims: atomic.Int64{}}
}

// Claim implements durable.Store.
func (s *skippingOperations) Claim(context.Context, durable.ClaimRequest) (durable.Operation, error) {
	n := s.claims.Add(1)
	id := durable.ID("op-" + strconv.FormatInt(n, 10))

	return durable.Operation{
		ID: id, Key: job.PhaseKey(job.ID(job.IDPrefix+strconv.FormatInt(n, 10)), 0), Kind: "", Version: 0, Input: nil,
		State: durable.StateRunning,
		Steps: nil, Attempt: 0, Failures: 0, Expiries: 0, LastError: "",
		Claim:           durable.Claim{Operation: id, Owner: "runner-1", Token: n, ExpiresAt: time.Time{}},
		CancelRequested: false, NotBefore: time.Time{}, CreatedAt: time.Time{}, UpdatedAt: time.Time{},
	}, nil
}

// Fail implements durable.Store. It ends the operation that the claimer gives
// up, so the claimer goes on to the next one.
func (s *skippingOperations) Fail(context.Context, durable.Claim, durable.Failure) error { return nil }

// claimSkips counts the operations that one Claim skips past the bound.
type claimSkips struct {
	mu   sync.Mutex
	seen []int
}

// newClaimSkips returns an observer that has counted nothing.
func newClaimSkips() *claimSkips {
	return &claimSkips{mu: sync.Mutex{}, seen: nil}
}

// SkippedClaims implements job.ClaimObserver.
func (o *claimSkips) SkippedClaims(n int) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.seen = append(o.seen, n)
}

// total returns the sum of the counts that the observer got.
func (o *claimSkips) total() int {
	o.mu.Lock()
	defer o.mu.Unlock()

	sum := 0
	for _, n := range o.seen {
		sum += n
	}

	return sum
}

// noJobs is a job.Store that holds no job. Get answers job.ErrNotFound, so the
// claimer ends each operation failed. Any other method of the port panics on
// the nil embedded interface.
type noJobs struct {
	job.Store
}

// newNoJobs returns a job store that holds no job.
func newNoJobs() *noJobs {
	return &noJobs{Store: nil}
}

// Get implements job.Store. It holds no job.
func (*noJobs) Get(context.Context, job.ID) (job.Job, error) {
	return job.Job{}, job.ErrNotFound
}

// skipLogMessage is the message of the log line that the claimer writes for
// each operation that a poll skips.
const skipLogMessage = "the claim skipped an operation"

// TestClaimPollCountsBoundSkips proves that a claim that reaches the bound of
// maxSkippedClaims tells the observer how many operations it skipped, so the
// server counts each operation that the bound of a poll keeps from a runner.
// The bound ends the claim with durable.ErrNoWork, and the observer counts the
// operations that the claim ended or gave back. The log holds one line for each
// skipped operation, with the job that the operation belongs to.
func TestClaimPollCountsBoundSkips(t *testing.T) {
	t.Parallel()

	operations := newSkippingOperations()
	observer := newClaimSkips()

	var logs bytes.Buffer

	claimer := job.NewClaimer(
		newNoJobs(), operations, oneNamespace{ns: claimerNamespace()}, jobtest.NoGates{},
		job.WithClaimObserver(observer),
		job.WithClaimerLogger(slog.New(slog.NewTextHandler(&logs, nil))),
	)

	_, err := claimer.Claim(t.Context(), job.ClaimRequest{
		Owner: "runner-1", Kinds: []durable.Kind{job.KindWriteBack}, MaxVersions: nil, Lease: time.Minute,
	})
	if !errors.Is(err, durable.ErrNoWork) {
		t.Fatalf("Claim = %v, want durable.ErrNoWork", err)
	}

	if got := observer.total(); got != maxClaimSkips {
		t.Fatalf("the observer counted %d skipped operations, want %d", got, maxClaimSkips)
	}

	var skipLines int

	for line := range strings.SplitSeq(logs.String(), "\n") {
		if !strings.Contains(line, skipLogMessage) {
			continue
		}

		skipLines++

		if !strings.Contains(line, "job=job-") {
			t.Errorf("the skip line names no job: %s", line)
		}
	}

	if skipLines != maxClaimSkips {
		t.Fatalf("the log holds %d skip lines, want %d:\n%s", skipLines, maxClaimSkips, logs.String())
	}
}

// claimerNamespace returns the namespace that the claimer of the test reads.
// No claim of the test reads it, because every operation names no phase.
func claimerNamespace() namespace.Namespace {
	return namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Time{}, Name: "acme", PrimaryInstallation: "",
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}
}
