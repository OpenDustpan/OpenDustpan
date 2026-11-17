package dustpan_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/tofuunit"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/jobtoken"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
	"opendustpan.org/opendustpan/pkg/workspace"
)

type jobTokenStateEnv struct {
	fixture        fixture
	jobStore       *jobTokenStateJobStore
	tokenStore     *jobTokenStateTokenStore
	jobToken       string
	recreatedToken string
}

type jobTokenStateJobStore struct {
	job.Store

	j job.Job
}

func (s *jobTokenStateJobStore) Get(_ context.Context, id job.ID) (job.Job, error) {
	if id != s.j.ID {
		return job.Job{}, job.ErrNotFound
	}

	return s.j, nil
}

type jobTokenStateTokenStore struct {
	jobtoken.Store

	matches map[jobtoken.Digest]jobtoken.Match
}

func (s *jobTokenStateTokenStore) GetByDigest(_ context.Context, d jobtoken.Digest) (jobtoken.Match, error) {
	m, ok := s.matches[d]
	if !ok {
		return jobtoken.Match{}, jobtoken.ErrNotFound
	}

	return m, nil
}

type jobTokenStatePermissive struct{}

func (*jobTokenStatePermissive) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return nil
}

func (*jobTokenStatePermissive) ListObjects(
	context.Context, identity.Principal, identity.Action,
) (identity.Scope, error) {
	return identity.Scope{Namespaces: nil, Collections: nil, Units: nil, Principals: nil}, nil
}

func jobTokenStateDo(t *testing.T, ts *httptest.Server, token, path string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+path, http.NoBody)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if token != "" {
		req.SetBasicAuth("job", token)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	data, readErr := io.ReadAll(resp.Body)
	if readErr != nil {
		t.Fatalf("read body: %v", readErr)
	}
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Errorf("close body: %v", closeErr)
	}

	return resp.StatusCode, string(data)
}

// ScopeExact implements identity.Authorizer.
func (*jobTokenStatePermissive) ScopeExact() bool { return true }

func setupJobTokenState(t *testing.T) jobTokenStateEnv {
	t.Helper()

	st, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "dustpan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := st.Close(); closeErr != nil {
			t.Errorf("close store: %v", closeErr)
		}
	})

	blobs, err := fs.Open(filepath.Join(t.TempDir(), "blobs"))
	if err != nil {
		t.Fatalf("open blob store: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := blobs.Close(); closeErr != nil {
			t.Errorf("close blob store: %v", closeErr)
		}
	})

	now := time.Date(2026, time.October, 8, 21, 0, 0, 0, time.UTC)

	srv, jobStore, tokenStore := newJobTokenStateServer(t, st, blobs, now)

	ts := httptest.NewServer(startedHandler(t, srv))
	t.Cleanup(ts.Close)

	f := fixture{store: st, server: ts, srv: srv}

	tokens := seedJobTokenState(t, f, jobStore, tokenStore, now)

	return jobTokenStateEnv{
		fixture:        f,
		jobStore:       jobStore,
		tokenStore:     tokenStore,
		jobToken:       tokens.jobToken,
		recreatedToken: tokens.recreatedToken,
	}
}

func newJobTokenStateServer(
	t *testing.T, st *sqlite.Store, blobs *fs.Store, now time.Time,
) (*dustpan.Server, *jobTokenStateJobStore, *jobTokenStateTokenStore) {
	t.Helper()

	authn, err := identity.NewStaticToken("admin", adminToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	jobStore := new(jobTokenStateJobStore)
	tokenStore := new(jobTokenStateTokenStore)

	srv, err := dustpan.NewServer(
		dustpan.WithInstallation(installationName),
		dustpan.WithNamespaceStore(st),
		dustpan.WithStackStore(st),
		dustpan.WithUpdateStore(st),
		dustpan.WithCheckpointStore(st),
		dustpan.WithEventStore(st),
		dustpan.WithSecretKeyStore(st),
		dustpan.WithSecretWrapper(wrapper),
		dustpan.WithWorkspaceStore(st),
		dustpan.WithStateStore(st),
		dustpan.WithBlobStore(blobs),
		dustpan.WithInventoryReader(st),
		dustpan.WithAuthenticator(authn),
		dustpan.WithAuthorizer(&jobTokenStatePermissive{}),
		dustpan.WithJobStore(jobStore),
		dustpan.WithJobTokenStore(tokenStore),
		dustpan.WithClock(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}

	return srv, jobStore, tokenStore
}

type jobTokenStateTokens struct {
	jobToken       string
	recreatedToken string
}

func seedJobTokenState(
	t *testing.T, f fixture, jobStore *jobTokenStateJobStore,
	tokenStore *jobTokenStateTokenStore, now time.Time,
) jobTokenStateTokens {
	t.Helper()

	st := f.store
	_ = st

	status, body := serveServerAPI(t, f, http.MethodPost, "/api/server/namespaces", `{"name":"acme"}`)
	if status != http.StatusCreated {
		t.Fatalf("create namespace: status %d, body %s", status, body)
	}

	mustDo(t, f, call{
		method: http.MethodPost, path: "/api/dustpan/v1/namespaces/acme/collections", body: `{"name":"default"}`,
	}, http.StatusCreated)

	idColl := placementCollectionID(t, f, "acme", "default")
	mustDo(t, f, call{
		method: http.MethodPost, path: "/api/dustpan/v1/namespaces/acme/placement-rules",
		body: `{"tool":"opentofu","project":"*","stack":"*","collectionID":"` + idColl + `"}`,
	}, http.StatusCreated)

	const state = `{"version":4,"serial":1,"lineage":"l1","outputs":{},"resources":[]}`

	status, body = tofuDoPath(t, f, "LOCK", "/tofu/state/acme/web", `{"ID":"probe"}`)
	if status != http.StatusOK {
		t.Fatalf("lock the workspace: status %d, body %s", status, body)
	}

	status, body = tofuDoPath(t, f, http.MethodPost, "/tofu/state/acme/web?ID=probe", state)
	if status != http.StatusOK {
		t.Fatalf("write the state: status %d, body %s", status, body)
	}

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	ref := workspace.Ref{Namespace: ns, Name: workspace.Name("web")}
	unitNoID, err := tofuunit.Object(ref)
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	var unitsPort access.Units = st

	id, found, err := unitsPort.UnitID(t.Context(), unitNoID)
	if err != nil || !found {
		t.Fatalf("read unit ID: found=%v err=%v", found, err)
	}

	unit := unitNoID.WithUnitID(id)

	jobSecret, err := jobtoken.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}

	recreatedSecret, err := jobtoken.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}

	var jobValue job.Job
	jobValue.ID = "job-1"
	jobValue.Spec.Unit = unit
	jobValue.State = job.StateRunning
	jobStore.j = jobValue

	nowPlus := now.Add(time.Hour)

	match := jobtoken.Match{
		Token: jobtoken.Token{
			Digest:         jobtoken.DigestOf(jobSecret),
			StarterID:      "prn-alice",
			JobID:          "job-1",
			Namespace:      ns,
			Unit:           unit,
			ClaimOperation: "op-1",
			ClaimToken:     1,
			ExpiresAt:      nowPlus,
			CreatedAt:      now.Add(-time.Minute),
		},
		Claim: jobtoken.Claim{
			OperationState:      durable.StateRunning,
			OperationClaimToken: 1,
			LeaseEndsAt:         now.Add(time.Minute),
			JobID:               "job-1",
			JobState:            job.StateRunning,
			JobOperation:        "op-1",
			JobUnitID:           unit.UnitID(),
			JobStarterID:        "prn-alice",
			Starter: identity.Principal{
				Subject: "alice", DisplayName: "alice", Admin: false, Kind: identity.KindPerson,
				Credential: "", JobID: "",
				TokenID: "", TokenExpiresAt: time.Time{},
				TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{},
			},
			StarterRecordID: "prn-alice",
			StarterEnabled:  true,
		},
	}

	recreatedMatch := match
	recreatedMatch.Token.Digest = jobtoken.DigestOf(recreatedSecret)
	recreatedMatch.Claim.JobUnitID = "different-id"

	tokenStore.matches = map[jobtoken.Digest]jobtoken.Match{
		jobtoken.DigestOf(jobSecret):       match,
		jobtoken.DigestOf(recreatedSecret): recreatedMatch,
	}

	return jobTokenStateTokens{
		jobToken:       jobSecret.Reveal(),
		recreatedToken: recreatedSecret.Reveal(),
	}
}

func TestJobTokenReachesOwnUnitState(t *testing.T) {
	t.Parallel()

	env := setupJobTokenState(t)
	f := env.fixture
	jobToken := env.jobToken
	if jobToken == "" {
		t.Fatal("empty job token")
	}
	if !jobtoken.LooksLikeSecret(jobToken) {
		t.Fatalf("not a secret: %s", jobToken)
	}

	ts := f.server

	otherPath := "/tofu/state/acme/other"

	do := func(token, path string) (int, string) {
		return jobTokenStateDo(t, ts, token, path)
	}

	cases := []struct {
		name   string
		path   string
		token  string
		want   int
		before func()
	}{
		{name: "own unit", path: "/tofu/state/acme/web", token: jobToken, want: http.StatusOK, before: nil},
		{name: "another unit", path: otherPath, token: jobToken, want: http.StatusForbidden, before: nil},
		{
			name: "recreated unit", path: "/tofu/state/acme/web", token: env.recreatedToken,
			want: http.StatusUnauthorized, before: nil,
		},
		{name: "no token", path: "/tofu/state/acme/web", token: "", want: http.StatusUnauthorized, before: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if tc.before != nil {
				tc.before()
			}
			status, body := do(tc.token, tc.path)
			if status != tc.want {
				t.Fatalf("%s: status %d, body %s, want %d", tc.name, status, body, tc.want)
			}
		})
	}
}
