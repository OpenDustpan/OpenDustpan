package runnerapi_test

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/runner"
)

const (
	routeBase     = "/api/dustpan/v1/namespaces/acme"
	routePools    = routeBase + "/runner-pools"
	routeOldBase  = "/api/runners/acme"
	routeNoSecret = "no-such-id"
)

// toggleAuthz allows the read of the namespace and the read of the runners
// always, and the management of the runners only while manage is set.
type toggleAuthz struct {
	manage *atomic.Bool
}

func (a toggleAuthz) Authorize(
	_ context.Context, _ identity.Principal, action identity.Action, _ identity.Object,
) error {
	if action == identity.ActionNamespaceManageRunners && a.manage.Load() {
		return nil
	}

	if action == identity.ActionNamespaceRead || action == identity.ActionNamespaceReadRunners {
		return nil
	}

	return identity.ErrDenied
}

func (toggleAuthz) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{}, errNoListing
}

// newToggleEnv serves the handler with a toggleAuthz that starts with the
// management grant on.
func newToggleEnv(t *testing.T) (*env, *atomic.Bool) {
	t.Helper()

	manage := &atomic.Bool{}
	manage.Store(true)

	e := newEnvServing(t, openSQLite, func(_ stores, opts *serveOptions) {
		opts.authz = toggleAuthz{manage: manage}
	})

	return e, manage
}

// TestPoolRoutes_NewPaths drives each management route on its path under
// /api/dustpan/v1/namespaces/{namespace}.
func TestPoolRoutes_NewPaths(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	pool := e.createPool()

	r := e.newRunner("builder-1")
	if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s, want 200", status, body)
	}

	tokenID := e.freshTokenID(pool)

	steps := []struct {
		method, path string
		body         any
		want         int
		contains     string
	}{
		{http.MethodGet, routePools, nil, http.StatusOK, `"pools"`},
		{http.MethodGet, routePools + "/" + pool, nil, http.StatusOK, pool},
		{http.MethodPatch, routePools + "/" + pool, map[string]any{}, http.StatusOK, pool},
		{http.MethodGet, routePools + "/" + pool + "/join-tokens", nil, http.StatusOK, tokenID},
		{http.MethodGet, routePools + "/" + pool + "/runners", nil, http.StatusOK, r.id},
		{
			http.MethodPost,
			routePools + "/" + pool + "/join-tokens/" + tokenID + "/revoke",
			nil,
			http.StatusNoContent,
			"",
		},
		{http.MethodPost, routeBase + "/runners/" + r.id + "/drain", nil, http.StatusNoContent, ""},
		{http.MethodPost, routeBase + "/runners/" + r.id + "/retire", nil, http.StatusNoContent, ""},
		{http.MethodDelete, routePools + "/" + pool, nil, http.StatusNoContent, ""},
	}

	for _, s := range steps {
		status, body := e.admin(s.method, s.path, s.body)
		if status != s.want || !strings.Contains(string(body), s.contains) {
			t.Errorf("%s %s = %d %s, want %d with %q", s.method, s.path, status, body, s.want, s.contains)
		}
	}
}

// TestPoolRoutes_OldPathsAreUnknown proves that the old paths answer 404, with
// no redirect and no data, for every method.
func TestPoolRoutes_OldPathsAreUnknown(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	pool := e.createPool()

	paths := []struct{ method, path string }{
		{http.MethodGet, routeOldBase + "/pools"},
		{http.MethodPost, routeOldBase + "/pools"},
		{http.MethodGet, routeOldBase + "/pools/" + pool},
		{http.MethodPatch, routeOldBase + "/pools/" + pool},
		{http.MethodDelete, routeOldBase + "/pools/" + pool},
		{http.MethodGet, routeOldBase + "/pools/" + pool + "/join-tokens"},
		{http.MethodPost, routeOldBase + "/pools/" + pool + "/join-tokens"},
		{http.MethodPost, routeOldBase + "/pools/" + pool + "/join-tokens/x/revoke"},
		{http.MethodGet, routeOldBase + "/pools/" + pool + "/runners"},
		{http.MethodPost, routeOldBase + "/runners/x/drain"},
		{http.MethodPost, routeOldBase + "/runners/x/retire"},
	}

	for _, p := range paths {
		status, body := e.admin(p.method, p.path, nil)
		if status != http.StatusNotFound {
			t.Errorf("%s %s = %d %s, want 404", p.method, p.path, status, body)
		}

		if strings.Contains(string(body), pool) {
			t.Errorf("%s %s answered with data: %s", p.method, p.path, body)
		}
	}
}

// TestPoolRoutes_AuditEntriesStoredInTransaction proves that each mutation
// leaves its audit entry together with its change, and that a refused
// mutation leaves neither.
func TestPoolRoutes_AuditEntriesStoredInTransaction(t *testing.T) {
	t.Parallel()

	e := newEnv(t)

	if got := e.auditActions(); len(got) != 0 {
		t.Fatalf("audit log before any change = %v, want empty", got)
	}

	pool := e.createPool()
	e.requireEvents(runner.EventPoolCreated)

	// A refused create stores no second entry and no second pool.
	before := len(e.pools())

	status, body := e.admin(http.MethodPost, routePools, map[string]any{"name": "prod", "purpose": "jobs"})
	if status != http.StatusConflict {
		t.Fatalf("create again = %d %s, want 409", status, body)
	}

	if after := len(e.pools()); after != before {
		t.Errorf("pools after refused create = %d, want %d", after, before)
	}

	r := e.newRunner("builder-1")
	if status, body = r.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s, want 200", status, body)
	}

	e.requireEvents(runner.EventJoinTokenCreated, runner.EventRunnerJoined)

	tokenID := e.freshTokenID(pool)

	steps := []struct {
		method, path string
		body         any
		event        runner.Event
	}{
		{http.MethodPatch, routePools + "/" + pool, map[string]any{}, runner.EventPoolChanged},
		{
			http.MethodPost, routePools + "/" + pool + "/join-tokens/" + tokenID + "/revoke", nil,
			runner.EventJoinTokenRevoked,
		},
		{http.MethodPost, routeBase + "/runners/" + r.id + "/drain", nil, runner.EventRunnerDrained},
		{http.MethodPost, routeBase + "/runners/" + r.id + "/retire", nil, runner.EventRunnerRetired},
		{http.MethodDelete, routePools + "/" + pool, nil, runner.EventPoolDeleted},
	}

	for _, s := range steps {
		if status, body = e.admin(s.method, s.path, s.body); status != http.StatusOK && status != http.StatusNoContent {
			t.Fatalf("%s %s = %d %s, want 2xx", s.method, s.path, status, body)
		}

		e.requireEvents(s.event)
	}

	if len(e.pools()) != 0 {
		t.Errorf("pools after delete = %d, want 0", len(e.pools()))
	}
}

// TestPoolRoutes_AuthRefusals proves 401 without a credential and 403 without
// the grant on each route.
func TestPoolRoutes_AuthRefusals(t *testing.T) {
	t.Parallel()

	e, manage := newToggleEnv(t)
	pool := e.createPool()

	r := e.newRunner("builder-1")
	if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s, want 200", status, body)
	}

	manage.Store(false)

	changes := routeTable(pool, r.id)

	for _, c := range changes {
		req, err := http.NewRequestWithContext(t.Context(), c.method, e.server.URL+c.path, http.NoBody)
		if err != nil {
			t.Fatalf("request: %v", err)
		}

		if status, body := e.send(req); status != http.StatusUnauthorized {
			t.Errorf("%s %s without credential = %d %s, want 401", c.method, c.path, status, body)
		}

		if !c.manage {
			continue
		}

		if status, body := e.admin(c.method, c.path, c.body); status != http.StatusForbidden {
			t.Errorf("%s %s without grant = %d %s, want 403", c.method, c.path, status, body)
		}
	}
}

// TestPoolRoutes_DeniedGrantChangesNothing calls each change route with no
// management grant and checks 403, the same pool rows and no audit entry.
func TestPoolRoutes_DeniedGrantChangesNothing(t *testing.T) {
	t.Parallel()

	e, manage := newToggleEnv(t)
	pool := e.createPool()

	r := e.newRunner("builder-1")
	if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s, want 200", status, body)
	}

	manage.Store(false)

	wantPools := e.pools()
	wantEvents := e.auditActions()

	for _, c := range routeTable(pool, r.id) {
		if !c.manage {
			continue
		}

		if status, body := e.admin(c.method, c.path, c.body); status != http.StatusForbidden {
			t.Errorf("%s %s = %d %s, want 403", c.method, c.path, status, body)
		}
	}

	if got := e.pools(); !reflect.DeepEqual(got, wantPools) {
		t.Errorf("pools after denied changes = %+v, want %+v", got, wantPools)
	}

	if got := e.auditActions(); !reflect.DeepEqual(got, wantEvents) {
		t.Errorf("audit events after denied changes = %v, want %v", got, wantEvents)
	}
}

type routeCase struct {
	method, path string
	body         any
	manage       bool
}

// routeTable lists each management route for the pool and the runner.
func routeTable(pool, runnerID string) []routeCase {
	return []routeCase{
		{http.MethodGet, routePools, nil, false},
		{http.MethodPost, routePools, map[string]any{"name": "other", "purpose": "jobs"}, true},
		{http.MethodGet, routePools + "/" + pool, nil, false},
		{http.MethodPatch, routePools + "/" + pool, map[string]any{}, true},
		{http.MethodDelete, routePools + "/" + pool, nil, true},
		{http.MethodGet, routePools + "/" + pool + "/join-tokens", nil, false},
		{http.MethodPost, routePools + "/" + pool + "/join-tokens", nil, true},
		{http.MethodPost, routePools + "/" + pool + "/join-tokens/" + routeNoSecret + "/revoke", nil, true},
		{http.MethodGet, routePools + "/" + pool + "/runners", nil, false},
		{http.MethodPost, routeBase + "/runners/" + runnerID + "/drain", nil, true},
		{http.MethodPost, routeBase + "/runners/" + runnerID + "/retire", nil, true},
	}
}

// pools returns the pools of acme from the store.
func (e *env) pools() []runner.Pool {
	e.t.Helper()

	pools, err := e.store.runners.Pools(e.t.Context(), "acme")
	if err != nil {
		e.t.Fatalf("list pools: %v", err)
	}

	return pools
}

// freshTokenID makes a join token that no runner used and returns its ID.
func (e *env) freshTokenID(pool string) string {
	e.t.Helper()

	status, body := e.admin(http.MethodPost, routePools+"/"+pool+"/join-tokens", nil)
	if status != http.StatusCreated {
		e.t.Fatalf("create join token = %d %s, want 201", status, body)
	}

	var tok struct {
		ID string `json:"id"`
	}

	decode(e.t, body, &tok)

	return tok.ID
}

// requireEvents fails unless the audit log of acme holds each event.
func (e *env) requireEvents(events ...runner.Event) {
	e.t.Helper()

	got := e.auditActions()
	for _, want := range events {
		if !got[string(want)] {
			e.t.Errorf("audit log has no entry for %s: %v", want, got)
		}
	}
}
