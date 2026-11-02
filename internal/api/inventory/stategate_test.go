package inventory_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"syscall"
	"testing"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/internal/api/stategate"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/schema"
)

// errNewSentinel is a sentinel that package gate does not know.
var errNewSentinel = errors.New("a sentinel of a later version")

// errGuardDown is the error of a guard that cannot decide.
var errGuardDown = errors.New("store down")

// recordingGuard answers each check with err, and keeps the checks it saw.
type recordingGuard struct {
	mu   sync.Mutex
	err  error
	seen []gate.StateWrite
}

var _ stategate.Guard = (*recordingGuard)(nil)

func newRecordingGuard(err error) *recordingGuard {
	return &recordingGuard{mu: sync.Mutex{}, err: err, seen: nil}
}

func (g *recordingGuard) GuardStateWrite(_ context.Context, w gate.StateWrite) (gate.Decision, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.seen = append(g.seen, w)

	return gate.Decision{
		Record: "", Result: gate.ResultAllow, Requirements: nil, Warnings: nil, AutoApply: false,
	}, g.err
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
			"restore", http.MethodPost, "/api/inventory/deleted-units/pulumi/stk-1/restore?organization=acme", "{}",
			http.StatusOK, gate.OperationRollback, identity.ActionUnitRestore,
		},
		{
			"purge", http.MethodDelete, "/api/inventory/deleted-units/pulumi/stk-1?organization=acme", "",
			http.StatusNoContent, gate.OperationDelete, identity.ActionUnitPurge,
		},
	}
}

// guardRefusal is one refusal that the guard can give and the answer it shows:
// code is the code in the message, and errorCode the code of the catalog in
// the answer.
type guardRefusal struct {
	name      string
	err       *gate.RefusalError
	status    int
	code      string
	errorCode string
}

func guardRefusals() []guardRefusal {
	refusal := func(err error, record string) *gate.RefusalError {
		return &gate.RefusalError{Err: err, Record: gate.RecordID(record), Messages: []string{"No restores."}}
	}

	return []guardRefusal{
		{
			"deny", refusal(gate.ErrDenied, "gdr-aaaaaaaaaaaaaaaaaaaa"), http.StatusForbidden, "gate.denied",
			errcode.GateDenied,
		},
		{
			"limit", refusal(gate.ErrLimited, "gdr-bbbbbbbbbbbbbbbbbbbb"), http.StatusForbidden, "gate.limited",
			errcode.GateLimited,
		},
		{
			"rule failed", refusal(gate.ErrRuleFailed, "gdr-cccccccccccccccccccc"), http.StatusConflict,
			"gate.rule-failed", errcode.GateRuleFailed,
		},
		{
			"engine down", refusal(gate.ErrUnavailable, "gdr-dddddddddddddddddddd"), http.StatusServiceUnavailable,
			"gate.unavailable", errcode.GateUnavailable,
		},
		{
			"final gate", refusal(gate.ErrFinal, "gdr-eeeeeeeeeeeeeeeeeeee"), http.StatusForbidden, "gate.final",
			errcode.GateDenied,
		},
		{
			"unknown sentinel", refusal(errNewSentinel, "gdr-gggggggggggggggggggg"), http.StatusServiceUnavailable,
			"gate.unavailable", errcode.GateUnavailable,
		},
	}
}

func guardedFixture(t *testing.T, guard stategate.Guard) deletedFixture {
	t.Helper()

	return buildGuardedFixture(t, guardSpec{
		entries: seededDeleted().entries, authz: selectiveAuthorizer{deny: map[identity.Action]bool{}},
		lifecycleErr: nil, scope: fixedScope{err: nil, ids: nil, all: true}, guard: guard,
	})
}

func serveRoute(t *testing.T, f deletedFixture, rt guardRoute) *httptest.ResponseRecorder {
	t.Helper()

	return f.serve(t, rt.method, rt.target, rt.body)
}

func TestStateGuardSeesTheWrite(t *testing.T) {
	t.Parallel()

	for _, rt := range guardRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()

			guard := newRecordingGuard(nil)
			f := guardedFixture(t, guard)

			rec := serveRoute(t, f, rt)
			if rec.Code != rt.ok || len(f.pulumi.calls) != 1 || len(guard.seen) != 1 {
				t.Fatalf("%s answers %d with %d calls and %d checks, want %d, 1 and 1: %s",
					rt.name, rec.Code, len(f.pulumi.calls), len(guard.seen), rt.ok, rec.Body)
			}

			got := guard.seen[0]
			if got.Operation != rt.op || got.Action != rt.action || got.Source != gate.SourceAPI ||
				got.Unit.Tool() != "pulumi" || got.Unit.UnitID() != "stk-1" {
				t.Fatalf(
					"guard saw %+v, want operation %s, action %s, the API source and the unit",
					got,
					rt.op,
					rt.action,
				)
			}
		})
	}
}

// TestNilGuardDoesNotStart checks the start-time step: a handler with no
// state guard is an error, never a handler that checks no write.
func TestNilGuardDoesNotStart(t *testing.T) {
	t.Parallel()

	cfg, _ := guardedConfig(t, guardSpec{
		entries: seededDeleted().entries, authz: selectiveAuthorizer{deny: map[identity.Action]bool{}},
		lifecycleErr: nil, scope: fixedScope{err: nil, ids: nil, all: true}, guard: nil,
	})

	h, err := apiinventory.NewHandler(cfg)
	if h != nil || !errors.Is(err, apiinventory.ErrMissingDependency) || !errors.Is(err, stategate.ErrNoGuard) {
		t.Fatalf("NewHandler(no guard) = %v, %v, want ErrMissingDependency and stategate.ErrNoGuard", h, err)
	}
}

// TestGatesOffChangesNothing checks that the explicit guard of the gates off
// lets each write go on.
func TestGatesOffChangesNothing(t *testing.T) {
	t.Parallel()

	for _, rt := range guardRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()

			f := newDeletedFixture(
				t,
				seededDeleted().entries,
				selectiveAuthorizer{deny: map[identity.Action]bool{}},
				nil,
			)

			rec := serveRoute(t, f, rt)
			if rec.Code != rt.ok || len(f.pulumi.calls) != 1 {
				t.Fatalf(
					"%s answers %d with %d calls, want %d and 1: %s",
					rt.name,
					rec.Code,
					len(f.pulumi.calls),
					rt.ok,
					rec.Body,
				)
			}
		})
	}
}

// refusalBody is the error body of the inventory API.
type refusalBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// checkRefusalBody checks the answer of a refusal.
func checkRefusalBody(t *testing.T, rec *httptest.ResponseRecorder, rf guardRefusal) {
	t.Helper()

	var body refusalBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body %q: %v", rec.Body, err)
	}

	if body.Code != rf.status || !strings.Contains(body.Message, "No restores.") ||
		!strings.Contains(body.Message, string(rf.err.Record)) || !strings.Contains(body.Message, rf.code) {
		t.Fatalf("body = %+v, want code %d, the message, the code %s and the record", body, rf.status, rf.code)
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, rf.status, rec.Body.Bytes(),
		errcodetest.Want{Code: rf.errorCode, Args: nil, RequestBody: nil})
}

func TestStateGuardRefusalIsInTheToolNeutralShape(t *testing.T) {
	t.Parallel()

	for _, rt := range guardRoutes() {
		for _, rf := range guardRefusals() {
			t.Run(rt.name+" on "+rf.name, func(t *testing.T) {
				t.Parallel()

				f := guardedFixture(t, newRecordingGuard(rf.err))

				rec := serveRoute(t, f, rt)
				if rec.Code != rf.status || len(f.pulumi.calls) != 0 {
					t.Fatalf("%s answers %d with %d calls, want %d and none: %s",
						rt.name, rec.Code, len(f.pulumi.calls), rf.status, rec.Body)
				}

				checkRefusalBody(t, rec, rf)
			})
		}
	}
}

func TestStateGuardThatCannotDecideRefuses(t *testing.T) {
	t.Parallel()

	for _, rt := range guardRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()

			f := guardedFixture(t, newRecordingGuard(errGuardDown))

			rec := serveRoute(t, f, rt)
			if rec.Code != http.StatusServiceUnavailable || len(f.pulumi.calls) != 0 ||
				!strings.Contains(rec.Body.String(), "gate.unavailable") {
				t.Fatalf("%s answers %d with %d calls, want 503 with gate.unavailable and none: %s",
					rt.name, rec.Code, len(f.pulumi.calls), rec.Body)
			}

			if strings.Contains(rec.Body.String(), errGuardDown.Error()) {
				t.Fatalf("body %q shows the internal error text", rec.Body)
			}
		})
	}
}

// TestStateGuardOnALostStoreAnswers503WithRetryAfter proves that a guard that
// fails because the database does not answer gives 503 with Retry-After 5 and
// the code namespace.unavailable, not the gate refusal, on each guarded route.
func TestStateGuardOnALostStoreAnswers503WithRetryAfter(t *testing.T) {
	t.Parallel()

	causes := []struct {
		name string
		err  error
	}{
		{"sentinel", namespace.ErrUnavailable},
		{"wrapped sentinel", fmt.Errorf("read fence: %w", namespace.ErrUnavailable)},
		{"connection failed", fmt.Errorf("%w: connection failed: %w", namespace.ErrUnavailable, syscall.ECONNREFUSED)},
		{"busy", fmt.Errorf("%w: database is busy", namespace.ErrUnavailable)},
		{"in a refusal", &gate.RefusalError{
			Err: fmt.Errorf("%w: %w", gate.ErrUnavailable, namespace.ErrUnavailable), Record: "", Messages: nil,
		}},
	}

	for _, rt := range guardRoutes() {
		for _, tc := range causes {
			t.Run(rt.name+" "+tc.name, func(t *testing.T) {
				t.Parallel()

				f := guardedFixture(t, newRecordingGuard(tc.err))

				rec := serveRoute(t, f, rt)
				if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" ||
					len(f.pulumi.calls) != 0 || !strings.Contains(rec.Body.String(), errcode.NamespaceUnavailable) {
					t.Fatalf("%s answers %d with Retry-After %q and %d calls, want 503, 5 and none with %s: %s",
						rt.name, rec.Code, rec.Header().Get("Retry-After"), len(f.pulumi.calls),
						errcode.NamespaceUnavailable, rec.Body)
				}
			})
		}
	}
}

// storeFaultCase is a guard error that the store causes and the text that the
// 503 body holds.
type storeFaultCase struct {
	name string
	err  error
	want []string
}

// storeFaultCases returns the guard errors that the store causes. The guard
// wraps a failed decision record in a refusal, so each cause comes in both
// forms.
func storeFaultCases() []storeFaultCase {
	below := &schema.MinimumError{SchemaMinimum: "v1.1.0", BinaryVersion: "v1.0.0"}
	belowText := []string{"v1.0.0", "v1.1.0", "schema.binary-below-minimum"}
	wrapped := func(cause error) error {
		return &gate.RefusalError{
			Err:      fmt.Errorf("%w: write the decision record: %w", gate.ErrUnavailable, cause),
			Record:   "gdr-1",
			Messages: []string{"The decision record could not be written, so the write is refused."},
		}
	}

	return []storeFaultCase{
		{"minimum error", below, belowText},
		{"minimum error in a refusal", wrapped(below), belowText},
		{"store lost", namespace.ErrUnavailable, []string{errcode.NamespaceUnavailable}},
		{"store lost in a refusal", wrapped(namespace.ErrUnavailable), []string{errcode.NamespaceUnavailable}},
		{"other failure in a refusal", wrapped(errGuardDown), []string{"gate.unavailable"}},
	}
}

// checkStoreFault runs one case on one route and checks the 503, the text and
// that no call reached the Pulumi port.
func checkStoreFault(t *testing.T, tt storeFaultCase, rt guardRoute) {
	t.Helper()

	f := guardedFixture(t, newRecordingGuard(tt.err))

	rec := serveRoute(t, f, rt)
	if rec.Code != http.StatusServiceUnavailable || len(f.pulumi.calls) != 0 {
		t.Fatalf("%s answers %d with %d calls, want 503 and none: %s",
			rt.name, rec.Code, len(f.pulumi.calls), rec.Body)
	}

	for _, want := range tt.want {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body lacks %q: %s", want, rec.Body)
		}
	}

	if strings.Contains(rec.Body.String(), errGuardDown.Error()) {
		t.Errorf("body %q shows the internal error text", rec.Body)
	}
}

func TestStateGuardStoreFaultNamesTheCause(t *testing.T) {
	t.Parallel()

	for _, tt := range storeFaultCases() {
		for _, rt := range guardRoutes() {
			t.Run(tt.name+" on "+rt.name, func(t *testing.T) {
				t.Parallel()
				checkStoreFault(t, tt, rt)
			})
		}
	}
}

// resultGuard answers each check with its result and no error.
type resultGuard struct {
	result gate.Result
}

func (g resultGuard) GuardStateWrite(context.Context, gate.StateWrite) (gate.Decision, error) {
	return gate.Decision{
		Record: "gdr-ffffffffffffffffffff", Result: g.result, Requirements: nil, Warnings: nil, AutoApply: false,
	}, nil
}

// auditedFixture is guardedFixture with the audit store entries.
func auditedFixture(t *testing.T, guard stategate.Guard, entries *testAudit) deletedFixture {
	t.Helper()

	cfg, pulumi := guardedConfig(t, guardSpec{
		entries: seededDeleted().entries, authz: selectiveAuthorizer{deny: map[identity.Action]bool{}},
		lifecycleErr: nil, scope: fixedScope{err: nil, ids: nil, all: true}, guard: guard,
	})
	cfg.Audit = entries

	handler, err := newCheckedHandler(t, cfg)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return deletedFixture{handler: handler, pulumi: pulumi}
}

// checkGuardDenied fails t unless entries holds one denied entry with action.
func checkGuardDenied(t *testing.T, entries *testAudit, action identity.Action) {
	t.Helper()

	entries.mu.Lock()
	got := append([]audit.Entry(nil), entries.entries...)
	entries.mu.Unlock()

	if len(got) != 1 || got[0].Outcome != audit.OutcomeDenied || got[0].Action != string(action) {
		t.Fatalf("audit entries = %+v, want one denied entry with the action %s", got, action)
	}
}

// TestUnknownResultRefusesAndRecords sends a result that is not allow or
// warn, with no error, through each write route. Each call is refused with
// gate.unavailable, reaches no lifecycle and writes one denied audit entry.
func TestUnknownResultRefusesAndRecords(t *testing.T) {
	t.Parallel()

	for _, rt := range guardRoutes() {
		for _, result := range []gate.Result{gate.ResultDeny, gate.ResultError, "", "maybe"} {
			t.Run(rt.name+"/"+string(result), func(t *testing.T) {
				t.Parallel()

				entries := newTestAudit()
				f := auditedFixture(t, resultGuard{result: result}, entries)

				rec := serveRoute(t, f, rt)
				if rec.Code != http.StatusServiceUnavailable ||
					!strings.Contains(rec.Body.String(), errcode.GateUnavailable) ||
					!strings.Contains(rec.Body.String(), "gdr-ffffffffffffffffffff") {
					t.Fatalf(
						"%s answers %d %s, want 503 with gate.unavailable and the record",
						rt.name,
						rec.Code,
						rec.Body,
					)
				}

				if len(f.pulumi.calls) != 0 {
					t.Fatalf("%d calls reached the lifecycle, want 0", len(f.pulumi.calls))
				}

				checkGuardDenied(t, entries, rt.action)
			})
		}
	}
}

// TestEachRefusalIsRecorded checks that each refusal of the guard writes one
// denied audit entry with the action of the route.
func TestEachRefusalIsRecorded(t *testing.T) {
	t.Parallel()

	for _, rt := range guardRoutes() {
		for _, rf := range guardRefusals() {
			t.Run(rt.name+"/"+rf.name, func(t *testing.T) {
				t.Parallel()

				entries := newTestAudit()
				f := auditedFixture(t, newRecordingGuard(rf.err), entries)

				if rec := serveRoute(t, f, rt); rec.Code != rf.status {
					t.Fatalf("%s answers %d, want %d: %s", rt.name, rec.Code, rf.status, rec.Body)
				}

				checkGuardDenied(t, entries, rt.action)
			})
		}
	}
}

// TestRefusalSaysOneCode checks that the answer, the detail and the audit
// entry of each refusal come from the one code of the admission. A refusal
// with an unknown sentinel says gate.unavailable in all three.
func TestRefusalSaysOneCode(t *testing.T) {
	t.Parallel()

	for _, rt := range guardRoutes() {
		for _, rf := range guardRefusals() {
			t.Run(rt.name+"/"+rf.name, func(t *testing.T) {
				t.Parallel()

				entries := newTestAudit()
				f := auditedFixture(t, newRecordingGuard(rf.err), entries)

				rec := serveRoute(t, f, rt)
				checkRefusalBody(t, rec, rf)

				entries.mu.Lock()
				got := append([]audit.Entry(nil), entries.entries...)
				entries.mu.Unlock()

				if len(got) != 1 || !strings.Contains(got[0].Reason, "state.write refused with "+rf.code) {
					t.Fatalf("audit entries = %+v, want one with the code %s", got, rf.code)
				}
			})
		}
	}
}

// TestEveryWriteRouteMeetsTheGates fails for a route that writes, that is
// not a route of guardRoutes, and that is not on the list of routes that
// write no state. Every route other than GET writes in this API.
func TestEveryWriteRouteMeetsTheGates(t *testing.T) {
	t.Parallel()

	// noStateWrite lists the routes other than GET that write no state, and
	// why. It is empty: each such route of this API writes state.
	noStateWrite := map[string]string{}

	gated := map[string]bool{}
	for _, rt := range guardRoutes() {
		gated[rt.method+" "+strings.SplitN(rt.target, "?", 2)[0]] = true
	}

	f := guardedFixture(t, newRecordingGuard(nil))

	for _, route := range f.handler.Routes() {
		if route.Method == http.MethodGet || route.Method == http.MethodHead {
			continue
		}

		key := route.Method + " " + route.Path
		if _, ok := noStateWrite[key]; ok {
			continue
		}

		if !routeIsGated(route.Method, route.Path, gated) {
			t.Errorf("%s writes and meets no state.write: add its case to guardRoutes, "+
				"or list it in noStateWrite with the reason", key)
		}
	}
}

// routeIsGated reports whether a target of gated matches the pattern of the
// route. A wildcard of the pattern matches one segment.
func routeIsGated(method, pattern string, gated map[string]bool) bool {
	want := strings.Split(pattern, "/")

	for key := range gated {
		m, target, _ := strings.Cut(key, " ")
		got := strings.Split(target, "/")

		if m != method || len(got) != len(want) {
			continue
		}

		match := true

		for i := range want {
			if !strings.HasPrefix(want[i], "{") && want[i] != got[i] {
				match = false
			}
		}

		if match {
			return true
		}
	}

	return false
}
