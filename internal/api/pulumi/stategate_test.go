package pulumi_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/api/pulumi"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/stack"
)

// errGuardDown is the error of a guard that cannot decide.
var errGuardDown = errors.New("store down")

// stateGuard answers each check with its decision and its error, and keeps
// the checks that it saw.
type stateGuard struct {
	mu       sync.Mutex
	decision gate.Decision
	err      error
	seen     []gate.StateWrite
}

func newStateGuard(result gate.Result, err error) *stateGuard {
	return &stateGuard{
		mu:       sync.Mutex{},
		decision: gate.Decision{Record: "", Result: result, Requirements: nil, Warnings: nil, AutoApply: false},
		err:      err,
		seen:     nil,
	}
}

func (g *stateGuard) GuardStateWrite(_ context.Context, w gate.StateWrite) (gate.Decision, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.seen = append(g.seen, w)

	return g.decision, g.err
}

func (g *stateGuard) checks() []gate.StateWrite {
	g.mu.Lock()
	defer g.mu.Unlock()

	return append([]gate.StateWrite(nil), g.seen...)
}

// routeRun is what one request to a guarded route did: the answer, whether
// the write reached the store, and the count of denied audit entries.
type routeRun struct {
	status int
	body   string
	wrote  bool
	denied int
}

// guardedRoute is one Pulumi route that meets state.write, what the guard
// must see for it, and a run of one request against a new handler with guard.
type guardedRoute struct {
	name    string
	pattern string
	op      gate.OperationKind
	action  identity.Action
	run     func(t *testing.T, guard stategate.Guard) routeRun
}

// stackPattern is the path of one stack in the route table.
const stackPattern = "/api/stacks/{namespace}/{project}/{stack}"

func guardedRoutes() []guardedRoute {
	return []guardedRoute{
		{
			name: "import", pattern: "POST " + stackPattern + "/import",
			op: gate.OperationStateImport, action: identity.ActionUnitWrite, run: runImport,
		},
		{
			name: "restore", pattern: "POST " + stackPattern + "/restore/{version}",
			op: gate.OperationRollback, action: identity.ActionUnitWrite, run: runRestore,
		},
		{
			name: "rename", pattern: "POST " + stackPattern + "/rename",
			op: gate.OperationRename, action: identity.ActionUnitManage, run: runRename,
		},
		{
			name: "delete", pattern: "DELETE " + stackPattern,
			op: gate.OperationDelete, action: identity.ActionUnitManage, run: runDelete,
		},
		{
			name: "cancel", pattern: "POST " + stackPattern + "/update/{update}/cancel",
			op: gate.OperationUnlock, action: identity.ActionUnitWrite, run: runCancel,
		},
	}
}

// guardedFixture returns an update fixture with guard as its state guard.
func guardedFixture(t *testing.T, guard stategate.Guard) updateFixture {
	t.Helper()

	return newUpdateFixtureWithAuthenticator(t, allowAll(), tokenAuthenticator{}, fixtureOptions{
		manual: nil, checker: nil, limit: 0, cutoff: 0, replayer: nil, guard: guard,
	})
}

// latestVersion returns the latest state version of the stack of f, or 0 when
// the stack has none.
func latestVersion(t *testing.T, f updateFixture) int64 {
	t.Helper()

	v, err := f.updates.LatestStateVersion(t.Context(), f.ref)
	if err != nil {
		return 0
	}

	return v
}

// deniedCount returns the count of the entries of f with the outcome denied.
func deniedCount(f updateFixture) int {
	f.audit.mu.Lock()
	defer f.audit.mu.Unlock()

	n := 0

	for i := range f.audit.entries {
		if f.audit.entries[i].Outcome == audit.OutcomeDenied {
			n++
		}
	}

	return n
}

func runImport(t *testing.T, guard stategate.Guard) routeRun {
	t.Helper()

	f := guardedFixture(t, guard)
	before := latestVersion(t, f)
	status, body := f.doUpdate(t, accessRequest(http.MethodPost, "/api/stacks/acme/web/prod/import",
		`{"version":3,"deployment":{"resources":[]}}`))

	return routeRun{status: status, body: body, wrote: latestVersion(t, f) != before, denied: deniedCount(f)}
}

func runRestore(t *testing.T, guard stategate.Guard) routeRun {
	t.Helper()

	f := guardedFixture(t, guard)
	seedRestoreVersion(t, f)
	before := latestVersion(t, f)
	status, body := f.doUpdate(t, accessRequest(http.MethodPost, "/api/stacks/acme/web/prod/restore/1", ""))

	return routeRun{status: status, body: body, wrote: latestVersion(t, f) != before, denied: deniedCount(f)}
}

func runCancel(t *testing.T, guard stategate.Guard) routeRun {
	t.Helper()

	f := guardedFixture(t, guard)

	u, err := f.updates.Create(t.Context(), f.ref,
		stack.Plan{Kind: stack.KindUpdate, DryRun: false, CreatedBy: "", RestoredFrom: 0, GitHead: ""})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, startErr := f.updates.Start(t.Context(), f.ref, u.ID); startErr != nil {
		t.Fatalf("start: %v", startErr)
	}

	status, body := f.doUpdate(t, accessRequest(http.MethodPost,
		"/api/stacks/acme/web/prod/update/"+u.ID.String()+"/cancel", ""))

	after, err := f.updates.Get(t.Context(), f.ref, u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}

	return routeRun{status: status, body: body, wrote: after.Status != stack.StatusRunning, denied: deniedCount(f)}
}

// stackRequest is one request to a stack route, and the read of its write
// from the stack store.
type stackRequest struct {
	method, target, body string
	wrote                func(*recordingStackStore) bool
}

// runStackRoute sends one request to a stack route of a handler with guard,
// and reads the writes from the stack store.
func runStackRoute(t *testing.T, guard stategate.Guard, sr stackRequest) routeRun {
	t.Helper()

	method, target, body, wrote := sr.method, sr.target, sr.body, sr.wrote

	entries := &memAuditStore{entries: nil, err: nil, mu: sync.Mutex{}}
	handler, stores := newGuardedEnforcingHandler(t, allowAll(), entries, guard)

	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "token test-token")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	return routeRun{
		status: rec.Code, body: rec.Body.String(), wrote: wrote(stores), denied: len(deniedEntries(entries)),
	}
}

func runRename(t *testing.T, guard stategate.Guard) routeRun {
	t.Helper()

	return runStackRoute(t, guard, stackRequest{
		method: http.MethodPost, target: "/api/stacks/acme/web/prod/rename",
		body:  `{"newName":"renamed","newProject":"web"}`,
		wrote: func(s *recordingStackStore) bool { return len(s.renamed) != 0 },
	})
}

func runDelete(t *testing.T, guard stategate.Guard) routeRun {
	t.Helper()

	return runStackRoute(t, guard, stackRequest{
		method: http.MethodDelete, target: "/api/stacks/acme/web/prod", body: "",
		wrote: func(s *recordingStackStore) bool { return len(s.deleted) != 0 || len(s.forced) != 0 },
	})
}

// pulumiError is the Pulumi error shape with the code of the catalog.
type pulumiError struct {
	Code      int    `json:"code"`
	Message   string `json:"message"`
	ErrorCode string `json:"errorCode"`
}

// TestStateWriteGuardAllows proves that each guarded route asks the guard once,
// with the operation kind, the action, the unit and the source of the route
// and with no state change, and that the write goes on when the guard allows
// it.
func TestStateWriteGuardAllows(t *testing.T) {
	t.Parallel()

	for _, rt := range guardedRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()

			guard := newStateGuard(gate.ResultAllow, nil)
			got := rt.run(t, guard)

			if got.status >= http.StatusBadRequest || !got.wrote || got.denied != 0 {
				t.Fatalf("run = %+v, want a success, a write and no denied entry", got)
			}

			checkGuardSaw(t, rt, guard.checks())
		})
	}
}

// checkGuardSaw checks that the guard saw one check, with the operation kind,
// the action, the unit and the source of rt, and with no state change.
func checkGuardSaw(t *testing.T, rt guardedRoute, seen []gate.StateWrite) {
	t.Helper()

	if len(seen) != 1 {
		t.Fatalf("guard checks = %d, want 1", len(seen))
	}

	c := seen[0]
	if c.Operation != rt.op || c.Action != rt.action || c.Source != gate.SourceCLI ||
		c.Unit.String() != "pulumi:acme/web/prod" || c.Principal.Subject != "tester" {
		t.Errorf("check = %+v, want operation %q, action %q, source cli, unit pulumi:acme/web/prod",
			c, rt.op, rt.action)
	}

	// The server does not read a Pulumi state for the gates: the state facts
	// stay not available, and no fact is invented.
	if c.State != nil || c.PlanLock {
		t.Errorf("check state = %+v, plan lock %v, want no state change and no plan lock", c.State, c.PlanLock)
	}
}

// guardAnswer is one answer of the guard, and the answer of the route to it.
type guardAnswer struct {
	name   string
	result gate.Result
	err    error
	status int
	code   string
	text   string
}

func guardAnswers() []guardAnswer {
	return []guardAnswer{
		{
			name: "deny gate", result: "", status: http.StatusForbidden, code: "gate.denied",
			err: &gate.RefusalError{
				Err: gate.ErrDenied, Record: "gdr-test", Messages: []string{"Writes are frozen.\r\nInjected: line"},
			},
			// The CR and the LF of the message become spaces.
			text: "Writes are frozen.  Injected: line (error gate.denied, decision record gdr-test)",
		},
		{
			name: "access limit", result: "", status: http.StatusForbidden, code: "gate.limited",
			err:  &gate.RefusalError{Err: gate.ErrLimited, Record: "gdr-limit", Messages: []string{"no-writes"}},
			text: "no-writes (error gate.limited, decision record gdr-limit)",
		},
		{
			name: "final gate", result: "", status: http.StatusForbidden, code: "gate.final",
			err:  &gate.RefusalError{Err: gate.ErrFinal, Record: "gdr-final", Messages: []string{"final"}},
			text: "decision record gdr-final",
		},
		{
			name: "guard error", result: "", err: errGuardDown, status: http.StatusForbidden,
			code: "gate.unavailable", text: stategate.GuardFailedText,
		},
		{
			name: "nil refusal", result: "", err: (*gate.RefusalError)(nil), status: http.StatusForbidden,
			code: "gate.unavailable", text: stategate.GuardFailedText,
		},
		{
			name: "unknown result", result: "maybe", err: nil, status: http.StatusForbidden,
			code: "gate.unavailable", text: stategate.UnknownResultText,
		},
		{
			name: "job token denied", result: "", err: identity.ErrDenied, status: http.StatusForbidden,
			code: "gate.denied", text: stategate.DeniedText,
		},
	}
}

// TestStateWriteGuardRefuses proves, for each guarded route and each refusal
// of the guard, that the route answers 403 in the Pulumi error shape with the
// code, the messages on one line and the decision record, that the write does
// not happen, and that the refusal has one denied audit entry. The guard is
// the only layer under test: it answers with hostile values directly.
func TestStateWriteGuardRefuses(t *testing.T) {
	t.Parallel()

	for _, rt := range guardedRoutes() {
		for _, ans := range guardAnswers() {
			t.Run(rt.name+"/"+ans.name, func(t *testing.T) {
				t.Parallel()

				checkRefused(t, ans, rt.run(t, newStateGuard(ans.result, ans.err)))
			})
		}
	}
}

// checkRefused checks that got is the refusal of ans: its status, no write,
// one denied entry, and the Pulumi error shape with the code and the text on
// one line.
func checkRefused(t *testing.T, ans guardAnswer, got routeRun) {
	t.Helper()

	if got.status != ans.status || got.wrote || got.denied != 1 {
		t.Fatalf("run = %+v, want status %d, no write and one denied entry", got, ans.status)
	}

	var e pulumiError
	if err := json.Unmarshal([]byte(got.body), &e); err != nil {
		t.Fatalf("decode %q: %v", got.body, err)
	}

	if e.Code != ans.status || e.ErrorCode != ans.code || !strings.Contains(e.Message, ans.text) ||
		strings.ContainsAny(e.Message, "\r\n") {
		t.Errorf("error = %+v, want code %d, errorCode %s and %q on one line", e, ans.status, ans.code, ans.text)
	}
}

// TestStateWriteGuardStoreUnavailable proves that a store that cannot take the
// decision record answers 503 in the Pulumi error shape, as every other write
// path does, and that the write does not happen.
func TestStateWriteGuardStoreUnavailable(t *testing.T) {
	t.Parallel()

	for _, rt := range guardedRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()

			got := rt.run(t, newStateGuard("", namespace.ErrUnavailable))

			if got.status != http.StatusServiceUnavailable || got.wrote || got.denied != 1 {
				t.Fatalf("run = %+v, want 503, no write and one denied entry", got)
			}

			var e pulumiError
			if err := json.Unmarshal([]byte(got.body), &e); err != nil || e.Code != http.StatusServiceUnavailable {
				t.Errorf("error = %+v (%v), want code 503", e, err)
			}
		})
	}
}

// TestStateWriteGuardRequired proves that a handler with no state guard does
// not start, and that the guard of Off with no reason does not start either.
func TestStateWriteGuardRequired(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		guard stategate.Guard
	}{
		{name: "nil guard", guard: nil},
		{name: "off with no reason", guard: stategate.Off(" ")},
		{name: "nil off guard", guard: (*stategate.OffGuard)(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if _, err := newHandlerWithGuard(t, tc.guard); err == nil {
				t.Fatal("NewHandler with no valid state guard = nil error, want an error")
			}
		})
	}
}

// newHandlerWithGuard builds a handler with guard as its state guard, and
// returns the error of NewHandler.
func newHandlerWithGuard(t *testing.T, guard stategate.Guard) (*pulumi.Handler, error) {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: homedNamespaceStore{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	stacks, err := stack.NewService(
		stack.Config{
			Store: stubStackStore{}, Namespaces: namespaces, Now: nil, State: nil, Retention: 0, ManualLocks: nil,
		},
	)
	if err != nil {
		t.Fatalf("stack service: %v", err)
	}

	return pulumi.NewHandler(pulumi.Config{
		Placer: nil,
		Stacks: stacks, Updates: newEnforcingUpdates(t, namespaces), Secrets: newTestSecrets(t, namespaces),
		Namespaces: namespaces, Authenticator: tokenAuthenticator{}, Authorizer: allowAll(), Sessions: nil,
		SessionPolicy: nil, AddressGate: nil, Logger: slog.New(slog.DiscardHandler), BaseURL: baseurl.Resolver{},
		MaxBodyBytesInFlight: 0, Gate: nil, Audit: newTestAudit(), OnAuditFailure: nil, DenialLimiter: nil,
		CheckpointDeltaCutoff: 0, DisablePulumiJournaling: false, StateGuard: guard,
	})
}

// updateCycle is the reason of the routes of the update cycle. The update meets
// state.write at its create in a later change: the effective value lists the
// write path pulumi.update as unchecked until then.
const updateCycle = "the update cycle: the write path pulumi.update, unchecked"

// routesWithNoStateWrite returns the routes of the Pulumi API that are not GET
// and that meet no state.write, with the reason.
func routesWithNoStateWrite() map[string]string {
	noState := "writes no state"

	return map[string]string{
		"POST /api/stacks/{namespace}/{project}":                        "creates a stack with no state",
		"PATCH " + stackPattern + "/tags":                               "changes the tags of the stack, " + noState,
		"POST " + stackPattern + "/encrypt":                             "encrypts a value, " + noState,
		"POST " + stackPattern + "/batch-encrypt":                       "encrypts values, " + noState,
		"POST " + stackPattern + "/decrypt":                             "decrypts a value, " + noState,
		"POST " + stackPattern + "/batch-decrypt":                       "decrypts values, " + noState,
		"POST " + stackPattern + "/decrypt/log-decryption":              "writes an audit entry, " + noState,
		"POST " + stackPattern + "/decrypt/log-batch-decryption":        "writes an audit entry, " + noState,
		"POST " + stackPattern + "/update":                              updateCycle,
		"POST " + stackPattern + "/refresh":                             updateCycle,
		"POST " + stackPattern + "/destroy":                             updateCycle,
		"POST " + stackPattern + "/preview":                             "creates a preview, which writes no state",
		"POST " + stackPattern + "/state/{version}/reveal":              "reveals the secrets of a state, " + noState,
		"POST " + stackPattern + "/update/{update}":                     updateCycle,
		"POST " + stackPattern + "/update/{update}/renew_lease":         updateCycle,
		"POST " + stackPattern + "/update/{update}/events/batch":        updateCycle,
		"POST " + stackPattern + "/update/{update}/complete":            updateCycle,
		"PATCH " + stackPattern + "/update/{update}/checkpoint":         updateCycle,
		"PATCH " + stackPattern + "/update/{update}/checkpointverbatim": updateCycle,
		"PATCH " + stackPattern + "/update/{update}/checkpointdelta":    updateCycle,
		"PATCH " + stackPattern + "/update/{update}/journalentries":     updateCycle,
	}
}

// TestEveryWriteRouteMeetsTheGates proves that each route of the Pulumi API
// that is not GET either meets state.write, with a case in guardedRoutes that
// the guard tests run, or is on the list of routes that meet no state.write,
// with the reason. A new write route with neither fails the test.
func TestEveryWriteRouteMeetsTheGates(t *testing.T) {
	t.Parallel()

	h, err := newHandlerWithGuard(t, newStateGuard(gate.ResultAllow, nil))
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	guarded := map[string]bool{}
	for _, rt := range guardedRoutes() {
		guarded[rt.pattern] = true
	}

	for _, route := range h.Routes() {
		key := route.Method + " " + route.Path
		if route.Method == http.MethodGet {
			continue
		}

		_, listed := routesWithNoStateWrite()[key]
		if guarded[key] == listed {
			t.Errorf("%s: meets state.write %v, listed with no state.write %v: want exactly one",
				key, guarded[key], listed)
		}
	}
}
