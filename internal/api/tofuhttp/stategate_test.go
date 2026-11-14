package tofuhttp_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/api/tofuhttp"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// errGuardDown is the error of a guard that cannot decide.
var errGuardDown = errors.New("store down")

// errUnknownSentinel is a refusal sentinel that the gate package does not
// know.
var errUnknownSentinel = errors.New("a sentinel of another version")

// fakeGuard answers each check with its decision and its error, and keeps
// the checks it saw.
type fakeGuard struct {
	mu       sync.Mutex
	decision gate.Decision
	err      error
	seen     []gate.StateWrite
}

func newFakeGuard(result gate.Result, err error) *fakeGuard {
	return &fakeGuard{
		mu: sync.Mutex{},
		decision: gate.Decision{
			Record: "gdr-test", Result: result, Requirements: nil, Warnings: nil, AutoApply: false,
		},
		err:  err,
		seen: nil,
	}
}

func (g *fakeGuard) GuardStateWrite(_ context.Context, w gate.StateWrite) (gate.Decision, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.seen = append(g.seen, w)

	return g.decision, g.err
}

func (g *fakeGuard) checks() []gate.StateWrite {
	g.mu.Lock()
	defer g.mu.Unlock()

	return append([]gate.StateWrite(nil), g.seen...)
}

// guardedUnits counts the state versions that reach the workspace port. The
// locks and the force-unlocks that reach it are in lockJobs and forced of
// fakeUnits.
type guardedUnits struct {
	*fakeUnits

	mu     sync.Mutex
	writes int
	// acceptAll makes WriteState accept each write with no lock check, as if
	// the lock check of the domain failed.
	acceptAll bool
}

func (c *guardedUnits) WriteState(
	ctx context.Context, id workspace.ID, in workspace.StreamInput, auth workspace.StreamAuthority, body io.Reader,
) (workspace.StateVersion, error) {
	c.mu.Lock()
	c.writes++
	accept := c.acceptAll
	c.mu.Unlock()

	if accept {
		if _, err := io.Copy(io.Discard, body); err != nil {
			return workspace.StateVersion{}, fmt.Errorf("drain body: %w", err)
		}

		var none workspace.StateVersion

		return none, nil
	}

	return c.fakeUnits.WriteState(ctx, id, in, auth, body)
}

// reached returns the number of locks, force-unlocks and state versions that
// reached the workspace port.
func (c *guardedUnits) reached() int {
	c.mu.Lock()
	writes := c.writes
	c.mu.Unlock()

	c.fakeUnits.mu.Lock()
	defer c.fakeUnits.mu.Unlock()

	return writes + len(c.lockJobs) + len(c.forced)
}

// guardedHandler is a handler with a state guard, and the stores it uses.
type guardedHandler struct {
	h     *tofuhttp.Handler
	units *guardedUnits
	audit *testAudit
	guard *fakeGuard
}

// TestNilGuardDoesNotStart checks the start-time step: a handler with no
// state guard is an error, never a handler that checks no write.
func TestNilGuardDoesNotStart(t *testing.T) {
	t.Parallel()

	h, err := tofuhttp.NewHandler(tofuhttp.Config{
		Authenticator:  tokenAuth{},
		Authorizer:     userAuthz{},
		Logger:         slog.New(slog.DiscardHandler),
		Workspaces:     newFakeUnits(true),
		Namespaces:     fakeNamespaces{},
		Audit:          newTestAudit(),
		Gate:           nil,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		StateGuard:     nil,
		Placer:         testPlacer(),
	})
	if h != nil || !errors.Is(err, tofuhttp.ErrMissingDependency) || !errors.Is(err, stategate.ErrNoGuard) {
		t.Fatalf("NewHandler(no guard) = %v, %v, want ErrMissingDependency and stategate.ErrNoGuard", h, err)
	}
}

// newGuardedHandler returns a handler over a unit that exists, with guard.
func newGuardedHandler(t *testing.T, guard *fakeGuard) guardedHandler {
	t.Helper()

	units := &guardedUnits{fakeUnits: newFakeUnits(true), mu: sync.Mutex{}, writes: 0, acceptAll: false}
	entries := newTestAudit()

	h, err := tofuhttp.NewHandler(tofuhttp.Config{
		Authenticator:  tokenAuth{},
		Authorizer:     userAuthz{},
		Logger:         slog.New(slog.DiscardHandler),
		Workspaces:     units,
		Namespaces:     fakeNamespaces{},
		Audit:          entries,
		Gate:           nil,
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		StateGuard:     guard,
		Placer:         testPlacer(),
	})
	if err != nil {
		t.Fatalf("new handler: %v", err)
	}

	return guardedHandler{h: h, units: units, audit: entries, guard: guard}
}

// stateWriteRoute is one write of the http backend that meets state.write:
// the request, how to prepare the unit, and the check that the guard must see.
type stateWriteRoute struct {
	name    string
	method  string
	target  string
	body    string
	token   string
	prepare func(u *fakeUnits)
	op      gate.OperationKind
	action  identity.Action
	// okStatus is the status of the write when the guard lets it go on.
	okStatus int
}

// lockedBy sets the lock of the unit to id, held by holder.
func lockedBy(id, holder string) func(u *fakeUnits) {
	return func(u *fakeUnits) {
		u.mu.Lock()
		defer u.mu.Unlock()

		u.ws.Lock = workspace.Lock{LockedAt: u.ws.CreatedAt, ID: id, Holder: holder, Reason: ""}
	}
}

// stateWriteRoutes returns each write of the http backend that meets
// state.write.
func stateWriteRoutes() []stateWriteRoute {
	state := `{"version":4,"serial":1,"lineage":"L","outputs":{},"resources":[]}`

	return []stateWriteRoute{
		{
			name: "LOCK", method: "LOCK", target: unitPath, body: lockBody(lockOne, "alice"), token: adminToken,
			prepare: func(*fakeUnits) {}, op: gate.OperationLock, action: identity.ActionUnitWrite,
			okStatus: http.StatusOK,
		},
		{
			name: "force-unlock", method: "UNLOCK", target: unitPath, body: lockBody("lock-carol", "carol"),
			token: adminToken, prepare: lockedBy("lock-carol", "carol"), op: gate.OperationUnlock,
			action: identity.ActionUnitWriteForced, okStatus: http.StatusOK,
		},
		{
			name: "state version with no lock", method: http.MethodPost, target: unitPath, body: state,
			token: adminToken, prepare: func(*fakeUnits) {}, op: gate.OperationUnlockedWrite,
			action: identity.ActionUnitWriteUnlocked, okStatus: http.StatusOK,
		},
	}
}

// guardOutcome is one answer of the guard and what the handler must make of
// it.
type guardOutcome struct {
	name     string
	result   gate.Result
	err      error
	status   int
	passes   bool
	wantText []string
}

// guardOutcomes returns each kind of answer of the guard.
func guardOutcomes() []guardOutcome {
	refusal := func(sentinel error, msg string) error {
		return &gate.RefusalError{Err: sentinel, Record: "gdr-test", Messages: []string{msg}}
	}

	return []guardOutcome{
		{name: "allow", result: gate.ResultAllow, err: nil, status: 0, passes: true, wantText: nil},
		{name: "warn", result: gate.ResultWarn, err: nil, status: 0, passes: true, wantText: nil},
		{
			name: "deny", result: gate.ResultAllow, err: refusal(gate.ErrDenied, "Writes are frozen."),
			status: http.StatusForbidden, passes: false,
			wantText: []string{"Writes are frozen.", "gate.denied", "decision record gdr-test"},
		},
		{
			name: "access limit", result: gate.ResultAllow, err: refusal(gate.ErrLimited, "no-writes"),
			status: http.StatusForbidden, passes: false, wantText: []string{"no-writes", "gate.limited"},
		},
		{
			name: "gate in error", result: gate.ResultAllow, err: refusal(gate.ErrRuleFailed, "the gate failed"),
			status: http.StatusForbidden, passes: false, wantText: []string{"the gate failed", "gate.rule-failed"},
		},
		{
			name: "engine down", result: gate.ResultAllow, err: refusal(gate.ErrUnavailable, "engine down"),
			status: http.StatusForbidden, passes: false, wantText: []string{"engine down", "gate.unavailable"},
		},
		{
			name: "guard error fails closed", result: gate.ResultAllow, err: errGuardDown,
			status: http.StatusForbidden, passes: false,
			wantText: []string{"could not decide", "gate.unavailable"},
		},
		{
			name: "job token denied", result: gate.ResultAllow,
			err:    fmt.Errorf("%w: the phase writes only the lock of its plan", identity.ErrDenied),
			status: http.StatusForbidden, passes: false, wantText: []string{"may not make this write", "gate.denied"},
		},
		{
			name: "refusal with an unknown sentinel", result: gate.ResultAllow,
			err:    refusal(errUnknownSentinel, "from another version"),
			status: http.StatusForbidden, passes: false, wantText: []string{"from another version", "gate.unavailable"},
		},
		{
			name: "unknown result with no error", result: gate.Result("maybe"), err: nil,
			status: http.StatusForbidden, passes: false,
			wantText: []string{"does not know", "gate.unavailable", "decision record gdr-test"},
		},
		{
			name: "deny result with no error", result: gate.ResultDeny, err: nil,
			status: http.StatusForbidden, passes: false, wantText: []string{"does not know", "gate.unavailable"},
		},
		{
			name: "store unavailable", result: gate.ResultAllow,
			err:    fmt.Errorf("record: %w", namespace.ErrUnavailable),
			status: http.StatusServiceUnavailable, passes: false, wantText: nil,
		},
	}
}

// TestStateWriteGuard proves, for each write of the http backend that meets
// state.write and for each answer of the guard, that the guard sees the
// operation and the action of the write, that only allow and warn let the
// write reach the workspace, and that every refusal answers in plain text
// and writes an audit entry.
func TestStateWriteGuard(t *testing.T) {
	t.Parallel()

	for _, route := range stateWriteRoutes() {
		for _, out := range guardOutcomes() {
			t.Run(route.name+"/"+out.name, func(t *testing.T) {
				t.Parallel()

				g := newGuardedHandler(t, newFakeGuard(out.result, out.err))
				route.prepare(g.units.fakeUnits)

				rec := do(g.h, route.method, route.target, route.body, bearer(route.token))
				checkGuardCall(t, g.guard.checks(), route)

				if out.passes {
					if rec.Code != route.okStatus || g.units.reached() != 1 {
						t.Fatalf("status %d, writes %d, want %d and 1 write: %s",
							rec.Code, g.units.reached(), route.okStatus, rec.Body.String())
					}

					return
				}

				checkRefused(t, g, refusedCall{route: route, out: out, status: rec.Code, body: rec.Body.String()})
			})
		}
	}
}

// checkGuardCall checks that the guard saw one check with the operation and
// the action of route, from the CLI.
func checkGuardCall(t *testing.T, seen []gate.StateWrite, route stateWriteRoute) {
	t.Helper()

	if len(seen) != 1 {
		t.Fatalf("guard checks = %d, want 1", len(seen))
	}

	w := seen[0]
	if w.Operation != route.op || w.Action != route.action || w.Source != gate.SourceCLI ||
		w.Unit.String() != "opentofu:acme/net" || w.Principal.Subject != "alice" || w.PlanLock {
		t.Fatalf("check = %+v, want operation %s, action %s, source cli, no plan lock", w, route.op, route.action)
	}
}

// refusedCall is one refused write: the route, the outcome of the guard and
// the answer of the handler.
type refusedCall struct {
	route  stateWriteRoute
	out    guardOutcome
	status int
	body   string
}

// checkRefused checks a refused write: the status, the plain text, no write,
// and one audit entry with the action of the check.
func checkRefused(t *testing.T, g guardedHandler, rc refusedCall) {
	t.Helper()

	if rc.status != rc.out.status || g.units.reached() != 0 {
		t.Fatalf("status %d, writes %d, want %d and no write: %s", rc.status, g.units.reached(), rc.out.status, rc.body)
	}

	checkRefusalText(t, rc)
	checkDenialEntry(t, g, rc.route.action)
}

// checkRefusalText checks the plain text of a refusal: the texts of the
// outcome on one line, and for a state version with no lock, the sentence
// that resources may exist with no stored state.
func checkRefusalText(t *testing.T, rc refusedCall) {
	t.Helper()

	for _, want := range rc.out.wantText {
		if !strings.Contains(rc.body, want) {
			t.Fatalf("body %q lacks %q", rc.body, want)
		}
	}

	if rc.status != http.StatusForbidden {
		return
	}

	if strings.ContainsAny(strings.TrimSuffix(rc.body, "\n"), "\r\n") {
		t.Fatalf("body %q is more than one line", rc.body)
	}

	if rc.route.op == gate.OperationUnlockedWrite &&
		!strings.Contains(rc.body, "resources may exist that no stored state records") {
		t.Fatalf("body %q of a refused state version does not say that resources may have no state", rc.body)
	}
}

// checkDenialEntry checks that the audit log holds a denial of action on the
// unit.
func checkDenialEntry(t *testing.T, g guardedHandler, action identity.Action) {
	t.Helper()

	listing, err := g.audit.List(t.Context(), "acme", audit.Page{Number: 1, Size: audit.MaxPageSize})
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}

	for i := range listing.Entries {
		e := &listing.Entries[i]
		if e.Action == string(action) && e.Object == "opentofu:acme/net" && e.Outcome == audit.OutcomeDenied {
			return
		}
	}

	t.Fatalf("audit entries %+v hold no denial of %s", listing.Entries, action)
}

// readRoutes returns the routes of the http backend that write no state, with
// the reason. A route that is neither here nor in stateWriteRoutes fails
// TestEveryWriteRouteMeetsTheGates.
func readRoutes() map[string]string {
	return map[string]string{
		http.MethodGet + " /tofu/state/{namespace}/{workspace}": "reads the current state and writes nothing",
	}
}

// TestEveryWriteRouteMeetsTheGates proves that each route of the http
// backend either meets state.write, with a case in stateWriteRoutes that
// TestStateWriteGuard runs, or is on the list of routes that write no state.
// A new write route with neither fails the test.
func TestEveryWriteRouteMeetsTheGates(t *testing.T) {
	t.Parallel()

	g := newGuardedHandler(t, newFakeGuard(gate.ResultAllow, nil))

	for _, route := range g.h.Routes() {
		key := route.Method + " " + route.Path
		if _, ok := readRoutes()[key]; ok {
			continue
		}

		met := false

		for _, w := range stateWriteRoutes() {
			if w.method == route.Method && route.Path == "/tofu/state/{namespace}/{workspace}" {
				met = true
			}
		}

		if !met {
			t.Errorf("%s writes and meets no state.write: add its case to stateWriteRoutes, "+
				"or list it in readRoutes with the reason", key)
		}
	}
}

// TestStateWriteGuardSkipsWritesUnderAPassedLock proves the writes that do
// not meet state.write again: a state version that names the lock of the
// caller, which met the gates at that lock, and the unlock of the holder,
// which ends its own lock. A guard that refuses everything stops neither.
func TestStateWriteGuardSkipsWritesUnderAPassedLock(t *testing.T) {
	t.Parallel()

	state := `{"version":4,"serial":1,"lineage":"L","outputs":{},"resources":[]}`

	for _, tc := range []struct {
		name, method, target, body string
	}{
		{name: "state version with the lock", method: http.MethodPost, target: unitPath + "?ID=" + lockOne, body: state},
		{name: "unlock by the holder", method: "UNLOCK", target: unitPath, body: lockBody(lockOne, "alice")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			g := newGuardedHandler(t, newFakeGuard(gate.ResultAllow, &gate.RefusalError{
				Err: gate.ErrDenied, Record: "gdr-test", Messages: []string{"Writes are frozen."},
			}))
			lockedBy(lockOne, "alice")(g.units.fakeUnits)

			rec := do(g.h, tc.method, tc.target, tc.body, bearer(adminToken))
			if rec.Code != http.StatusOK || len(g.guard.checks()) != 0 {
				t.Fatalf("status %d, guard checks %d, want 200 and none: %s",
					rec.Code, len(g.guard.checks()), rec.Body.String())
			}
		})
	}
}

// TestStateWriteGuardJobTokenLock proves that the LOCK of a job token is the
// lock of the plan of its job: the check says so, and the gate service decides
// it by the job model. A LOCK of another credential that carries a job ID is
// not a plan lock.
func TestStateWriteGuardJobTokenLock(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		token string
		want  bool
	}{
		{name: "job token", token: jobToken, want: true},
		{name: "access token with a job ID", token: jobIDOnAccessToken, want: false},
		{name: "access token", token: adminToken, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			g := newGuardedHandler(t, newFakeGuard(gate.ResultAllow, nil))

			rec := do(g.h, "LOCK", unitPath, lockBody(lockOne, "alice"), bearer(tc.token))

			seen := g.guard.checks()
			if rec.Code != http.StatusOK || len(seen) != 1 || seen[0].PlanLock != tc.want {
				t.Fatalf("status %d, checks %+v, want 200 and one check with plan lock %v", rec.Code, seen, tc.want)
			}
		})
	}
}

// TestStateWriteGuardRunsAfterTheCreate proves that a first LOCK creates the
// workspace before the gates run, because the gates read the facts of a unit
// that exists, and that a refusal takes no lock.
func TestStateWriteGuardRunsAfterTheCreate(t *testing.T) {
	t.Parallel()

	g := newGuardedHandler(t, newFakeGuard(gate.ResultAllow, &gate.RefusalError{
		Err: gate.ErrDenied, Record: "gdr-test", Messages: []string{"Writes are frozen."},
	}))
	g.units.exists = false

	rec := do(g.h, "LOCK", unitPath, lockBody(lockOne, "alice"), bearer(adminToken))
	if rec.Code != http.StatusForbidden || g.units.created != 1 || g.units.reached() != 0 {
		t.Fatalf("status %d, created %d, writes %d, want 403, one create and no lock: %s",
			rec.Code, g.units.created, g.units.reached(), rec.Body.String())
	}
}

// TestStateWriteGuardMessageStaysOneLine proves that a gate message with CR
// and LF cannot add a line to the plain-text answer.
func TestStateWriteGuardMessageStaysOneLine(t *testing.T) {
	t.Parallel()

	g := newGuardedHandler(t, newFakeGuard(gate.ResultAllow, &gate.RefusalError{
		Err: gate.ErrDenied, Record: "gdr-a\r\nInjected: yes", Messages: []string{"Frozen.\r\nX-Injected: yes"},
	}))

	rec := do(g.h, "LOCK", unitPath, lockBody(lockOne, "alice"), bearer(adminToken))

	body := strings.TrimSuffix(rec.Body.String(), "\n")
	if rec.Code != http.StatusForbidden || strings.ContainsAny(body, "\r\n") {
		t.Fatalf("status %d, body %q, want 403 on one line", rec.Code, body)
	}
}

// TestStateWriteGuardRunsForALockThatTheCallerDoesNotHold proves that a
// state version whose lock ID is not the current lock of the caller meets the
// gates, even when the workspace port would accept the write. The guard
// refuses everything, so each write must stop at the guard.
func TestStateWriteGuardRunsForALockThatTheCallerDoesNotHold(t *testing.T) {
	t.Parallel()

	state := `{"version":4,"serial":1,"lineage":"L","outputs":{},"resources":[]}`

	for _, tc := range []struct {
		name    string
		lockID  string
		prepare func(u *fakeUnits)
	}{
		{name: "lock ID on an unlocked unit", lockID: "no-such-lock", prepare: func(*fakeUnits) {}},
		{name: "the real lock ID of another holder", lockID: "lock-carol", prepare: lockedBy("lock-carol", "carol")},
		{name: "a wrong ID of the caller's own lock", lockID: "not-" + lockOne, prepare: lockedBy(lockOne, "alice")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			g := newGuardedHandler(t, newFakeGuard(gate.ResultAllow, &gate.RefusalError{
				Err: gate.ErrDenied, Record: "gdr-test", Messages: []string{"Writes are frozen."},
			}))
			g.units.acceptAll = true
			tc.prepare(g.units.fakeUnits)

			rec := do(g.h, http.MethodPost, unitPath+"?ID="+tc.lockID, state, bearer(adminToken))

			seen := g.guard.checks()
			if rec.Code != http.StatusForbidden || len(seen) != 1 || seen[0].Operation != gate.OperationUnlockedWrite ||
				g.units.reached() != 0 {
				t.Fatalf("status %d, checks %+v, writes %d, want 403, one unlocked-write check and no write: %s",
					rec.Code, seen, g.units.reached(), rec.Body.String())
			}
		})
	}
}
