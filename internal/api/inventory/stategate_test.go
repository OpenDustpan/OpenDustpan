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
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// errGuardDown is the error of a guard that cannot decide.
var errGuardDown = errors.New("store down")

// recordingGuard answers each check with err, and keeps the checks it saw.
type recordingGuard struct {
	mu   sync.Mutex
	err  error
	seen []gate.StateWrite
}

var _ apiinventory.StateGuard = (*recordingGuard)(nil)

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
	}
}

func guardedFixture(t *testing.T, guard apiinventory.StateGuard) deletedFixture {
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

func TestNoGuardChangesNothing(t *testing.T) {
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
