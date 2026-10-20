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

	"opendustpan.org/opendustpan/internal/api/tfe"
	"opendustpan.org/opendustpan/internal/baseurl"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// fakeGuard answers each check with err, and keeps the checks it saw.
type fakeGuard struct {
	mu   sync.Mutex
	err  error
	seen []gate.StateWrite
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

func newGuardedHandler(t *testing.T, ws tfe.WorkspaceService, guard tfe.StateGuard) *tfe.Handler {
	t.Helper()

	h, err := tfe.NewHandler(tfe.Config{
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

// gateErrorDoc decodes a JSON:API error document with the code member.
type gateErrorDoc struct {
	Errors []struct {
		Status string `json:"status"`
		Code   string `json:"code"`
		Title  string `json:"title"`
		Detail string `json:"detail"`
	} `json:"errors"`
}

func TestStateGuardOnTheWriteRoutes(t *testing.T) {
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
			"workspace delete", http.MethodDelete, "/api/v2/organizations/acme/workspaces/web", "", http.StatusNoContent,
			gate.OperationDelete, identity.ActionUnitManage,
		},
	}

	refusals := []struct {
		name string
		err  error
		code string
	}{
		{"deny", &gate.RefusalError{Err: gate.ErrDenied, Record: "gdr-aaaaaaaaaaaaaaaaaaaa", Messages: []string{"No locks."}}, "gate.denied"},
		{"limit", &gate.RefusalError{Err: gate.ErrLimited, Record: "gdr-bbbbbbbbbbbbbbbbbbbb", Messages: []string{"A limit stops this."}}, "gate.limited"},
		{"rule failed", &gate.RefusalError{Err: gate.ErrRuleFailed, Record: "gdr-cccccccccccccccccccc", Messages: []string{"A gate failed."}}, "gate.rule-failed"},
		{"engine down", &gate.RefusalError{Err: gate.ErrUnavailable, Record: "gdr-dddddddddddddddddddd", Messages: []string{"No answer."}}, "gate.unavailable"},
	}

	for _, rt := range routes {
		t.Run(rt.name+" passes when nothing refuses", func(t *testing.T) {
			t.Parallel()

			guard := &fakeGuard{}
			ws := &writeCounter{}
			rec := serveGuarded(t, newGuardedHandler(t, ws, guard), rt.method, rt.target, rt.body)

			if rec.Code != rt.ok || ws.count() != 1 {
				t.Fatalf("%s answers %d with %d writes, want %d and 1: %s", rt.name, rec.Code, ws.count(), rt.ok, rec.Body)
			}

			if len(guard.seen) != 1 {
				t.Fatalf("guard saw %d checks, want 1", len(guard.seen))
			}

			got := guard.seen[0]
			if got.Operation != rt.op || got.Action != rt.action || got.Source != gate.SourceCLI ||
				got.Principal.Subject != "tester" || got.Unit.Tool() != "opentofu" || got.Unit.Namespace() != "acme" {
				t.Fatalf("guard saw %+v, want operation %s, action %s, the CLI source and the unit", got, rt.op, rt.action)
			}
		})

		t.Run(rt.name+" is the same without a guard", func(t *testing.T) {
			t.Parallel()

			ws := &writeCounter{}
			rec := serveGuarded(t, newGuardedHandler(t, ws, nil), rt.method, rt.target, rt.body)

			if rec.Code != rt.ok || ws.count() != 1 {
				t.Fatalf("%s answers %d with %d writes, want %d and 1: %s", rt.name, rec.Code, ws.count(), rt.ok, rec.Body)
			}
		})

		for _, rf := range refusals {
			t.Run(fmt.Sprintf("%s refuses on %s", rt.name, rf.name), func(t *testing.T) {
				t.Parallel()

				guard := &fakeGuard{err: rf.err}
				ws := &writeCounter{}
				rec := serveGuarded(t, newGuardedHandler(t, ws, guard), rt.method, rt.target, rt.body)

				if rec.Code != http.StatusForbidden || ws.count() != 0 {
					t.Fatalf("%s answers %d with %d writes, want 403 and none: %s", rt.name, rec.Code, ws.count(), rec.Body)
				}

				if ct := rec.Header().Get("Content-Type"); ct != "application/vnd.api+json" {
					t.Fatalf("content type = %q, want the JSON:API media type", ct)
				}

				var doc gateErrorDoc
				if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || len(doc.Errors) != 1 {
					t.Fatalf("body %q: %v, want one JSON:API error", rec.Body, err)
				}

				e := doc.Errors[0]
				refusal := &gate.RefusalError{}
				_ = errors.As(rf.err, &refusal)

				if e.Status != "403" || e.Code != rf.code || e.Title != "forbidden" ||
					!strings.Contains(e.Detail, refusal.Messages[0]) ||
					!strings.Contains(e.Detail, string(refusal.Record)) || !strings.Contains(e.Detail, rf.code) {
					t.Fatalf("error = %+v, want status 403, code %s, the message and the record %s", e, rf.code, refusal.Record)
				}
			})
		}

		t.Run(rt.name+" is refused when the guard cannot decide", func(t *testing.T) {
			t.Parallel()

			guard := &fakeGuard{err: errors.New("store down")}
			ws := &writeCounter{}
			rec := serveGuarded(t, newGuardedHandler(t, ws, guard), rt.method, rt.target, rt.body)

			if rec.Code < 400 || ws.count() != 0 {
				t.Fatalf("%s answers %d with %d writes, want an error and no write", rt.name, rec.Code, ws.count())
			}

			if strings.Contains(rec.Body.String(), "store down") {
				t.Fatalf("body %q shows the internal error text", rec.Body)
			}
		})
	}
}

func serveGuarded(t *testing.T, h *tfe.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-token")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
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
