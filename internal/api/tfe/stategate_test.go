package tfe_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// errGuardDown is the error of a guard that cannot decide.
var errGuardDown = errors.New("store down")

// fakeGuard answers each check with err, and keeps the checks it saw.
type fakeGuard struct {
	mu   sync.Mutex
	err  error
	seen []gate.StateWrite
}

func newFakeGuard(err error) *fakeGuard {
	return &fakeGuard{mu: sync.Mutex{}, err: err, seen: nil}
}

func (g *fakeGuard) GuardStateWrite(_ context.Context, w gate.StateWrite) (gate.Decision, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.seen = append(g.seen, w)

	return gate.Decision{
		Record: "", Result: gate.ResultAllow, Requirements: nil, Warnings: nil, AutoApply: false,
	}, g.err
}

// writeCounter counts the writes that reach the workspace port.
type writeCounter struct {
	stubWorkspaces

	mu     sync.Mutex
	writes int
}

func newWriteCounter() *writeCounter {
	return &writeCounter{stubWorkspaces: stubWorkspaces{}, mu: sync.Mutex{}, writes: 0}
}

func (c *writeCounter) LockForJob(
	ctx context.Context, id workspace.ID, holder, reason, job string,
) (workspace.Workspace, error) {
	c.bump()

	return c.stubWorkspaces.LockForJob(ctx, id, holder, reason, job)
}

//nolint:revive // The signature of the workspace port that the handler reads.
func (c *writeCounter) ForceUnlockLock(
	ctx context.Context, id workspace.ID, expect workspace.Lock, by string, allow namespace.Overrides,
	record func() error, e audit.Entry,
) (workspace.Workspace, bool, error) {
	c.bump()

	return c.stubWorkspaces.ForceUnlockLock(ctx, id, expect, by, allow, record, e)
}

func (c *writeCounter) Restore(
	ctx context.Context, id workspace.ID, from workspace.StateVersionID, by string,
) (workspace.StateVersion, error) {
	c.bump()

	return c.stubWorkspaces.Restore(ctx, id, from, by)
}

func (c *writeCounter) Delete(ctx context.Context, ref workspace.Ref, e audit.Entry) error {
	c.bump()

	return c.stubWorkspaces.Delete(ctx, ref, e)
}

func (c *writeCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.writes
}

func (c *writeCounter) bump() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.writes++
}

func newGuardedHandler(t *testing.T, ws tfe.WorkspaceService, guard tfe.StateGuard) *tfe.Handler {
	t.Helper()

	h, err := tfe.NewHandler(tfe.Config{
		Registry:      nil,
		Authenticator: tokenAuthenticator{}, Authorizer: allowAll(), AddressGate: nil,
		Logger: slog.New(slog.DiscardHandler), Namespaces: newTestNamespaces(t), Workspaces: ws,
		BaseURL: baseurl.Resolver{}, Audit: nil, OnAuditFailure: nil, DenialLimiter: nil, Gate: nil,
		MaxStateBytes: 0, StateGuard: guard,
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return h
}

// gateErrorItem is one error of a JSON:API document with the code member.
type gateErrorItem struct {
	Status string `json:"status"`
	Code   string `json:"code"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// gateErrorDoc decodes a JSON:API error document with the code member.
type gateErrorDoc struct {
	Errors []gateErrorItem `json:"errors"`
}

// guardRoute is one write route and what the guard must see for it.
type guardRoute struct {
	name   string
	method string
	target string
	body   string
	ok     int
	op     gate.OperationKind
	action identity.Action
}

func guardRoutes() []guardRoute {
	return []guardRoute{
		{
			"lock", http.MethodPost, "/api/v2/workspaces/ws-test123/actions/lock", `{"data":{}}`, http.StatusOK,
			gate.OperationLock, identity.ActionUnitWrite,
		},
		{
			"force-unlock", http.MethodPost, "/api/v2/workspaces/ws-test123/actions/force-unlock", "", http.StatusOK,
			gate.OperationUnlock, identity.ActionUnitWriteForced,
		},
		{
			"restore", http.MethodPost, "/api/v2/state-versions/sv-test123/actions/restore", "", http.StatusOK,
			gate.OperationRollback, identity.ActionUnitWrite,
		},
		{
			"workspace delete",
			http.MethodDelete,
			"/api/v2/organizations/acme/workspaces/web",
			"",
			http.StatusNoContent,
			gate.OperationDelete,
			identity.ActionUnitManage,
		},
	}
}

// guardRefusal is one refusal that the guard can give and the code it shows.
type guardRefusal struct {
	name    string
	err     *gate.RefusalError
	code    string
	message string
}

func guardRefusals() []guardRefusal {
	refusal := func(err error, record, message string) *gate.RefusalError {
		return &gate.RefusalError{Err: err, Record: gate.RecordID(record), Messages: []string{message}}
	}

	return []guardRefusal{
		{"deny", refusal(gate.ErrDenied, "gdr-aaaaaaaaaaaaaaaaaaaa", "No locks."), "gate.denied", "No locks."},
		{"limit", refusal(gate.ErrLimited, "gdr-bbbbbbbbbbbbbbbbbbbb", "A limit."), "gate.limited", "A limit."},
		{
			"rule failed",
			refusal(gate.ErrRuleFailed, "gdr-cccccccccccccccccccc", "A gate failed."),
			"gate.rule-failed",
			"A gate failed.",
		},
		{
			"engine down",
			refusal(gate.ErrUnavailable, "gdr-dddddddddddddddddddd", "No answer."),
			"gate.unavailable",
			"No answer.",
		},
	}
}

func serveGuarded(t *testing.T, h *tfe.Handler, rt guardRoute) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), rt.method, rt.target, strings.NewReader(rt.body))
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestStateGuardSeesTheWrite(t *testing.T) {
	t.Parallel()

	for _, rt := range guardRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()

			guard, ws := newFakeGuard(nil), newWriteCounter()

			rec := serveGuarded(t, newGuardedHandler(t, ws, guard), rt)
			if rec.Code != rt.ok || ws.count() != 1 || len(guard.seen) != 1 {
				t.Fatalf("%s answers %d with %d writes and %d checks, want %d, 1 and 1: %s",
					rt.name, rec.Code, ws.count(), len(guard.seen), rt.ok, rec.Body)
			}

			got := guard.seen[0]
			if got.Operation != rt.op || got.Action != rt.action || got.Source != gate.SourceCLI ||
				got.Principal.Subject != "tester" || got.Unit.Tool() != "opentofu" || got.Unit.Namespace() != "acme" {
				t.Fatalf(
					"guard saw %+v, want operation %s, action %s, the CLI source and the unit",
					got,
					rt.op,
					rt.action,
				)
			}
		})
	}
}

func TestNoGuardChangesNothing(t *testing.T) {
	t.Parallel()

	for _, rt := range guardRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()

			ws := newWriteCounter()

			rec := serveGuarded(t, newGuardedHandler(t, ws, nil), rt)
			if rec.Code != rt.ok || ws.count() != 1 {
				t.Fatalf(
					"%s answers %d with %d writes, want %d and 1: %s",
					rt.name,
					rec.Code,
					ws.count(),
					rt.ok,
					rec.Body,
				)
			}
		})
	}
}

// checkRefusalDocument checks the 403 answer of a refusal.
func checkRefusalDocument(t *testing.T, rec *httptest.ResponseRecorder, rf guardRefusal) {
	t.Helper()

	if ct := rec.Header().Get("Content-Type"); ct != "application/vnd.api+json" {
		t.Fatalf("content type = %q, want the JSON:API media type", ct)
	}

	var doc gateErrorDoc
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || len(doc.Errors) != 1 {
		t.Fatalf("body %q: %v, want one JSON:API error", rec.Body, err)
	}

	e := doc.Errors[0]
	if e.Status != "403" || e.Code != rf.code || e.Title != "forbidden" ||
		!strings.Contains(e.Detail, rf.message) || !strings.Contains(e.Detail, string(rf.err.Record)) ||
		!strings.Contains(e.Detail, rf.code) {
		t.Fatalf("error = %+v, want status 403, code %s, the message and the record %s", e, rf.code, rf.err.Record)
	}
}

func TestStateGuardRefusalIsA403InTheTFEShape(t *testing.T) {
	t.Parallel()

	for _, rt := range guardRoutes() {
		for _, rf := range guardRefusals() {
			t.Run(rt.name+" on "+rf.name, func(t *testing.T) {
				t.Parallel()

				ws := newWriteCounter()

				rec := serveGuarded(t, newGuardedHandler(t, ws, newFakeGuard(rf.err)), rt)
				if rec.Code != http.StatusForbidden || ws.count() != 0 {
					t.Fatalf(
						"%s answers %d with %d writes, want 403 and none: %s",
						rt.name,
						rec.Code,
						ws.count(),
						rec.Body,
					)
				}

				checkRefusalDocument(t, rec, rf)
			})
		}
	}
}

func TestStateGuardThatCannotDecideRefuses(t *testing.T) {
	t.Parallel()

	for _, rt := range guardRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()

			ws := newWriteCounter()

			rec := serveGuarded(t, newGuardedHandler(t, ws, newFakeGuard(errGuardDown)), rt)
			// OpenTofu reads 403 as a refusal and stops. It retries a 5xx.
			if rec.Code != http.StatusForbidden || ws.count() != 0 ||
				!strings.Contains(rec.Body.String(), "gate.unavailable") {
				t.Fatalf(
					"%s answers %d with %d writes, want 403 with gate.unavailable and no write: %s",
					rt.name, rec.Code, ws.count(), rec.Body,
				)
			}

			if strings.Contains(rec.Body.String(), errGuardDown.Error()) {
				t.Fatalf("body %q shows the internal error text", rec.Body)
			}
		})
	}
}

func TestStateGuardRefusalOfAJobTokenIsADenial(t *testing.T) {
	t.Parallel()

	for _, rt := range guardRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()

			ws := newWriteCounter()
			err := fmt.Errorf("job 42 does not hold the unit: %w", identity.ErrDenied)

			rec := serveGuarded(t, newGuardedHandler(t, ws, newFakeGuard(err)), rt)
			if rec.Code != http.StatusForbidden || ws.count() != 0 ||
				strings.Contains(rec.Body.String(), "gate.unavailable") {
				t.Fatalf("%s answers %d with %d writes, want 403 as a denial and no write: %s",
					rt.name, rec.Code, ws.count(), rec.Body)
			}
		})
	}
}

// heldWorkspaces is a workspace port whose workspace is locked by someone
// else, and whose GetByID fails when readFails is set.
type heldWorkspaces struct {
	*writeCounter

	readFails bool
}

func (h heldWorkspaces) GetByID(ctx context.Context, id workspace.ID) (workspace.Workspace, error) {
	if h.readFails {
		return workspace.Workspace{}, workspace.ErrNotFound
	}

	w, err := h.writeCounter.GetByID(ctx, id)
	w.Lock = workspace.Lock{LockedAt: time.Now(), ID: "lock-1", Holder: "bob", Reason: ""}

	return w, err
}

func (h heldWorkspaces) LockForJob(
	_ context.Context, _ workspace.ID, _, _, _ string,
) (workspace.Workspace, error) {
	return workspace.Workspace{}, workspace.ErrLocked
}

// A CLI that waits under -lock-timeout retries a lock on a held workspace. The
// retry writes no decision record, so the guard is not asked while the lock is held.
func TestLockOnAHeldWorkspaceAsksNoGuard(t *testing.T) {
	t.Parallel()

	guard := newFakeGuard(nil)
	h := newGuardedHandler(t, heldWorkspaces{writeCounter: newWriteCounter(), readFails: false}, guard)
	rt := guardRoutes()[0]

	for range 3 {
		if rec := serveGuarded(t, h, rt); rec.Code != http.StatusConflict {
			t.Fatalf("lock on a held workspace answers %d, want 409: %s", rec.Code, rec.Body)
		}
	}

	if len(guard.seen) != 0 {
		t.Fatalf("the guard saw %d checks of a lock on a held workspace, want none", len(guard.seen))
	}
}

// The guard runs after the allowance check of a force-unlock, so a call that
// the allowance refuses writes no decision record.
func TestForceUnlockAsksTheGuardAfterTheAllowance(t *testing.T) {
	t.Parallel()

	guard := newFakeGuard(nil)
	h := newGuardedHandler(t, heldWorkspaces{writeCounter: newWriteCounter(), readFails: true}, guard)
	rt := guardRoutes()[1]

	if rec := serveGuarded(t, h, rt); rec.Code != http.StatusNotFound {
		t.Fatalf("force-unlock with a failed allowance answers %d, want 404: %s", rec.Code, rec.Body)
	}

	if len(guard.seen) != 0 {
		t.Fatalf("the guard saw %d checks before the allowance, want none", len(guard.seen))
	}
}

func TestGateErrorCodesAreListed(t *testing.T) {
	t.Parallel()

	listed := tfe.ErrorCodes()

	for _, want := range []string{"gate.denied", "gate.limited", "gate.rule-failed", "gate.unavailable"} {
		found := false

		for _, c := range listed {
			found = found || c == want
		}

		if !found {
			t.Fatalf("ErrorCodes() = %v, want %s in it", listed, want)
		}
	}
}

// flipWorkspaces is a workspace port whose lock reads as held by bob while
// held(lockCalls) is true. LockForJob answers the errors in lockErrs one by
// one, then succeeds, and counts its calls. A held lock that LockForJob would
// still take models a holder that unlocks after the check of the handler.
type flipWorkspaces struct {
	*writeCounter

	mu        sync.Mutex
	held      func(lockCalls int) bool
	lockErrs  []error
	lockCalls int
}

func newFlipWorkspaces(held func(lockCalls int) bool, lockErrs ...error) *flipWorkspaces {
	return &flipWorkspaces{
		writeCounter: newWriteCounter(), mu: sync.Mutex{}, held: held, lockErrs: lockErrs, lockCalls: 0,
	}
}

func (f *flipWorkspaces) GetByID(ctx context.Context, id workspace.ID) (workspace.Workspace, error) {
	w, err := f.writeCounter.GetByID(ctx, id)

	f.mu.Lock()
	defer f.mu.Unlock()

	w.Lock = workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""}
	if f.held(f.lockCalls) {
		w.Lock = workspace.Lock{LockedAt: time.Now(), ID: "lock-1", Holder: "bob", Reason: ""}
	}

	return w, err
}

func (f *flipWorkspaces) LockForJob(
	ctx context.Context, id workspace.ID, holder, reason, job string,
) (workspace.Workspace, error) {
	f.mu.Lock()

	f.lockCalls++
	if len(f.lockErrs) > 0 {
		err := f.lockErrs[0]
		f.lockErrs = f.lockErrs[1:]
		f.mu.Unlock()

		return workspace.Workspace{}, err
	}

	f.mu.Unlock()

	return f.writeCounter.LockForJob(ctx, id, holder, reason, job)
}

func (f *flipWorkspaces) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.lockCalls
}

func always(int) bool { return true }

func never(int) bool { return false }

// The lock is held at the check and would be free at LockForJob. The handler
// must not call LockForJob for a lock that it saw held: the call that would win
// has not passed the gates and the limits.
func TestLockHeldAtTheCheckIsNeverTakenUnguarded(t *testing.T) {
	t.Parallel()

	guard := newFakeGuard(nil)
	ws := newFlipWorkspaces(always)

	rec := serveGuarded(t, newGuardedHandler(t, ws, guard), guardRoutes()[0])
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "is locked by User bob") {
		t.Fatalf("lock held at the check answers %d, want 409 with the holder: %s", rec.Code, rec.Body)
	}

	if ws.calls() != 0 || ws.count() != 0 || len(guard.seen) != 0 {
		t.Fatalf("lock held at the check made %d lock calls and %d checks, want none", ws.calls(), len(guard.seen))
	}
}

// A caller that a limit blocks still sees the holder of a held lock, and the
// call writes no record, because the guard is not asked.
func TestLimitBlockedCallerSeesTheHolderOfAHeldLock(t *testing.T) {
	t.Parallel()

	limited := &gate.RefusalError{
		Err: gate.ErrLimited, Record: "gdr-bbbbbbbbbbbbbbbbbbbb", Messages: []string{"A limit."},
	}
	guard := newFakeGuard(limited)
	ws := newFlipWorkspaces(always)
	h := newGuardedHandler(t, ws, guard)

	for range 3 {
		rec := serveGuarded(t, h, guardRoutes()[0])
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "is locked by User bob") {
			t.Fatalf("limited caller on a held lock gets %d, want 409 with the holder: %s", rec.Code, rec.Body)
		}
	}

	if ws.calls() != 0 || len(guard.seen) != 0 {
		t.Fatalf("held lock made %d lock calls and %d checks, want none", ws.calls(), len(guard.seen))
	}
}

// A lost compare-and-swap retries through the guard again, so the second
// LockForJob call is also guarded.
func TestLockRetryRunsTheGuardAgain(t *testing.T) {
	t.Parallel()

	guard := newFakeGuard(nil)
	ws := newFlipWorkspaces(never, workspace.ErrConcurrentModification)

	rec := serveGuarded(t, newGuardedHandler(t, ws, guard), guardRoutes()[0])
	if rec.Code != http.StatusOK || ws.calls() != 2 || len(guard.seen) != 2 {
		t.Fatalf("retried lock answers %d with %d lock calls and %d checks, want 200, 2 and 2: %s",
			rec.Code, ws.calls(), len(guard.seen), rec.Body)
	}
}

// The holder appears between the lost compare-and-swap and the retry. The
// retry answers 409 and does not call LockForJob again.
func TestLockRetryOnAHeldLockCallsNothing(t *testing.T) {
	t.Parallel()

	guard := newFakeGuard(nil)
	ws := newFlipWorkspaces(func(calls int) bool { return calls >= 1 }, workspace.ErrConcurrentModification)

	rec := serveGuarded(t, newGuardedHandler(t, ws, guard), guardRoutes()[0])
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "is locked by User bob") ||
		ws.calls() != 1 || len(guard.seen) != 1 {
		t.Fatalf("retry on a held lock answers %d with %d lock calls and %d checks, want 409, 1 and 1: %s",
			rec.Code, ws.calls(), len(guard.seen), rec.Body)
	}
}

// passThenRefuse allows the first check and refuses the later ones with after.
type passThenRefuse struct {
	mu    sync.Mutex
	seen  int
	after error
}

func (g *passThenRefuse) GuardStateWrite(_ context.Context, _ gate.StateWrite) (gate.Decision, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.seen++

	if g.seen > 1 {
		return gate.Decision{Record: "", Result: gate.ResultDeny, Requirements: nil, Warnings: nil, AutoApply: false},
			g.after
	}

	return gate.Decision{Record: "", Result: gate.ResultAllow, Requirements: nil, Warnings: nil, AutoApply: false}, nil
}

// A gate that refuses at the retry stops the second take of the lock.
func TestLockRetryRefusedByTheGuardTakesNoLock(t *testing.T) {
	t.Parallel()

	denied := &gate.RefusalError{
		Err:      gate.ErrDenied,
		Record:   "gdr-aaaaaaaaaaaaaaaaaaaa",
		Messages: []string{"No locks."},
	}
	guard := &passThenRefuse{mu: sync.Mutex{}, seen: 0, after: denied}
	ws := newFlipWorkspaces(never, workspace.ErrConcurrentModification)

	rec := serveGuarded(t, newGuardedHandler(t, ws, guard), guardRoutes()[0])
	if rec.Code != http.StatusForbidden || ws.calls() != 1 || ws.count() != 0 {
		t.Fatalf("retry refused by the guard answers %d with %d lock calls, want 403 and 1: %s",
			rec.Code, ws.calls(), rec.Body)
	}
}
