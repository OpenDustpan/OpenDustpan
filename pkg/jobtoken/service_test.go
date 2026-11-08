package jobtoken_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/jobtoken"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
)

// errStoreFailed is the error of a store that fails.
var errStoreFailed = errors.New("store failed")

// testNow returns the clock of the tests.
func testNow() time.Time {
	return time.UnixMilli(1_700_000_000_000).UTC()
}

// fakeStore is a jobtoken.Store whose answer the test sets.
type fakeStore struct {
	match    jobtoken.Match
	err      error
	issueErr error
	got      jobtoken.Digest
	entry    audit.Entry
	issued   []jobtoken.Token
	issues   int
}

// Issue implements jobtoken.Store.
func (f *fakeStore) Issue(_ context.Context, t jobtoken.Token, _ namespace.Fence, e audit.Entry) error {
	f.issues++
	f.issued = append(f.issued, t)
	f.entry = e

	return f.issueErr
}

// GetByDigest implements jobtoken.Store.
func (f *fakeStore) GetByDigest(_ context.Context, d jobtoken.Digest) (jobtoken.Match, error) {
	f.got = d

	return f.match, f.err
}

// testWorld returns a namespace, a unit with a stable ID and a starter.
func testWorld(t *testing.T) (namespace.Name, identity.Object, identity.Principal) {
	t.Helper()

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	unit, err := identity.UnitObject("pulumi", ns, "web/prod")
	if err != nil {
		t.Fatalf("unit: %v", err)
	}

	unit = unit.WithUnitID("unt-1")

	var p identity.Principal

	p.Subject = "alice"
	p.DisplayName = "Alice"
	p.Kind = identity.KindPerson

	return ns, unit, p
}

// liveMatch returns a match of a live token with the digest d at the time
// now.
func liveMatch(t *testing.T, d jobtoken.Digest, now time.Time) jobtoken.Match {
	t.Helper()

	_, unit, starter := testWorld(t)

	var m jobtoken.Match

	m.Token.Digest = d
	m.Token.StarterID = "prn-alice"
	m.Token.JobID = "job-1"
	m.Token.Namespace = unit.Namespace()
	m.Token.Unit = unit
	m.Token.ClaimOperation = "op-1"
	m.Token.ClaimToken = 1
	m.Token.ExpiresAt = now.Add(time.Hour)
	m.Token.CreatedAt = now.Add(-time.Minute)

	m.Claim.OperationState = durable.StateRunning
	m.Claim.OperationClaimToken = 1
	m.Claim.LeaseEndsAt = now.Add(time.Minute)
	m.Claim.JobID = "job-1"
	m.Claim.JobState = job.StateRunning
	m.Claim.JobOperation = "op-1"
	m.Claim.JobUnitID = unit.UnitID()
	m.Claim.JobStarterID = "prn-alice"
	m.Claim.Starter = starter
	m.Claim.StarterRecordID = "prn-alice"
	m.Claim.StarterEnabled = true

	return m
}

// serviceCase is one row of the service table.
type serviceCase struct {
	name       string
	mutate     func(*jobtoken.Match)
	storeErr   error
	noStore    bool
	refusal    bool
	failClosed bool
}

func allowCase(name string, mutate func(*jobtoken.Match)) serviceCase {
	return serviceCase{name: name, mutate: mutate, storeErr: nil, noStore: false, refusal: false, failClosed: false}
}

func denyCase(name string, mutate func(*jobtoken.Match)) serviceCase {
	return serviceCase{name: name, mutate: mutate, storeErr: nil, noStore: false, refusal: true, failClosed: false}
}

func unknownCase(name string) serviceCase {
	return serviceCase{
		name:       name,
		mutate:     nil,
		storeErr:   jobtoken.ErrNotFound,
		noStore:    false,
		refusal:    true,
		failClosed: false,
	}
}

func failingCase(name string) serviceCase {
	return serviceCase{
		name:       name,
		mutate:     nil,
		storeErr:   errStoreFailed,
		noStore:    false,
		refusal:    false,
		failClosed: true,
	}
}

func malformedCase(name string) serviceCase {
	return serviceCase{name: name, mutate: nil, storeErr: nil, noStore: true, refusal: true, failClosed: false}
}

func serviceCases() []serviceCase {
	return []serviceCase{
		allowCase("a live token", nil),
		denyCase("the operation is not running", func(m *jobtoken.Match) {
			m.Claim.OperationState = durable.StatePending
		}),
		denyCase("the operation state is empty", func(m *jobtoken.Match) {
			m.Claim.OperationState = durable.State("")
		}),
		denyCase("the operation state is unknown", func(m *jobtoken.Match) {
			m.Claim.OperationState = durable.State("unknown-state")
		}),
		denyCase("the claim token of the operation differs", func(m *jobtoken.Match) {
			m.Claim.OperationClaimToken = 2
		}),
		denyCase("the claim lease ends at now", func(m *jobtoken.Match) {
			m.Claim.LeaseEndsAt = testNow()
		}),
		denyCase("the claim lease is the zero time", func(m *jobtoken.Match) {
			m.Claim.LeaseEndsAt = time.Time{}
		}),
		allowCase("the claim lease ends one millisecond after now", func(m *jobtoken.Match) {
			m.Claim.LeaseEndsAt = testNow().Add(time.Millisecond)
		}),
		allowCase("the job is dispatched", func(m *jobtoken.Match) {
			m.Claim.JobState = job.StateDispatched
		}),
		allowCase("the job is canceling", func(m *jobtoken.Match) {
			m.Claim.JobState = job.StateCanceling
		}),
		denyCase("the job is queued", func(m *jobtoken.Match) {
			m.Claim.JobState = job.StateQueued
		}),
		denyCase("the job is awaiting approval", func(m *jobtoken.Match) {
			m.Claim.JobState = job.StateAwaitingApproval
		}),
		denyCase("the job state is empty", func(m *jobtoken.Match) {
			m.Claim.JobState = job.State("")
		}),
		denyCase("the job state is unknown", func(m *jobtoken.Match) {
			m.Claim.JobState = job.State("unknown-state")
		}),
		denyCase("the job state is paused-unknown", func(m *jobtoken.Match) {
			m.Claim.JobState = job.State("paused-unknown")
		}),
		denyCase("the job is terminal", func(m *jobtoken.Match) {
			m.Claim.JobState = job.StateFailed
		}),
		denyCase("the job runs another operation", func(m *jobtoken.Match) {
			m.Claim.JobOperation = "op-2"
		}),
		denyCase("the job unit ID differs", func(m *jobtoken.Match) {
			m.Claim.JobUnitID = "unt-2"
		}),
		denyCase("the hard expiry is at now", func(m *jobtoken.Match) {
			m.Token.ExpiresAt = testNow()
		}),
		denyCase("the hard expiry is the zero time", func(m *jobtoken.Match) {
			m.Token.ExpiresAt = time.Time{}
		}),
		allowCase("the hard expiry is one millisecond after now", func(m *jobtoken.Match) {
			m.Token.ExpiresAt = testNow().Add(time.Millisecond)
		}),
		denyCase("the token lifetime is above the maximum", func(m *jobtoken.Match) {
			m.Token.CreatedAt = testNow().Add(-time.Hour)
			m.Token.ExpiresAt = m.Token.CreatedAt.Add(jobtoken.MaxLifetime + time.Millisecond)
		}),
		denyCase("the starter is not enabled", func(m *jobtoken.Match) {
			m.Claim.StarterEnabled = false
		}),
		denyCase("the starter has no subject", func(m *jobtoken.Match) {
			m.Claim.Starter.Subject = ""
		}),
		denyCase("the starter is not the job starter", func(m *jobtoken.Match) {
			m.Token.StarterID = "prn-bob"
		}),
		denyCase("the starter record differs from the token starter", func(m *jobtoken.Match) {
			m.Claim.StarterRecordID = "prn-bob"
		}),
		denyCase("the job record differs from the token job", func(m *jobtoken.Match) {
			m.Claim.JobID = "job-2"
		}),
		denyCase(
			"the starter record of the raw row is another principal than the job starter",
			func(m *jobtoken.Match) {
				m.Token.StarterID = "prn-bob"
				m.Claim.StarterRecordID = "prn-bob"
			},
		),
		denyCase("the store returns a valid record with another digest", func(m *jobtoken.Match) {
			m.Token.Digest[0] ^= 1
		}),
		denyCase("the token and the claim have an empty starter on both sides", func(m *jobtoken.Match) {
			m.Token.StarterID = ""
			m.Claim.JobStarterID = ""
			m.Claim.StarterRecordID = ""
		}),
		denyCase("the token and the claim have an empty operation on both sides", func(m *jobtoken.Match) {
			m.Token.ClaimOperation = ""
			m.Claim.JobOperation = ""
		}),
		denyCase("the token and the claim have an empty unit ID on both sides", func(m *jobtoken.Match) {
			m.Token.Unit = m.Token.Unit.WithUnitID("")
			m.Claim.JobUnitID = ""
		}),
		denyCase("the token namespace differs from its unit", func(m *jobtoken.Match) {
			m.Token.Namespace = "beta"
		}),
		allowCase("the starter carries another credential and a temporary elevation", func(m *jobtoken.Match) {
			m.Claim.Starter.TokenID = "tok-x"
			m.Claim.Starter.TemporaryAdministrator = true
			m.Claim.Starter.TemporaryExpiresAt = testNow().Add(2 * time.Hour)
		}),
		allowCase("the starter is an administrator", func(m *jobtoken.Match) {
			m.Claim.Starter.Admin = true
		}),
		unknownCase("the store does not find the digest"),
		failingCase("the store fails"),
		malformedCase("the value is not a secret"),
	}
}

func TestAuthenticate(t *testing.T) {
	t.Parallel()

	secret, err := jobtoken.NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}

	digest := jobtoken.DigestOf(secret)

	for _, tc := range serviceCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runServiceCase(t, tc, secret, digest)
		})
	}
}

func runServiceCase(t *testing.T, tc serviceCase, secret jobtoken.Secret, d jobtoken.Digest) {
	t.Helper()

	m := liveMatch(t, d, testNow())
	if tc.mutate != nil {
		tc.mutate(&m)
	}

	var store fakeStore

	store.match = m
	store.err = tc.storeErr

	svc := jobtoken.NewService(&store, testNow)

	raw := secret.Reveal()
	if tc.noStore {
		raw = "not-a-secret"
	}

	p, unit, err := svc.Authenticate(t.Context(), raw)

	var result serviceResult

	result.principal = p
	result.unit = unit
	result.err = err
	result.store = &store
	result.raw = raw
	result.match = &m

	checkServiceResult(t, tc, &result)
}

// serviceResult is the answer of one service case.
type serviceResult struct {
	principal identity.Principal
	unit      identity.Object
	err       error
	store     *fakeStore
	raw       string
	match     *jobtoken.Match
}

func checkServiceResult(t *testing.T, tc serviceCase, r *serviceResult) {
	t.Helper()

	switch {
	case tc.failClosed:
		checkFailClosed(t, r.err, tc.storeErr)
		checkZeroPrincipal(t, r.principal)
		checkZeroObject(t, r.unit)
	case tc.refusal:
		checkRefusal(t, r.err, r.raw, &r.match.Token)
		checkZeroPrincipal(t, r.principal)
		checkZeroObject(t, r.unit)

		if tc.noStore && !r.store.got.IsZero() {
			t.Fatal("a value that is not a secret reached the store")
		}
	default:
		checkPrincipal(t, r.principal, r.unit, r.err, r.match)
	}
}

func checkFailClosed(t *testing.T, err, want error) {
	t.Helper()

	if !errors.Is(err, want) {
		t.Fatalf("Authenticate = %v, want the store error", err)
	}

	if errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatal("a store failure must not read as a refusal")
	}
}

func checkRefusal(t *testing.T, err error, raw string, tok *jobtoken.Token) {
	t.Helper()

	if !errors.Is(err, identity.ErrUnauthenticated) {
		t.Fatalf("Authenticate = %v, want ErrUnauthenticated", err)
	}

	checkNoLeak(t, err, raw, tok)
}

func checkZeroPrincipal(t *testing.T, p identity.Principal) {
	t.Helper()

	var zero identity.Principal

	if p != zero {
		t.Fatal("a refusal returned a principal")
	}
}

func checkZeroObject(t *testing.T, o identity.Object) {
	t.Helper()

	var zero identity.Object

	if o != zero {
		t.Fatal("a refusal returned an object")
	}
}

func checkPrincipal(
	t *testing.T, p identity.Principal, unit identity.Object, err error, m *jobtoken.Match,
) {
	t.Helper()

	if err != nil {
		t.Fatalf("Authenticate = %v", err)
	}

	if unit != m.Token.Unit || unit.UnitID() == "" {
		t.Fatalf("unit = %+v, want the unit of the record with its stable unit ID", unit)
	}

	want := m.Claim.Starter
	want.JobID = string(m.Token.JobID)
	want.Credential = identity.CredentialJobToken
	want.TokenExpiresAt = m.Token.ExpiresAt
	want.TokenID = ""
	want.Admin = false
	want.TemporaryAdministrator = false
	want.TemporaryExpiresAt = time.Time{}

	if p != want {
		t.Fatalf("principal = %+v, want %+v", p, want)
	}
}

// checkNoLeak proves that err names none of the values of the token.
func checkNoLeak(t *testing.T, err error, secret string, tok *jobtoken.Token) {
	t.Helper()

	text := err.Error()
	for _, value := range []string{secret, tok.Digest.String(), string(tok.JobID), tok.Unit.UnitName(), tok.Namespace.String()} {
		if value != "" && strings.Contains(text, value) {
			t.Fatalf("error %q holds %q", text, value)
		}
	}
}

// maxResourceSteps returns two steps of the maximum timeout that each change
// resources.
func maxResourceSteps() []job.Step {
	steps := make([]job.Step, 2)
	names := []string{"apply-a", "apply-b"}

	for i := range steps {
		steps[i] = job.Step{
			Name:             names[i],
			Role:             job.RoleTool,
			Phase:            job.PhaseApply,
			Args:             []string{"tofu", "apply"},
			Env:              nil,
			Dir:              "",
			Timeout:          job.MaxStepTimeout,
			AllowFailure:     false,
			ChangesResources: true,
			Outputs:          nil,
		}
	}

	return steps
}

// issueExpiryCase is one row of the expiry table of Issue.
type issueExpiryCase struct {
	name       string
	mutate     func(*jobtoken.IssueRequest)
	wantText   string
	wantIssues int
}

func TestIssueExpiry(t *testing.T) {
	t.Parallel()

	for _, tc := range issueExpiryCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			runIssueExpiryCase(t, tc)
		})
	}
}

func issueExpiryCases() []issueExpiryCase {
	return []issueExpiryCase{
		{
			name:       "at the issue time",
			mutate:     func(req *jobtoken.IssueRequest) { req.ExpiresAt = testNow() },
			wantText:   "the expiry is not after the issue time",
			wantIssues: 0,
		},
		{
			name:       "before the issue time",
			mutate:     func(req *jobtoken.IssueRequest) { req.ExpiresAt = testNow().Add(-time.Millisecond) },
			wantText:   "the expiry is not after the issue time",
			wantIssues: 0,
		},
		{
			name: "a job with no steps",
			mutate: func(req *jobtoken.IssueRequest) {
				req.Job.Spec.Steps = nil
				req.ExpiresAt = testNow().Add(30 * time.Minute)
			},
			wantText:   "the job has no steps",
			wantIssues: 0,
		},
		{
			name: "two steps of the maximum timeout and an expiry 30 hours after the issue",
			mutate: func(req *jobtoken.IssueRequest) {
				req.Job.Spec.Steps = maxResourceSteps()
				req.ExpiresAt = testNow().Add(30 * time.Hour)
			},
			wantText:   "",
			wantIssues: 1,
		},
		{
			name: "one millisecond after the deadline of the phase",
			mutate: func(req *jobtoken.IssueRequest) {
				req.Job.Spec.Steps = maxResourceSteps()
				req.ExpiresAt = testNow().Add(
					runner.LimitsFor(req.Job.Spec.Steps).Deadline + time.Millisecond,
				)
			},
			wantText:   "the expiry is above the deadline of the phase",
			wantIssues: 0,
		},
		{
			name: "at the deadline of the phase",
			mutate: func(req *jobtoken.IssueRequest) {
				req.Job.Spec.Steps = maxResourceSteps()
				req.ExpiresAt = testNow().Add(runner.LimitsFor(req.Job.Spec.Steps).Deadline)
			},
			wantText:   "",
			wantIssues: 1,
		},
	}
}

func runIssueExpiryCase(t *testing.T, tc issueExpiryCase) {
	t.Helper()

	var store fakeStore

	svc := jobtoken.NewService(&store, testNow)
	req := issueRequest(t, testNow().Add(time.Hour))
	tc.mutate(req)

	_, err := svc.Issue(t.Context(), req)

	if tc.wantIssues != 0 {
		if err != nil {
			t.Fatalf("Issue = %v", err)
		}

		if store.issues != tc.wantIssues {
			t.Fatalf("store issues = %d, want %d", store.issues, tc.wantIssues)
		}

		return
	}

	if !errors.Is(err, jobtoken.ErrInvalid) {
		t.Fatalf("Issue = %v, want ErrInvalid", err)
	}

	if !strings.Contains(err.Error(), tc.wantText) {
		t.Fatalf("Issue = %v, want the text %q", err, tc.wantText)
	}

	if store.issues != 0 {
		t.Fatal("a refused issue reached the store")
	}
}

func TestIssueReturnsASecretAndStoresTheDigest(t *testing.T) {
	t.Parallel()

	var store fakeStore

	svc := jobtoken.NewService(&store, testNow)
	req := issueRequest(t, testNow().Add(time.Hour))

	secret, err := svc.Issue(t.Context(), req)
	if err != nil {
		t.Fatalf("Issue = %v", err)
	}

	if !jobtoken.LooksLikeSecret(secret.Reveal()) {
		t.Fatalf("secret = %q, want the job token form", secret)
	}

	if store.issues != 1 {
		t.Fatalf("store issues = %d, want 1", store.issues)
	}

	if len(store.issued) != 1 || store.issued[0].Digest != jobtoken.DigestOf(secret) {
		t.Fatal("the store did not get the digest of the returned secret")
	}

	if secret.String() != "[job token]" {
		t.Fatalf("formatted secret = %q, want the redacted text", secret)
	}
}

func TestIssueSetsTheEntryTime(t *testing.T) {
	t.Parallel()

	var store fakeStore

	svc := jobtoken.NewService(&store, testNow)
	req := issueRequest(t, testNow().Add(time.Hour))
	req.Entry.At = testNow().Add(-time.Hour)
	req.Entry.Action = "job:issue"

	if _, err := svc.Issue(t.Context(), req); err != nil {
		t.Fatalf("Issue = %v", err)
	}

	if !store.entry.At.Equal(testNow()) {
		t.Fatalf("entry time = %v, want %v", store.entry.At, testNow())
	}

	if store.entry.Action != req.Entry.Action {
		t.Fatalf("entry action = %q, want %q", store.entry.Action, req.Entry.Action)
	}

	if !req.Entry.At.Equal(testNow().Add(-time.Hour)) {
		t.Fatal("Issue changed the entry of the request")
	}
}

func TestIssueRefusesBeforeTheStore(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		nilReq bool
		mutate func(*jobtoken.IssueRequest)
	}{
		{name: "a nil request", nilReq: true, mutate: nil},
		{name: "a starter that is not the job starter", nilReq: false, mutate: func(req *jobtoken.IssueRequest) {
			req.StarterSubject = "bob"
		}},
		{name: "an empty starter subject", nilReq: false, mutate: func(req *jobtoken.IssueRequest) {
			req.StarterSubject = ""
		}},
		{name: "an empty starter subject and no job starter", nilReq: false, mutate: func(req *jobtoken.IssueRequest) {
			req.StarterSubject = ""
			req.Job.StartedBy = ""
		}},
		{name: "a claim of another operation than the job", nilReq: false, mutate: func(req *jobtoken.IssueRequest) {
			req.Claim.Operation = "op-2"
		}},
		{name: "a job without a current operation", nilReq: false, mutate: func(req *jobtoken.IssueRequest) {
			req.Job.Phases = nil
			req.Job.Current = 0
		}},
		{name: "a job with a negative current phase", nilReq: false, mutate: func(req *jobtoken.IssueRequest) {
			req.Job.Current = -1
		}},
		{name: "a token that fails validation", nilReq: false, mutate: func(req *jobtoken.IssueRequest) {
			req.Job.Spec.Unit = req.Job.Spec.Unit.WithUnitID("")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var store fakeStore

			svc := jobtoken.NewService(&store, testNow)

			var req *jobtoken.IssueRequest
			if !tc.nilReq {
				req = issueRequest(t, testNow().Add(time.Hour))
				tc.mutate(req)
			}

			if _, err := svc.Issue(t.Context(), req); !errors.Is(err, jobtoken.ErrInvalid) {
				t.Fatalf("Issue = %v, want ErrInvalid", err)
			}

			if store.issues != 0 {
				t.Fatal("a refused issue reached the store")
			}
		})
	}
}

func TestIssueStoreFailureReturnsNoSecret(t *testing.T) {
	t.Parallel()

	var store fakeStore

	store.issueErr = errStoreFailed

	svc := jobtoken.NewService(&store, testNow)
	req := issueRequest(t, testNow().Add(time.Hour))

	secret, err := svc.Issue(t.Context(), req)
	if !errors.Is(err, errStoreFailed) {
		t.Fatalf("Issue = %v, want the store error", err)
	}

	if secret.Reveal() != "" {
		t.Fatal("a failed issue returned a secret")
	}
}

// issueRequest returns a request of a live issue.
func issueRequest(t *testing.T, expiry time.Time) *jobtoken.IssueRequest {
	t.Helper()

	ns, unit, _ := testWorld(t)

	var j job.Job

	j.ID = "job-1"
	j.Spec.Unit = unit
	j.Spec.Steps = []job.Step{{
		Name: "apply", Role: job.RoleTool, Phase: job.PhaseApply, Args: []string{"tofu", "apply"},
		Env: nil, Dir: "", Timeout: 0, AllowFailure: false, ChangesResources: true, Outputs: nil,
	}}
	j.StartedBy = "alice"
	j.Phases = []job.Phase{{
		Kind: job.PhaseKind(""), Operation: "op-1", Claims: 0, Executor: "", Steps: nil, StateVersion: "",
		Plan: nil, Gates: nil, Approvals: nil, StartedAt: time.Time{}, FinishedAt: time.Time{},
	}}
	j.Current = 0

	var claim durable.Claim

	claim.Operation = "op-1"
	claim.Token = 1
	claim.ExpiresAt = testNow().Add(time.Minute)

	var req jobtoken.IssueRequest

	req.StarterID = "prn-alice"
	req.StarterSubject = "alice"
	req.Job = j
	req.Claim = claim
	req.ExpiresAt = expiry
	req.Fence = namespace.Fence{Namespace: ns, FailoverVersion: 1}

	return &req
}
