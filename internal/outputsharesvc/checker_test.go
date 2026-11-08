package outputsharesvc_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/outputsharesvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/outputshare"
)

// errPort is the error of a port that fails.
var errPort = errors.New("port failed")

// checkerRig is a Checker over fakes that count each port call.
type checkerRig struct {
	checker *outputsharesvc.Checker
	authz   *scriptedAuthz
	states  *scriptedStates
	audit   *recordedAudit
	ns      *scriptedNamespaces
}

// scriptedAuthz answers each action with the error that the test set. An
// action with no error is allowed. It counts the calls.
type scriptedAuthz struct {
	mu    sync.Mutex
	errs  map[identity.Action]error
	calls []identity.Action
}

func (a *scriptedAuthz) Authorize(
	_ context.Context, _ identity.Principal, action identity.Action, _ identity.Object,
) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.calls = append(a.calls, action)

	return a.errs[action]
}

// scriptedStates answers the Latest call with the state and error that the
// test set.
type scriptedStates struct {
	mu    sync.Mutex
	state outputshare.State
	err   error
	calls int
}

func (s *scriptedStates) Latest(_ context.Context, _ identity.Object) (outputshare.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++

	return s.state, s.err
}

// recordedAudit keeps the entries that the checker appends.
type recordedAudit struct {
	audit.Store

	mu      sync.Mutex
	entries []audit.Entry
	fences  []namespace.Fence
	err     error
}

func (a *recordedAudit) Append(_ context.Context, fence namespace.Fence, e audit.Entry) (audit.Entry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.err != nil {
		return audit.Entry{}, a.err
	}

	a.entries = append(a.entries, e)
	a.fences = append(a.fences, fence)

	return e, nil
}

// scriptedNamespaces knows the namespace acme and no other.
type scriptedNamespaces struct {
	err error
}

func (n *scriptedNamespaces) Get(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	if n.err != nil {
		return namespace.Namespace{}, n.err
	}

	if name != "acme" {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.UnixMilli(1).UTC(), Name: name, PrimaryInstallation: "eu-west",
		FailoverVersion: 7, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}, nil
}

const latestID = "sv-latest"

func newCheckerRig(t *testing.T) *checkerRig {
	t.Helper()

	rig := &checkerRig{
		checker: nil,
		authz:   &scriptedAuthz{mu: sync.Mutex{}, errs: map[identity.Action]error{}, calls: nil},
		states: &scriptedStates{
			mu: sync.Mutex{}, state: outputshare.State{ID: latestID, ClientEncrypted: false}, err: nil, calls: 0,
		},
		audit: &recordedAudit{Store: nil, mu: sync.Mutex{}, entries: nil, fences: nil, err: nil},
		ns:    &scriptedNamespaces{err: nil},
	}

	checker, err := outputsharesvc.NewChecker(outputsharesvc.CheckerConfig{
		Authorizer: rig.authz, States: rig.states, Namespaces: rig.ns, Audit: rig.audit,
		Logger: slog.New(slog.DiscardHandler),
		Now:    func() time.Time { return time.UnixMilli(1_790_000_000_000) },
	})
	if err != nil {
		t.Fatalf("NewChecker: %v", err)
	}

	rig.checker = checker

	return rig
}

func producerAcme() outputshare.Placement {
	unit, err := identity.UnitObject("opentofu", "acme", "prod")
	if err != nil {
		panic(err)
	}

	return outputshare.Placement{Unit: unit.WithUnitID("ws-1"), Collection: identity.Object{}}
}

func personRequest(latest string, secrets bool) outputshare.ReadRequest {
	return outputshare.ReadRequest{
		Caller: identity.Principal{
			Subject: "alice", DisplayName: "", Admin: false, TokenID: "tok-1", TokenExpiresAt: time.Time{},
			TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: identity.KindPerson,
			Credential: identity.CredentialAccessToken, JobID: "",
			JobUnit: identity.Object{},
		},
		Producer:   producerAcme(),
		Attachment: outputshare.AttachmentRef{Attachment: "", Profile: "", Block: "", Revision: 0},
		Latest:     latest,
		Secrets:    secrets,
	}
}

// decisionCase is one row of TestCheckerDecisions.
type decisionCase struct {
	name string
	// authzErrs holds the error of the authorizer for each action. An action
	// with no entry is allowed.
	authzErrs map[identity.Action]error
	// state and stateErr are the answer of the states port.
	state    outputshare.State
	stateErr error
	request  outputshare.ReadRequest
	// wantCheck is the check that denies, or "" for an allowed read.
	wantCheck   string
	wantErr     error
	wantSecrets bool
}

// row returns a case for a read of the latest state by a person with every
// grant, and lets edit change it.
func row(name string, edit func(*decisionCase)) decisionCase {
	c := decisionCase{
		name: name, authzErrs: map[identity.Action]error{},
		state: outputshare.State{ID: latestID, ClientEncrypted: false}, stateErr: nil,
		request:   personRequest(latestID, false),
		wantCheck: "", wantErr: nil, wantSecrets: true,
	}
	edit(&c)

	return c
}

// denies marks the case as a denial by check with the sentinel.
func (c *decisionCase) denies(check string, sentinel error) {
	c.wantCheck, c.wantErr = check, sentinel
}

// withoutAction makes the authorizer refuse the action with err.
func (c *decisionCase) withoutAction(action identity.Action, err error) {
	c.authzErrs[action] = err
}

// decisionCases returns the rows of TestCheckerDecisions.
func decisionCases() []decisionCase {
	denied := identity.ErrDenied
	encrypted := outputshare.State{ID: latestID, ClientEncrypted: true}
	noRead := func(c *decisionCase) { c.withoutAction(identity.ActionUnitRead, denied) }

	return []decisionCase{
		row("outputs grant reads", func(c *decisionCase) {
			c.withoutAction(identity.ActionUnitReadSecrets, denied)
			c.wantSecrets = false
		}),
		row("outputs and secrets grants give a decision with secrets", func(c *decisionCase) {
			c.request = personRequest(latestID, true)
		}),
		row("no outputs grant", func(c *decisionCase) {
			c.withoutAction(identity.ActionUnitReadOutputs, denied)
			c.denies(outputshare.CheckGrant, identity.ErrDenied)
		}),
		row("unit:read alone is not enough", func(c *decisionCase) {
			c.withoutAction(identity.ActionUnitReadOutputs, denied)
			c.denies(outputshare.CheckGrant, identity.ErrDenied)
		}),
		row("a failed authorizer denies, it does not allow", func(c *decisionCase) {
			c.withoutAction(identity.ActionUnitReadOutputs, errPort)
			c.denies(outputshare.CheckGrant, identity.ErrDenied)
		}),
		row("a secret output without unit:read-secrets", func(c *decisionCase) {
			c.request = personRequest(latestID, true)
			c.withoutAction(identity.ActionUnitReadSecrets, denied)
			c.denies(outputshare.CheckSecrets, identity.ErrDenied)
		}),
		row("no secret output needed, no unit:read-secrets: the decision leaves secrets out", func(c *decisionCase) {
			c.withoutAction(identity.ActionUnitReadSecrets, denied)
			c.wantSecrets = false
		}),
		row("an empty latest state ID", func(c *decisionCase) {
			c.request = personRequest("", false)
			c.denies(outputshare.CheckLatest, identity.ErrDenied)
		}),
		row("an older state ID", func(c *decisionCase) {
			c.request = personRequest("sv-older", false)
			c.denies(outputshare.CheckLatest, identity.ErrDenied)
		}),
		row("a unit with no state", func(c *decisionCase) {
			c.state = outputshare.State{ID: "", ClientEncrypted: false}
			c.denies(outputshare.CheckLatest, identity.ErrDenied)
		}),
		row("the states port fails", func(c *decisionCase) {
			c.stateErr = errPort
			c.denies(outputshare.CheckLatest, identity.ErrDenied)
		}),
		row("the states port fails with an error that wraps ErrDenied", func(c *decisionCase) {
			c.stateErr = errors.Join(errPort, identity.ErrDenied)
			c.denies(outputshare.CheckLatest, identity.ErrDenied)
		}),
		row("an unknown producer", func(c *decisionCase) {
			c.stateErr = outputshare.ErrUnknownProducer
			c.denies(outputshare.CheckProducer, outputshare.ErrUnknownProducer)
		}),
		row("an unknown producer with no grant is still unknown", func(c *decisionCase) {
			c.stateErr = outputshare.ErrUnknownProducer
			c.withoutAction(identity.ActionUnitReadOutputs, denied)
			c.denies(outputshare.CheckProducer, outputshare.ErrUnknownProducer)
		}),
		row("a client-encrypted state, outputs only", func(c *decisionCase) {
			c.state = encrypted
			noRead(c)
			c.denies(outputshare.CheckClientEncrypted, outputshare.ErrClientEncrypted)
		}),
		row("a client-encrypted state, unit:read holder reads", func(c *decisionCase) { c.state = encrypted }),
		row("a client-encrypted state and no grant: the grant check comes first", func(c *decisionCase) {
			c.state = encrypted
			c.withoutAction(identity.ActionUnitReadOutputs, denied)
			noRead(c)
			c.denies(outputshare.CheckGrant, identity.ErrDenied)
		}),
		row("a client-encrypted state and a stale ID: the latest-state check comes first", func(c *decisionCase) {
			c.state = encrypted
			c.request = personRequest("sv-older", false)
			noRead(c)
			c.denies(outputshare.CheckLatest, identity.ErrDenied)
		}),
		row("a client-encrypted state, the check of unit:read fails", func(c *decisionCase) {
			c.state = encrypted
			c.withoutAction(identity.ActionUnitRead, errPort)
			c.denies(outputshare.CheckClientEncrypted, outputshare.ErrClientEncrypted)
		}),
		row("a caller with no subject", func(c *decisionCase) {
			c.request.Caller.Subject = ""
			c.denies(outputshare.CheckCaller, identity.ErrDenied)
		}),
		row("a job token waits for the job path", func(c *decisionCase) {
			c.request.Caller.Credential = identity.CredentialJobToken
			c.request.Caller.JobID = "job-1"
			c.denies(outputshare.CheckCaller, identity.ErrDenied)
		}),
	}
}

// TestCheckerDecisions proves each check of a read by a principal alone, as if
// the other layers had failed, and the order of the checks. Every case makes
// the same port calls, whichever check denies.
func TestCheckerDecisions(t *testing.T) {
	t.Parallel()

	for _, tt := range decisionCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newCheckerRig(t)
			rig.authz.errs = tt.authzErrs
			rig.states.state = tt.state
			rig.states.err = tt.stateErr

			dec, err := rig.checker.CheckRead(t.Context(), tt.request)

			wantPortCalls(t, rig)

			if tt.wantCheck == "" {
				wantAllowedRead(t, rig, dec, err, outputshare.Decision{
					Kind: outputshare.ReadByPrincipal, Rule: "", Secrets: tt.wantSecrets,
				})

				return
			}

			wantDenial(t, err, tt.wantCheck, tt.wantErr)

			if dec != (outputshare.Decision{Kind: "", Rule: "", Secrets: false}) {
				t.Errorf("a denial returned the decision %+v, want the zero decision", dec)
			}
		})
	}
}

// wantAllowedRead proves an allowed read: the decision, and no audit entry.
func wantAllowedRead(t *testing.T, rig *checkerRig, dec outputshare.Decision, err error, want outputshare.Decision) {
	t.Helper()

	if err != nil {
		t.Fatalf("CheckRead = %v, want an allowed read", err)
	}

	if dec != want {
		t.Errorf("Decision = %+v, want %+v", dec, want)
	}

	if len(rig.audit.entries) != 0 {
		t.Errorf("an allowed read wrote %d audit entries, want 0", len(rig.audit.entries))
	}
}

// wantPortCalls proves that the checker made every port call, so that a denial
// by an early check takes the same work as an allowed read.
func wantPortCalls(t *testing.T, rig *checkerRig) {
	t.Helper()

	if rig.states.calls != 1 {
		t.Errorf("States.Latest calls = %d, want 1", rig.states.calls)
	}

	want := []identity.Action{identity.ActionUnitReadOutputs, identity.ActionUnitReadSecrets, identity.ActionUnitRead}
	if len(rig.authz.calls) != len(want) {
		t.Fatalf("authorizer calls = %v, want %v", rig.authz.calls, want)
	}

	for i := range want {
		if rig.authz.calls[i] != want[i] {
			t.Errorf("authorizer call %d = %s, want %s", i, rig.authz.calls[i], want[i])
		}
	}
}

// wantDenial proves that err is a *DeniedError of the check that unwraps to the
// one sentinel.
func wantDenial(t *testing.T, err error, check string, sentinel error) {
	t.Helper()

	var denied *outputshare.DeniedError
	if !errors.As(err, &denied) {
		t.Fatalf("CheckRead = %v, want a *DeniedError", err)
	}

	if denied.Check != check {
		t.Errorf("check = %q, want %q", denied.Check, check)
	}

	for _, other := range []error{identity.ErrDenied, outputshare.ErrUnknownProducer, outputshare.ErrClientEncrypted} {
		if got, want := errors.Is(err, other), errors.Is(other, sentinel); got != want {
			t.Errorf("errors.Is(err, %v) = %v, want %v", other, got, want)
		}
	}

	if errors.Is(err, errPort) {
		t.Error("the denial unwraps to the cause of a port, want the sentinel only")
	}
}

// TestCheckerRefusesABadRequest proves the shape checks: a request that is
// not valid returns ErrInvalid, makes no port call and writes no entry.
func TestCheckerRefusesABadRequest(t *testing.T) {
	t.Parallel()

	namespaceObject := identity.NamespaceObject("acme")
	collection := identity.CollectionObject("globex", "apps").WithCollectionID("col-1")

	tests := []struct {
		name   string
		mutate func(*outputshare.ReadRequest)
	}{
		{"a producer that is not a unit", func(r *outputshare.ReadRequest) {
			r.Producer = outputshare.Placement{Unit: namespaceObject, Collection: identity.Object{}}
		}},
		{"an empty producer", func(r *outputshare.ReadRequest) {
			r.Producer = outputshare.Placement{Unit: identity.Object{}, Collection: identity.Object{}}
		}},
		{"a collection of another namespace", func(r *outputshare.ReadRequest) { r.Producer.Collection = collection }},
		{"an attachment reference on a read by a principal", func(r *outputshare.ReadRequest) {
			r.Attachment = outputshare.AttachmentRef{Attachment: "att-1", Profile: "prof-1", Block: "b", Revision: 1}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rig := newCheckerRig(t)
			req := personRequest(latestID, false)
			tt.mutate(&req)

			if _, err := rig.checker.CheckRead(t.Context(), req); !errors.Is(err, outputshare.ErrInvalid) {
				t.Fatalf("CheckRead = %v, want ErrInvalid", err)
			}

			if rig.states.calls != 0 || len(rig.authz.calls) != 0 || len(rig.audit.entries) != 0 {
				t.Error("a bad request made port calls or wrote entries")
			}
		})
	}
}

// TestCheckerAuditsADenialInTheProducerNamespace proves that one entry in the namespace of the producer.
func TestCheckerAuditsADenialInTheProducerNamespace(t *testing.T) {
	t.Parallel()

	rig := newCheckerRig(t)
	rig.authz.errs = map[identity.Action]error{identity.ActionUnitReadOutputs: identity.ErrDenied}

	_, err := rig.checker.CheckRead(t.Context(), personRequest(latestID, false))
	wantDenial(t, err, outputshare.CheckGrant, identity.ErrDenied)

	if len(rig.audit.entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(rig.audit.entries))
	}

	entry := rig.audit.entries[0]
	if entry.Namespace != "acme" || entry.Subject != "alice" || entry.TokenID != "tok-1" ||
		entry.Action != string(identity.ActionUnitReadOutputs) || entry.Object != "opentofu:acme/prod" ||
		entry.ObjectID != "ws-1" || entry.Outcome != audit.OutcomeDenied ||
		entry.Reason != "read of outputs denied by the check grant" {
		t.Errorf("entry = %+v", entry)
	}

	if err := entry.Validate(); err != nil {
		t.Errorf("entry does not validate: %v", err)
	}

	if rig.audit.fences[0].FailoverVersion != 7 {
		t.Errorf("fence version = %d, want 7", rig.audit.fences[0].FailoverVersion)
	}
}

// TestCheckerKeepsTheAnswerWhenTheAuditFails proves that a failed append does not change the answer.
func TestCheckerKeepsTheAnswerWhenTheAuditFails(t *testing.T) {
	t.Parallel()

	rig := newCheckerRig(t)
	rig.audit.err = errPort
	rig.authz.errs = map[identity.Action]error{identity.ActionUnitReadOutputs: identity.ErrDenied}

	_, err := rig.checker.CheckRead(t.Context(), personRequest(latestID, false))
	wantDenial(t, err, outputshare.CheckGrant, identity.ErrDenied)
}

// TestCheckerWritesNoEntryForAMissingNamespace proves that a namespace that does not exist gets no entry.
func TestCheckerWritesNoEntryForAMissingNamespace(t *testing.T) {
	t.Parallel()

	rig := newCheckerRig(t)
	rig.states.err = outputshare.ErrUnknownProducer

	req := personRequest(latestID, false)

	unit, err := identity.UnitObject("opentofu", "ghost", "prod")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	req.Producer = outputshare.Placement{Unit: unit, Collection: identity.Object{}}

	_, err = rig.checker.CheckRead(t.Context(), req)
	wantDenial(t, err, outputshare.CheckProducer, outputshare.ErrUnknownProducer)

	if len(rig.audit.entries) != 0 {
		t.Errorf("entries = %d, want 0 for a namespace that does not exist", len(rig.audit.entries))
	}
}

// TestCheckerKeepsTheAnswerWhenTheNamespaceStoreFails proves that a namespace store that fails does not change the
// answer.
func TestCheckerKeepsTheAnswerWhenTheNamespaceStoreFails(t *testing.T) {
	t.Parallel()

	rig := newCheckerRig(t)
	rig.ns.err = errPort
	rig.states.err = outputshare.ErrUnknownProducer

	_, err := rig.checker.CheckRead(t.Context(), personRequest(latestID, false))
	wantDenial(t, err, outputshare.CheckProducer, outputshare.ErrUnknownProducer)
}

// TestCheckerWritesNoEntryForACallerWithNoSubject proves that a caller with no subject gets no entry.
func TestCheckerWritesNoEntryForACallerWithNoSubject(t *testing.T) {
	t.Parallel()

	rig := newCheckerRig(t)
	req := personRequest(latestID, false)
	req.Caller.Subject = ""

	_, err := rig.checker.CheckRead(t.Context(), req)
	wantDenial(t, err, outputshare.CheckCaller, identity.ErrDenied)

	if len(rig.audit.entries) != 0 {
		t.Errorf("entries = %d, want 0", len(rig.audit.entries))
	}
}

// TestNewCheckerNeedsItsPorts proves that a missing port fails the build of
// the checker.
func TestNewCheckerNeedsItsPorts(t *testing.T) {
	t.Parallel()

	rig := newCheckerRig(t)
	full := outputsharesvc.CheckerConfig{
		Authorizer: rig.authz, States: rig.states, Namespaces: rig.ns, Audit: rig.audit,
		Logger: slog.New(slog.DiscardHandler), Now: nil,
	}

	tests := map[string]func(*outputsharesvc.CheckerConfig){
		"authorizer": func(c *outputsharesvc.CheckerConfig) { c.Authorizer = nil },
		"states":     func(c *outputsharesvc.CheckerConfig) { c.States = nil },
		"namespaces": func(c *outputsharesvc.CheckerConfig) { c.Namespaces = nil },
		"audit":      func(c *outputsharesvc.CheckerConfig) { c.Audit = nil },
		"logger":     func(c *outputsharesvc.CheckerConfig) { c.Logger = nil },
	}

	for name, drop := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg := full
			drop(&cfg)

			if _, err := outputsharesvc.NewChecker(cfg); !errors.Is(err, outputsharesvc.ErrMissingDependency) {
				t.Fatalf("NewChecker without %s = %v, want ErrMissingDependency", name, err)
			}
		})
	}
}

// TestToolStatesRoutesByTool proves the router: a tool with no entry and an
// object that is not a unit.
func TestToolStatesRoutesByTool(t *testing.T) {
	t.Parallel()

	inner := &scriptedStates{
		mu: sync.Mutex{}, state: outputshare.State{ID: "x", ClientEncrypted: false}, err: nil, calls: 0,
	}
	states := outputsharesvc.ToolStates{"opentofu": inner}

	tofu, err := identity.UnitObject("opentofu", "acme", "prod")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	pulumi, err := identity.UnitObject("pulumi", "acme", "web/prod")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	if got, err := states.Latest(t.Context(), tofu); err != nil || got.ID != "x" {
		t.Errorf("Latest(opentofu) = %+v, %v; want the state of the tool", got, err)
	}

	if _, err := states.Latest(t.Context(), pulumi); !errors.Is(err, outputshare.ErrUnknownProducer) {
		t.Errorf("Latest(pulumi) = %v, want ErrUnknownProducer", err)
	}

	if _, err := states.Latest(t.Context(), identity.NamespaceObject("acme")); !errors.Is(err, outputshare.ErrInvalid) {
		t.Errorf("Latest(namespace) = %v, want ErrInvalid", err)
	}
}
