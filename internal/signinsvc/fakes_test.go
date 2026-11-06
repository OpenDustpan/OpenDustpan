package signinsvc_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/signinsvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

// errStoreStopped is the error of a store that does not answer.
var errStoreStopped = errors.New("the store does not answer")

// errDriver is an error of no known class.
var errDriver = errors.New("an error of a driver")

// testNow returns the time of the clock of the tests.
func testNow() time.Time { return time.Date(2026, time.November, 5, 12, 0, 0, 0, time.UTC) }

// world holds the fake ports of one test and what they saw.
type world struct {
	mu sync.Mutex

	// What the ports answer.
	principalErr   error
	principal      principal.Principal
	disableErr     error
	startErr       error
	auditErr       error
	groupsErr      error
	groupsResult   principal.GroupClaimResult
	withGroupsPort bool

	// What the ports saw.
	signInInputs []principal.SignInInput
	disabled     []string
	bumped       []string
	starts       []session.Start
	startEntries []audit.Entry
	entries      []audit.Entry
	claims       []principal.GroupClaims
	auditFailed  int
}

// newWorld returns the fake ports of a test. By default the principal prn-1
// exists and each port answers.
func newWorld() *world {
	var w world

	w.principal = principal.Principal{
		CreatedAt: testNow(), UpdatedAt: testNow(), DisabledAt: time.Time{}, ID: "prn-1", Subject: "corp:sub-1",
		DisplayName: "Alice", Kind: principal.KindPerson, Source: principal.SourceOIDC, Namespace: "", GroupCount: 0,
		BreakGlass: false,
	}

	return &w
}

func (w *world) SignInPrincipal(
	_ context.Context, _ audit.Entry, in principal.SignInInput,
) (principal.Principal, bool, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.signInInputs = append(w.signInInputs, in)

	return w.principal, false, w.principalErr
}

func (w *world) DisablePrincipal(_ context.Context, id string, _ audit.Entry) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.disableErr != nil {
		return w.disableErr
	}

	w.disabled = append(w.disabled, id)

	return nil
}

func (w *world) BumpGeneration(
	_ context.Context, id string, _ session.BumpCause, _ uint64, _ audit.Entry,
) (uint64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.bumped = append(w.bumped, id)

	return 2, nil
}

func (w *world) Start(_ context.Context, e audit.Entry, in session.Start) (session.Session, session.Secret, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.startErr != nil {
		return session.Session{}, session.Secret{}, w.startErr
	}

	w.starts = append(w.starts, in)
	w.startEntries = append(w.startEntries, e)

	secret, err := session.NewSecret()
	if err != nil {
		return session.Session{}, session.Secret{}, err
	}

	return session.Session{
		CreatedAt: testNow(), LastSeenAt: testNow(), AuthTime: in.AuthTime, EndedAt: time.Time{}, ID: "ses-1",
		PrincipalID: in.PrincipalID, Provider: in.Provider, EndReason: "", SourceIP: in.SourceIP,
		UserAgent: in.UserAgent, Generation: in.Generation,
	}, secret, nil
}

func (*world) Now() time.Time { return testNow() }

func (w *world) AppendServer(_ context.Context, e audit.Entry) (audit.Entry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.auditErr != nil {
		return audit.Entry{}, w.auditErr
	}

	w.entries = append(w.entries, e)

	return e, nil
}

func (w *world) ApplyGroupClaims(
	_ context.Context, _ audit.Entry, c principal.GroupClaims,
) (principal.GroupClaimResult, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.claims = append(w.claims, c)

	return w.groupsResult, w.groupsErr
}

// service returns a Service over the fakes of w.
func (w *world) service(t *testing.T) *signinsvc.Service {
	t.Helper()

	var groups principal.GroupClaimApplier
	if w.withGroupsPort {
		groups = w
	}

	svc, err := signinsvc.New(signinsvc.Config{
		Principals: w, Disabler: w, Generations: w, Sessions: w, Audit: w, Groups: groups,
		States: nil, Logger: slog.New(slog.DiscardHandler), OnAuditFailure: func() { w.auditFailed++ },
	})
	if err != nil {
		t.Fatalf("New() = %v", err)
	}

	return svc
}

// reasonOf returns the value of the change reason of e, or "".
func reasonOf(e *audit.Entry) string { return changeOf(e, "reason") }

// changeOf returns the new value of the change key of e, or "".
func changeOf(e *audit.Entry, key string) string {
	for _, c := range e.Changes {
		if c.Key == key {
			return c.New
		}
	}

	return ""
}

// callerEntry returns the entry of the caller of a console request.
func callerEntry() audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: "", IDPSubject: "", TokenID: "", Action: "", Object: "",
		Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: "192.0.2.1", UserAgent: "test",
		Channel: audit.ChannelConsole, Kind: "", Outcome: "", RequestID: "", ObjectID: "", PrevHash: "", Hash: "",
		Sequence: 0, FenceVersion: 0, Reason: "", PrincipalKind: "", JobID: "",
	}
}

// oidcAssertion returns a valid Assertion of the OpenID Connect provider corp.
func oidcAssertion() session.Assertion {
	return session.Assertion{
		AuthTime: testNow(), ProviderID: "corp", PrincipalID: "", Subject: "sub-1", UserName: "alice", Email: "",
		Method: session.MethodOIDC, Names: []string{"Alice"}, Groups: nil, Factors: nil, EmailVerified: false,
		GroupsPresent: false, AuthTimeKnown: true,
	}
}

// disabledAssertion returns the Assertion that a method returns with
// ErrSubjectDisabled: the provider ID, the subject and the kind only.
func disabledAssertion() session.Assertion {
	return session.Assertion{
		AuthTime: time.Time{}, ProviderID: "corp", PrincipalID: "", Subject: "sub-1", UserName: "", Email: "",
		Method: session.MethodOIDC, Names: nil, Groups: nil, Factors: nil, EmailVerified: false,
		GroupsPresent: false, AuthTimeKnown: false,
	}
}

// oidcProvider returns the OpenID Connect provider corp of a route.
func oidcProvider() signinsvc.Provider {
	return signinsvc.Provider{ID: "corp", Kind: session.MethodOIDC, AutoCreate: true, Stored: false}
}

// requestOf returns a Request for the provider p and the Assertion a.
func requestOf(p signinsvc.Provider, a session.Assertion, methodErr error) *signinsvc.Request {
	return &signinsvc.Request{MethodErr: methodErr, Entry: callerEntry(), Provider: p, Assertion: a}
}
