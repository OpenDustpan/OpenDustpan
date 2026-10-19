package runnerapi_test

import (
	"context"
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
		ID: namespace.NewID(), CreatedAt: time.UnixMilli(1_700_000_000_000).UTC(), Name: "acme", HomeRegion: region,
		FailoverVersion: 1, Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	})
	if err != nil {
		t.Fatalf("create namespace: %v", err)
	}

	service, err := namespace.NewService(namespace.Config{Store: s, Now: nil, Region: region})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	members := s.Runners()

	h, err := runnerapi.NewHandler(runnerapi.Config{
		Namespaces: service, Pools: members, Members: members, Handoffs: members, Claimer: nil, Sweeps: nil,
		Operations: nil, Authenticator: adminAuthenticator{}, Authorizer: holdsOnly{actions: actions}, Sessions: nil,
		Logger: slog.New(slog.DiscardHandler), Release: serverRelease(), ServerURL: serverURL, Now: nil,
		PollInterval: 0,
	})
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

	entries := e.auditActions()
	for _, want := range []runner.Event{
		runner.EventPoolCreated, runner.EventJoinTokenCreated, runner.EventRunnerJoined, runner.EventRunnerDrained,
	} {
		if !entries[string(want)] {
			t.Errorf("audit log has no entry for %s", want)
		}
	}
}
