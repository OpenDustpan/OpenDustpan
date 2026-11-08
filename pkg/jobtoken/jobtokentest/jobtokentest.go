// Package jobtokentest is the conformance suite of the Store port of package
// jobtoken. A new adapter runs Run with a Factory.
//
// Each case states one rule of the port. The hooks of the Harness set up the
// world of a case: a namespace, a unit, a starter principal, a job and its
// claim. They change that world with the rules of package durable and package
// job, so an adapter runs the same cases.
package jobtokentest

import (
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/jobtoken"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Starter is a principal of the world: the ID of its record and the principal
// that a request of it carries.
type Starter struct {
	ID        string
	Principal identity.Principal
}

// Operation describes the claim operation of a job.
type Operation struct {
	ID       durable.ID
	State    durable.State
	Token    int64
	LeaseEnd time.Time
}

// Harness is one fresh, empty store for one subtest, with the hooks that set
// up its world.
type Harness struct {
	Store jobtoken.Store

	// Namespace makes the namespace called name and returns its fence.
	Namespace func(t *testing.T, name string) namespace.Fence
	// DeleteNamespace deletes the namespace ns. A read of a token of the
	// namespace returns ErrNotFound.
	DeleteNamespace func(t *testing.T, ns namespace.Name)
	// RenameNamespace changes the name of the namespace ns to newName. The
	// stable ID of the namespace stays. A read of a token of the namespace
	// returns ErrNotFound when the current name differs from the namespace of
	// the unit of the record.
	RenameNamespace func(t *testing.T, ns namespace.Name, newName string)
	// Unit makes the unit of the tool called name in the namespace ns, and
	// returns its reference with the stable unit ID set.
	Unit func(t *testing.T, ns, tool, name string) identity.Object
	// Principal makes the principal with the subject subject.
	Principal func(t *testing.T, subject string) Starter
	// Disable disables the principal s.
	Disable func(t *testing.T, s Starter)
	// Delete deletes the principal s.
	Delete func(t *testing.T, s Starter)
	// Rename changes the name of unit to newName and returns the renamed
	// reference. The stable unit ID stays.
	Rename func(t *testing.T, unit identity.Object, newName string) identity.Object
	// Job makes a running job on unit, started by starter, with the claim
	// operation op as its current operation.
	Job func(t *testing.T, unit identity.Object, starter Starter, op Operation) job.Job
	// Takeover gives the claim operation of the job with the ID j a new claim
	// token and lease, and returns the end of the new lease.
	Takeover func(t *testing.T, j job.ID) time.Time
	// SetJobState sets the state of the job with the ID j.
	SetJobState func(t *testing.T, j job.ID, state job.State)
	// EndClaim ends the claim operation of the job with the ID j as
	// succeeded.
	EndClaim func(t *testing.T, j job.ID)
	// ReleaseClaim returns the claim operation of the job with the ID j to
	// pending with no claim.
	ReleaseClaim func(t *testing.T, j job.ID)
	// EndJob moves the job with the ID j to a terminal state.
	EndJob func(t *testing.T, j job.ID)
	// MoveJob makes op the current operation of the job with the ID j.
	MoveJob func(t *testing.T, j job.ID, op durable.ID)
	// WriteRaw writes tok straight into the store with no check, as a
	// restored row arrives.
	WriteRaw func(t *testing.T, tok jobtoken.Token)
	// Audit returns the audit entries of a namespace, oldest first.
	Audit func(t *testing.T, ns namespace.Name) []audit.Entry
}

// Factory returns a fresh harness for one subtest.
type Factory func(t *testing.T) Harness

type suiteCase struct {
	name string
	test func(t *testing.T, h Harness)
}

// CaseNames returns the name of each case, in order.
func CaseNames() []string {
	all := cases()
	names := make([]string, 0, len(all))

	for _, tc := range all {
		names = append(names, tc.name)
	}

	return names
}

// Run runs every case of the suite.
func Run(t *testing.T, newHarness Factory) {
	t.Helper()

	for _, tc := range cases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.test(t, newHarness(t))
		})
	}
}

func cases() []suiteCase {
	out := make([]suiteCase, 0, caseCount)
	out = append(out, issueCases()...)
	out = append(out, readCases()...)

	return out
}

const (
	// issueMs is the time of the cases, in Unix milliseconds.
	issueMs = 1_700_000_000_000
	// lease is the time from a claim to its end.
	lease = 15 * time.Minute
	// stepTimeout is the timeout of the step of a case.
	stepTimeout = 90 * time.Second
	// grace is the grace period after the step timeout.
	grace = 30 * time.Second
	// oneMillisecond is one millisecond.
	oneMillisecond = time.Millisecond
	// caseCount is the number of cases of the suite.
	caseCount = 37
)

// issueTime returns the time of the cases.
func issueTime() time.Time {
	return time.UnixMilli(issueMs).UTC()
}

// liveExpiry returns the hard expiry of a token of the cases.
func liveExpiry() time.Time {
	return issueTime().Add(stepTimeout + grace)
}

// liveOperation returns a running claim operation with a live lease.
func liveOperation(id durable.ID) Operation {
	return Operation{ID: id, State: durable.StateRunning, Token: 1, LeaseEnd: issueTime().Add(lease)}
}

// tokenSpec describes the token that tokenFor builds.
type tokenSpec struct {
	Unit      identity.Object
	Starter   Starter
	JobID     job.ID
	Operation Operation
	ExpiresAt time.Time
}

// tokenFor returns a token for the specification, with a fresh secret.
func tokenFor(t *testing.T, spec *tokenSpec) jobtoken.Token {
	t.Helper()

	secret, err := jobtoken.NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}

	var tok jobtoken.Token

	tok.Digest = jobtoken.DigestOf(secret)
	tok.StarterID = spec.Starter.ID
	tok.JobID = spec.JobID
	tok.Namespace = spec.Unit.Namespace()
	tok.Unit = spec.Unit
	tok.ClaimOperation = spec.Operation.ID
	tok.ClaimToken = spec.Operation.Token
	tok.ExpiresAt = spec.ExpiresAt
	tok.CreatedAt = issueTime()

	return tok
}

// liveToken returns a token for the world with the live expiry.
func liveToken(
	t *testing.T, unit identity.Object, starter Starter, jobID job.ID, op Operation,
) jobtoken.Token {
	t.Helper()

	spec := tokenSpec{Unit: unit, Starter: starter, JobID: jobID, Operation: op, ExpiresAt: liveExpiry()}

	return tokenFor(t, &spec)
}

// auditEntry returns a valid audit entry of an issue in ns.
func auditEntry(ns namespace.Name) audit.Entry {
	var e audit.Entry

	e.At = issueTime()
	e.Namespace = ns
	e.Subject = "alice"
	e.Action = "job:issue"
	e.Object = ns.String()
	e.Channel = audit.ChannelAPI
	e.Kind = audit.KindChange
	e.Outcome = audit.OutcomeSuccess

	return e
}

// invalidEntry returns an audit entry that fails validation.
func invalidEntry(ns namespace.Name) audit.Entry {
	e := auditEntry(ns)
	e.Subject = ""

	return e
}

// staleFence returns a fence that is older than f.
func staleFence(f namespace.Fence) namespace.Fence {
	f.FailoverVersion--

	return f
}

// tryIssue issues tok and returns the error of the store.
func tryIssue(t *testing.T, h Harness, tok jobtoken.Token, fence namespace.Fence) error {
	t.Helper()

	return h.Store.Issue(t.Context(), tok, fence, auditEntry(fence.Namespace))
}

// mustIssue issues tok and fails the test on an error.
func mustIssue(t *testing.T, h Harness, tok jobtoken.Token, fence namespace.Fence) {
	t.Helper()

	if err := tryIssue(t, h, tok, fence); err != nil {
		t.Fatalf("Issue: %v", err)
	}
}

// mustRead returns the match of d and fails the test on an error.
func mustRead(t *testing.T, h Harness, d jobtoken.Digest) jobtoken.Match {
	t.Helper()

	m, err := h.Store.GetByDigest(t.Context(), d)
	if err != nil {
		t.Fatalf("GetByDigest: %v", err)
	}

	return m
}

// wantNothing reports whether a failed issue wrote no record and no entry.
func wantNothing(t *testing.T, h Harness, d jobtoken.Digest, ns namespace.Name) {
	t.Helper()

	if _, err := h.Store.GetByDigest(t.Context(), d); err == nil {
		t.Fatal("GetByDigest after a failed Issue found a record")
	}

	if got := len(h.Audit(t, ns)); got != 0 {
		t.Fatalf("audit entries after a failed Issue = %d, want 0", got)
	}
}
