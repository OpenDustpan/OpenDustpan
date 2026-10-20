package inventory_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
)

// recordingGuard answers each check with err, and keeps the checks it saw.
type recordingGuard struct {
	mu   sync.Mutex
	err  error
	seen []gate.StateWrite
}

func (g *recordingGuard) GuardStateWrite(_ context.Context, w gate.StateWrite) (gate.Decision, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	g.seen = append(g.seen, w)

	return gate.Decision{
		Record: "", Result: gate.ResultAllow, Requirements: nil, Warnings: nil, AutoApply: false,
	}, g.err
}

func TestStateGuardOnRestoreAndPurge(t *testing.T) {
	t.Parallel()

	routes := []struct {
		name   string
		method string
		target string
		body   string
		ok     int
		op     gate.OperationKind
		action identity.Action
	}{
		{
			"restore", http.MethodPost, "/api/inventory/deleted-units/pulumi/stk-1/restore?organization=acme", "{}", http.StatusOK,
			gate.OperationRollback, identity.ActionUnitRestore,
		},
		{
			"purge", http.MethodDelete, "/api/inventory/deleted-units/pulumi/stk-1?organization=acme", "", http.StatusNoContent,
			gate.OperationDelete, identity.ActionUnitPurge,
		},
	}

	refusals := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"deny", &gate.RefusalError{Err: gate.ErrDenied, Record: "gdr-aaaaaaaaaaaaaaaaaaaa", Messages: []string{"No restores."}}, http.StatusForbidden, "gate.denied"},
		{"limit", &gate.RefusalError{Err: gate.ErrLimited, Record: "gdr-bbbbbbbbbbbbbbbbbbbb", Messages: []string{"No restores."}}, http.StatusForbidden, "gate.limited"},
		{"rule failed", &gate.RefusalError{Err: gate.ErrRuleFailed, Record: "gdr-cccccccccccccccccccc", Messages: []string{"No restores."}}, http.StatusConflict, "gate.rule-failed"},
		{"engine down", &gate.RefusalError{Err: gate.ErrUnavailable, Record: "gdr-dddddddddddddddddddd", Messages: []string{"No restores."}}, http.StatusServiceUnavailable, "gate.unavailable"},
	}

	for _, rt := range routes {
		t.Run(rt.name+" passes when nothing refuses", func(t *testing.T) {
			t.Parallel()

			guard := &recordingGuard{}
			f := buildGuardedFixture(t, seededDeleted().entries, selectiveAuthorizer{deny: map[identity.Action]bool{}}, nil,
				fixedScope{err: nil, ids: nil, all: true}, guard)

			rec := f.serve(t, rt.method, rt.target, rt.body)
			if rec.Code != rt.ok || len(f.pulumi.calls) != 1 {
				t.Fatalf("%s answers %d with %d calls, want %d and 1: %s", rt.name, rec.Code, len(f.pulumi.calls), rt.ok, rec.Body)
			}

			if len(guard.seen) != 1 {
				t.Fatalf("guard saw %d checks, want 1", len(guard.seen))
			}

			got := guard.seen[0]
			if got.Operation != rt.op || got.Action != rt.action || got.Source != gate.SourceAPI ||
				got.Unit.Tool() != "pulumi" || got.Unit.UnitID() != "stk-1" {
				t.Fatalf("guard saw %+v, want operation %s, action %s, the API source and the unit", got, rt.op, rt.action)
			}
		})

		t.Run(rt.name+" is the same without a guard", func(t *testing.T) {
			t.Parallel()

			f := newDeletedFixture(t, seededDeleted().entries, selectiveAuthorizer{deny: map[identity.Action]bool{}}, nil)

			rec := f.serve(t, rt.method, rt.target, rt.body)
			if rec.Code != rt.ok || len(f.pulumi.calls) != 1 {
				t.Fatalf("%s answers %d with %d calls, want %d and 1: %s", rt.name, rec.Code, len(f.pulumi.calls), rt.ok, rec.Body)
			}
		})

		for _, rf := range refusals {
			t.Run(rt.name+" refuses on "+rf.name, func(t *testing.T) {
				t.Parallel()

				guard := &recordingGuard{err: rf.err}
				f := buildGuardedFixture(t, seededDeleted().entries, selectiveAuthorizer{deny: map[identity.Action]bool{}}, nil,
					fixedScope{err: nil, ids: nil, all: true}, guard)

				rec := f.serve(t, rt.method, rt.target, rt.body)
				if rec.Code != rf.status || len(f.pulumi.calls) != 0 {
					t.Fatalf("%s answers %d with %d calls, want %d and none: %s", rt.name, rec.Code, len(f.pulumi.calls), rf.status, rec.Body)
				}

				var body struct {
					Code    int    `json:"code"`
					Message string `json:"message"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("body %q: %v", rec.Body, err)
				}

				refusal := &gate.RefusalError{}
				_ = errors.As(rf.err, &refusal)

				if body.Code != rf.status || !strings.Contains(body.Message, refusal.Messages[0]) ||
					!strings.Contains(body.Message, string(refusal.Record)) || !strings.Contains(body.Message, rf.code) {
					t.Fatalf("body = %+v, want code %d, the message, the code %s and the record", body, rf.status, rf.code)
				}
			})
		}

		t.Run(rt.name+" is refused when the guard cannot decide", func(t *testing.T) {
			t.Parallel()

			guard := &recordingGuard{err: errors.New("store down")}
			f := buildGuardedFixture(t, seededDeleted().entries, selectiveAuthorizer{deny: map[identity.Action]bool{}}, nil,
				fixedScope{err: nil, ids: nil, all: true}, guard)

			rec := f.serve(t, rt.method, rt.target, rt.body)
			if rec.Code < 400 || len(f.pulumi.calls) != 0 {
				t.Fatalf("%s answers %d with %d calls, want an error and none", rt.name, rec.Code, len(f.pulumi.calls))
			}

			if strings.Contains(rec.Body.String(), "store down") {
				t.Fatalf("body %q shows the internal error text", rec.Body)
			}
		})
	}
}

var _ apiinventory.StateGuard = (*recordingGuard)(nil)
