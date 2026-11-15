package runnerapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/runnerapi"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// holdsOnly allows the actions it lists on a namespace, and denies the rest.
type holdsOnly struct {
	actions []identity.Action
}

func (h holdsOnly) Authorize(_ context.Context, _ identity.Principal, action identity.Action, _ identity.Object) error {
	if slices.Contains(h.actions, action) {
		return nil
	}

	return identity.ErrDenied
}

var errNoListing = errors.New("no route lists objects")

func (holdsOnly) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{}, errNoListing
}

// handlerWith returns a handler over a fresh store whose authorizer holds
// only actions.
func handlerWith(t *testing.T, actions ...identity.Action) http.Handler {
	t.Helper()

	return handlerAuthorizing(t, holdsOnly{actions: actions})
}

// handlerAuthorizing returns a handler over a fresh store that asks authz.
func handlerAuthorizing(t *testing.T, authz identity.Authorizer, opts ...func(*runnerapi.Config)) *runnerapi.Handler {
	t.Helper()

	s, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}

	t.Cleanup(func() {
		if closeErr := s.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	err = s.CreateNamespace(t.Context(), namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                "acme",
		PrimaryInstallation: installationName,
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	service, err := namespace.NewService(namespace.Config{Store: s, Now: nil, Installation: installationName})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	members := s.Runners()

	cfg := runnerapi.Config{
		Audit:                      newTestAudit(),
		OnAuditFailure:             nil,
		Namespaces:                 service,
		Pools:                      members,
		Members:                    members,
		Handoffs:                   members,
		Claimer:                    nil,
		Sweeps:                     nil,
		Operations:                 nil,
		Recorder:                   nil,
		Jobs:                       nil,
		Authenticator:              adminAuthenticator{},
		Authorizer:                 authz,
		Sessions:                   nil,
		Logger:                     slog.New(slog.DiscardHandler),
		Release:                    serverRelease(),
		ServerURL:                  serverURL,
		Now:                        nil,
		PollInterval:               0,
		DenialLimiter:              nil,
		Plans:                      nil,
		Artifacts:                  nil,
		OnHandoffRefusalSuppressed: nil,
	}

	for _, opt := range opts {
		opt(&cfg)
	}

	h, err := runnerapi.NewHandler(cfg)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return h
}

// TestManagementActions proves the order of the checks of a management
// route: a caller that cannot read the namespace gets 404, a caller that can
// read it but lacks the action gets 403, and a caller with the action
// succeeds.
func TestManagementActions(t *testing.T) {
	t.Parallel()

	read, readRunners, manage := identity.ActionNamespaceRead, identity.ActionNamespaceReadRunners,
		identity.ActionNamespaceManageRunners

	tests := []struct {
		name    string
		holds   []identity.Action
		method  string
		path    string
		body    string
		wantErr int
	}{
		{"list without namespace:read", nil, http.MethodGet, "/api/runners/acme/pools", "", http.StatusNotFound},
		{
			"list without read-runners",
			[]identity.Action{read},
			http.MethodGet, "/api/runners/acme/pools", "",
			http.StatusForbidden,
		},
		{
			"list with read-runners",
			[]identity.Action{read, readRunners},
			http.MethodGet, "/api/runners/acme/pools", "",
			http.StatusOK,
		},
		{
			"create with read-runners only",
			[]identity.Action{read, readRunners},
			http.MethodPost, "/api/runners/acme/pools",
			`{"name":"prod","purpose":"jobs"}`, http.StatusForbidden,
		},
		{
			"create with manage-runners",
			[]identity.Action{read, manage},
			http.MethodPost, "/api/runners/acme/pools",
			`{"name":"prod","purpose":"jobs"}`, http.StatusCreated,
		},
		{
			"create with a bad name",
			[]identity.Action{read, manage},
			http.MethodPost, "/api/runners/acme/pools",
			`{"name":"Prod!","purpose":"jobs"}`, http.StatusBadRequest,
		},
		{
			"create with an unknown field",
			[]identity.Action{read, manage},
			http.MethodPost, "/api/runners/acme/pools",
			`{"name":"prod","purpose":"jobs","flavor":"red"}`, http.StatusBadRequest,
		},
		{
			"unknown namespace",
			[]identity.Action{read, readRunners},
			http.MethodGet, "/api/runners/absent/pools", "",
			http.StatusNotFound,
		},
		{
			"unknown pool",
			[]identity.Action{read, readRunners},
			http.MethodGet, "/api/runners/acme/pools/none", "",
			http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "token "+adminToken)

			rec := httptest.NewRecorder()
			handlerWith(t, tc.holds...).ServeHTTP(rec, req)

			if rec.Code != tc.wantErr {
				t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), tc.wantErr)
			}
		})
	}
}

// TestPoolCreateNamesTheHelperRule proves that a pool with a git credential
// helper that the server refuses answers 400 with a text that names the field
// and the rule, and not only that the request is not valid.
func TestPoolCreateNamesTheHelperRule(t *testing.T) {
	t.Parallel()

	for _, helper := range []string{`/usr/bin/x\"y`, "/usr/bin/x#y", "/usr/bin/x;y", "bin/helper", `/usr/bin/x\ny`} {
		body := `{"name":"prod","purpose":"jobs","gitCredentials":{"mode":"custom","helper":"` + helper + `"}}`
		req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/runners/acme/pools",
			strings.NewReader(body))
		req.Header.Set("Authorization", "token "+adminToken)

		rec := httptest.NewRecorder()
		handlerWith(t, identity.ActionNamespaceRead, identity.ActionNamespaceManageRunners).ServeHTTP(rec, req)

		var got struct {
			Message string `json:"message"`
		}

		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("helper %q: body %q: %v", helper, rec.Body.String(), err)
		}

		if rec.Code != http.StatusBadRequest || got.Message != runner.ErrInvalidHelper.Error() {
			t.Errorf("helper %q: status %d message %q, want 400 and %q", helper, rec.Code, got.Message,
				runner.ErrInvalidHelper.Error())
		}
	}
}

// tokenListing is the answer of the list of join tokens.
type tokenListing struct {
	JoinTokens []tokenID `json:"joinTokens"`
}

type tokenID struct {
	ID string `json:"id"`
}

// TestPoolAndTokenLifecycle drives the management API of pools and join
// tokens: create, read, the join token with its secret once, and the revoke.
func TestPoolAndTokenLifecycle(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	pool := e.createPool()

	status, body := e.admin(
		http.MethodPost,
		"/api/runners/acme/pools",
		map[string]any{"name": "prod", "purpose": "jobs"},
	)
	if status != http.StatusConflict {
		t.Fatalf("create pool again = %d %s, want 409", status, body)
	}

	status, body = e.admin(http.MethodGet, "/api/runners/acme/pools/prod", nil)
	if status != http.StatusOK || !strings.Contains(string(body), pool) {
		t.Fatalf("get pool by name = %d %s, want 200 with the ID", status, body)
	}

	status, body = e.admin(http.MethodPost, "/api/runners/acme/pools/"+pool+"/join-tokens", map[string]any{"ttl": 0})
	if status != http.StatusCreated || !strings.Contains(string(body), `"token":"dpj_`) ||
		!strings.Contains(string(body), "--token-stdin") {
		t.Fatalf("create join token = %d %s, want 201 with the secret and the command", status, body)
	}

	status, body = e.admin(http.MethodGet, "/api/runners/acme/pools/"+pool+"/join-tokens", nil)
	if status != http.StatusOK || strings.Contains(string(body), "dpj_") {
		t.Fatalf("list join tokens = %d %s, want 200 with no secret", status, body)
	}

	var listing tokenListing

	decode(t, body, &listing)

	if len(listing.JoinTokens) != 1 {
		t.Fatalf("join tokens = %d, want 1", len(listing.JoinTokens))
	}

	revoke := "/api/runners/acme/pools/" + pool + "/join-tokens/" + listing.JoinTokens[0].ID + "/revoke"
	if status, body = e.admin(http.MethodPost, revoke, nil); status != http.StatusNoContent {
		t.Fatalf("revoke = %d %s, want 204", status, body)
	}

	if status, body = e.newRunner("builder-1").join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join with a new token = %d %s, want 200", status, body)
	}
}

// TestRunnerManagement drives the list, the drain and the audit entries of
// the runners of a pool, and the refusal to delete a pool in use.
func TestRunnerManagement(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	pool := e.createPool()
	r := e.newRunner("builder-1")

	if status, body := r.join(pool, e.joinToken(pool)); status != http.StatusOK {
		t.Fatalf("join = %d %s", status, body)
	}

	status, body := e.admin(http.MethodGet, "/api/runners/acme/pools/prod/runners", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"status":"online"`) ||
		!strings.Contains(string(body), r.id) {
		t.Fatalf("list runners = %d %s, want the online runner", status, body)
	}

	status, body = e.admin(
		http.MethodPost,
		"/api/runners/acme/runners/"+r.id+"/drain",
		nil,
	)

	if status != http.StatusNoContent {
		t.Fatalf("drain = %d %s, want 204", status, body)
	}

	if work := r.poll(time.Second); work != nil {
		t.Fatalf("poll of a draining runner = %+v, want 204", work)
	}

	e.startOperation(pool)

	if status, body = e.admin(http.MethodDelete, "/api/runners/acme/pools/prod", nil); status != http.StatusConflict {
		t.Fatalf("delete a pool in use = %d %s, want 409", status, body)
	}

	e.retireThenDrain(r.id)

	entries := e.auditActions()
	for _, want := range []runner.Event{
		runner.EventPoolCreated, runner.EventJoinTokenCreated, runner.EventRunnerJoined, runner.EventRunnerDrained,
	} {
		if !entries[string(want)] {
			t.Errorf("audit log has no entry for %s", want)
		}
	}
}

// TestPoolUpdate drives PATCH on a pool: the fields of the body change, the
// others stay, the purpose never changes, and each change has an audit entry.
func TestPoolUpdate(t *testing.T) {
	t.Parallel()

	e := newEnv(t)
	pool := e.createPool()
	path := "/api/runners/acme/pools/" + pool

	status, body := e.admin(http.MethodPatch, path, map[string]any{"concurrency": 4, "lease": 120_000})
	if status != http.StatusOK || !strings.Contains(string(body), `"concurrency":4`) ||
		!strings.Contains(string(body), `"lease":120000`) || !strings.Contains(string(body), `"name":"prod"`) {
		t.Fatalf("patch = %d %s, want 200 with the new concurrency and lease, and the old name", status, body)
	}

	status, body = e.admin(http.MethodPatch, path, map[string]any{"name": "staging"})
	if status != http.StatusOK || !strings.Contains(string(body), `"name":"staging"`) ||
		!strings.Contains(string(body), `"concurrency":4`) {
		t.Fatalf("patch of the name = %d %s, want 200 with the name and the earlier concurrency", status, body)
	}

	tests := []struct {
		name string
		body any
		want int
	}{
		{"a purpose that differs", map[string]any{"purpose": "sweeps"}, http.StatusConflict},
		{"an unknown field", map[string]any{"flavor": "red"}, http.StatusBadRequest},
		{"a name that is not valid", map[string]any{"name": "Prod!"}, http.StatusBadRequest},
	}

	for _, tc := range tests {
		if status, body = e.admin(http.MethodPatch, path, tc.body); status != tc.want {
			t.Errorf("patch with %s = %d %s, want %d", tc.name, status, body, tc.want)
		}
	}

	status, body = e.admin(http.MethodPatch, "/api/runners/acme/pools/rp-missing", map[string]any{})
	if status != http.StatusNotFound {
		t.Errorf("patch of an unknown pool = %d %s, want 404", status, body)
	}

	if !e.auditActions()[string(runner.EventPoolChanged)] {
		t.Errorf("audit log has no entry for %s", runner.EventPoolChanged)
	}
}

// onlyIn allows every action on the objects of one namespace.
type onlyIn struct {
	ns namespace.Name
}

func (o onlyIn) Authorize(_ context.Context, _ identity.Principal, _ identity.Action, obj identity.Object) error {
	if obj.Namespace() == o.ns {
		return nil
	}

	return identity.ErrDenied
}

func (onlyIn) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{}, errNoListing
}

// TestOtherNamespaceIsNotFound proves that a principal of one namespace
// cannot drain, retire or revoke a runner or a join token of another
// namespace by its ID: each answers 404 and changes nothing.
func TestOtherNamespaceIsNotFound(t *testing.T) {
	t.Parallel()

	e := newEnv(t)

	other := namespace.Namespace{
		ID:                  namespace.NewID(),
		CreatedAt:           time.UnixMilli(1_700_000_000_000).UTC(),
		Name:                "globex",
		PrimaryInstallation: installationName,
		FailoverVersion:     1,
		Overrides:           namespace.Overrides{Forced: false, Unlocked: false},
	}
	if err := e.store.namespaces.CreateNamespace(t.Context(), other); err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	status, body := e.admin(
		http.MethodPost,
		"/api/runners/globex/pools",
		map[string]any{"name": "prod", "purpose": "jobs"},
	)
	if status != http.StatusCreated {
		t.Fatalf("create pool in globex = %d %s, want 201", status, body)
	}

	var pool struct {
		ID string `json:"id"`
	}

	decode(t, body, &pool)

	status, body = e.admin(http.MethodPost, "/api/runners/globex/pools/"+pool.ID+"/join-tokens", nil)
	if status != http.StatusCreated {
		t.Fatalf("create join token in globex = %d %s, want 201", status, body)
	}

	var tok struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}

	decode(t, body, &tok)

	foreign := e.newRunner("foreign")
	if status, body = foreign.join(pool.ID, e.joinToken2(pool.ID)); status != http.StatusOK {
		t.Fatalf("join in globex = %d %s, want 200", status, body)
	}

	// The same stores, behind an authorizer that holds acme only.
	_, srv, _ := serve(
		t,
		e.store,
		serveOptions{
			now:      nil,
			authz:    onlyIn{ns: "acme"},
			claimer:  nil,
			recorder: nil,
			jobs:     nil,
			logger:   nil,
			mutate:   nil,
		},
	)
	acme := &env{t: t, store: e.store, server: srv, handler: nil, claimer: nil, fence: e.fence, clock: nil}
	acmePool := acme.createPool()

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"drain by ID", http.MethodPost, "/api/runners/acme/runners/" + foreign.id + "/drain"},
		{"retire by ID", http.MethodPost, "/api/runners/acme/runners/" + foreign.id + "/retire"},
		{
			"revoke a token by ID",
			http.MethodPost,
			"/api/runners/acme/pools/" + acmePool + "/join-tokens/" + tok.ID + "/revoke",
		},
		{
			"revoke through the pool ID",
			http.MethodPost,
			"/api/runners/acme/pools/" + pool.ID + "/join-tokens/" + tok.ID + "/revoke",
		},
		{"read the pool by ID", http.MethodGet, "/api/runners/acme/pools/" + pool.ID},
		{"drain in the namespace", http.MethodPost, "/api/runners/globex/runners/" + foreign.id + "/drain"},
	}

	for _, tc := range tests {
		if status, body = acme.admin(tc.method, tc.path, nil); status != http.StatusNotFound {
			t.Errorf("%s = %d %s, want 404", tc.name, status, body)
		}
	}

	status, body = e.admin(http.MethodGet, "/api/runners/globex/pools/"+pool.ID+"/runners", nil)
	if status != http.StatusOK || !strings.Contains(string(body), `"draining":false`) ||
		strings.Contains(string(body), `"status":"retired"`) {
		t.Errorf("runners of globex = %d %s, want the runner not draining and not retired", status, body)
	}

	status, body = e.admin(http.MethodGet, "/api/runners/globex/pools/"+pool.ID+"/join-tokens", nil)
	if status != http.StatusOK || strings.Contains(string(body), `"revokedAt"`) {
		t.Errorf("join tokens of globex = %d %s, want none revoked", status, body)
	}
}

// retireThenDrain retires the runner and proves that a drain of a retired
// runner answers 409 with the reason.
func (e *env) retireThenDrain(id string) {
	e.t.Helper()

	status, body := e.admin(http.MethodPost, "/api/runners/acme/runners/"+id+"/retire", nil)
	if status != http.StatusNoContent {
		e.t.Fatalf("retire = %d %s, want 204", status, body)
	}

	status, body = e.admin(http.MethodPost, "/api/runners/acme/runners/"+id+"/drain", nil)
	if status != http.StatusConflict || !strings.Contains(string(body), "the runner is retired") {
		e.t.Fatalf("drain of a retired runner = %d %s, want 409 with the message that the runner is retired",
			status, body)
	}
}
