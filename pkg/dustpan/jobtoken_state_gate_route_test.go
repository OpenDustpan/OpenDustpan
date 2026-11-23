package dustpan_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/blob/fs"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/jobtoken"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/secret"
	"opendustpan.org/opendustpan/pkg/store/storetest"
)

// The tests in this file send the writes of a real job token, checked by the
// real token service, through the real routes that ask the state.write guard.

// gateDeniedCode is the error code that a refusal of the guard carries in its
// body on each route.
const gateDeniedCode = "gate.denied"

// gateJobStore is a job store that holds many jobs and counts each read.
type gateJobStore struct {
	job.Store

	mu   sync.Mutex
	jobs map[job.ID]job.Job
	gets int
}

// Get implements the read of job.Store.
func (s *gateJobStore) Get(_ context.Context, id job.ID) (job.Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.gets++

	j, ok := s.jobs[id]
	if !ok {
		return job.Job{}, job.ErrNotFound
	}

	return j, nil
}

func (s *gateJobStore) put(j *job.Job) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.jobs[j.ID] = *j
}

func (s *gateJobStore) remove(id job.ID) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.jobs, id)
}

// update changes the stored job id with change.
func (s *gateJobStore) update(t *testing.T, id job.ID, change func(j *job.Job)) {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	j, ok := s.jobs[id]
	if !ok {
		t.Fatalf("job %s is not in the store", id)
	}

	change(&j)
	s.jobs[id] = j
}

// copyJob stores a copy of the job from with the ID to.
func (s *gateJobStore) copyJob(t *testing.T, from, to job.ID) {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	j, ok := s.jobs[from]
	if !ok {
		t.Fatalf("job %s is not in the store", from)
	}

	j.ID = to
	s.jobs[to] = j
}

func (s *gateJobStore) reads() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.gets
}

// gateEnv is one server with the workspace acme/web, the job job-1 on it and
// a job token of job-1.
type gateEnv struct {
	f      fixture
	jobs   *gateJobStore
	tokens *jobTokenStateTokenStore
	token  string
	wsID   string
	now    time.Time
}

func newJobTokenStateGateServer(
	t *testing.T, st *sqlite.Store, blobs *fs.Store, now time.Time,
) (*dustpan.Server, *gateJobStore, *jobTokenStateTokenStore) {
	t.Helper()

	authn, err := identity.NewStaticToken("admin", adminToken)
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("key wrapper: %v", err)
	}

	jobStore := &gateJobStore{Store: nil, mu: sync.Mutex{}, jobs: map[job.ID]job.Job{}, gets: 0}
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

// setupJobTokenStateGate builds the server, seeds it with the fixture of the
// job token state routes, and then makes each write possible past the guard:
// the starter has a principal record, the seed lock is released, and the
// unlocked and forced writes are switched on.
func setupJobTokenStateGate(t *testing.T) gateEnv {
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

	// The server clock stays at this time, but it starts at the real time: the
	// database checks the start time of each blob write attempt of a Pulumi
	// checkpoint against its own clock, and refuses an attempt that started
	// more than a few minutes away from it.
	now := time.Now().UTC().Truncate(time.Second)

	srv, jobStore, tokenStore := newJobTokenStateGateServer(t, st, blobs, now)

	ts := httptest.NewServer(startedHandler(t, srv))
	t.Cleanup(ts.Close)

	f := fixture{store: st, server: ts, srv: srv}

	// The seed writes job-1 into a store of one job. The server reads the
	// store of many jobs, so job-1 moves there.
	scratch := new(jobTokenStateJobStore)
	tokens := seedJobTokenState(t, f, scratch, tokenStore, now)
	jobStore.put(&scratch.j)

	// The gates read the principal record of the starter of a job token.
	p := principal.Principal{
		CreatedAt: now, UpdatedAt: now, DisabledAt: time.Time{}, ID: "prn-alice", Subject: "alice",
		DisplayName: "alice", Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "",
		GroupCount: 0, BreakGlass: false,
	}
	if err := st.CreatePrincipal(t.Context(), p, storetest.ServerChange(now, principal.Ref(p.ID))); err != nil {
		t.Fatalf("create the starter: %v", err)
	}

	status, body := tofuDoPath(t, f, "UNLOCK", "/tofu/state/acme/web", `{"ID":"probe"}`)
	if status != http.StatusOK {
		t.Fatalf("release the seed lock: status %d, body %s", status, body)
	}

	env := gateEnv{f: f, jobs: jobStore, tokens: tokenStore, token: tokens.jobToken, wsID: "", now: now}
	env.wsID = env.workspaceID(t, "web")

	// job-1 runs its plan phase. A job with no phase at its current index
	// gives no claim, so the job token of such a job writes nothing.
	env.setPhase(t, "job-1", 0)

	for _, change := range []struct{ path, body string }{
		{
			path: "/api/dustpan/v1/namespaces/acme/override-switches",
			body: `{"allowForcedWrites":true,"allowUnlockedWrites":true}`,
		},
		{
			path: "/api/v2/workspaces/" + env.wsID + "/override-switches",
			body: `{"allow-forced-writes":true,"allow-unlocked-writes":true}`,
		},
	} {
		status, body := doWithAuth(t, f, authRequest{
			method: http.MethodPut, path: change.path, auth: "Bearer " + adminToken, body: change.body,
		})
		if status != http.StatusOK {
			t.Fatalf("PUT %s = %d, want 200: %s", change.path, status, body)
		}
	}

	return env
}

// workspaceID reads the TFE ID of the workspace acme/name.
func (e gateEnv) workspaceID(t *testing.T, name string) string {
	t.Helper()

	return e.dataID(t, "/api/v2/organizations/acme/workspaces/"+name)
}

// dataID reads data.id of the JSON:API document at path with the admin token.
func (e gateEnv) dataID(t *testing.T, path string) string {
	t.Helper()

	status, body := doWithAuth(t, e.f, authRequest{
		method: http.MethodGet, path: path, auth: "Bearer " + adminToken, body: "",
	})
	if status != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, status, body)
	}

	var doc gateDoc
	if err := json.Unmarshal([]byte(body), &doc); err != nil || doc.Data.ID == "" {
		t.Fatalf("GET %s: no data.id in %s (%v)", path, body, err)
	}

	return doc.Data.ID
}

// gateDoc is the part of a JSON:API document that the tests read.
type gateDoc struct {
	Data gateDocData `json:"data"`
}

type gateDocData struct {
	ID string `json:"id"`
}

// setApplyPhase puts the job id in its apply phase: the phases plan and apply,
// with the current phase apply.
func (e gateEnv) setApplyPhase(t *testing.T, id job.ID) {
	t.Helper()

	e.setPhase(t, id, 1)
}

// setPhase gives the job id the phases plan and apply, with the current phase
// at index current.
func (e gateEnv) setPhase(t *testing.T, id job.ID, current int) {
	t.Helper()

	e.jobs.update(t, id, func(j *job.Job) {
		var plan, apply job.Phase
		plan.Kind = job.PhasePlan
		apply.Kind = job.PhaseApply
		j.Phases = []job.Phase{plan, apply}
		j.Current = current
	})
}

// jobMatch returns the token record of job-1.
func (e gateEnv) jobMatch(t *testing.T) jobtoken.Match {
	t.Helper()

	var found jobtoken.Match

	for d := range e.tokens.matches {
		if e.tokens.matches[d].Token.JobID == "job-1" {
			found = e.tokens.matches[d]
		}
	}

	if found.Token.JobID != "job-1" {
		t.Fatal("no token record of job-1")
	}

	return found
}

// addJobToken stores a second job, a copy of job-1 with the ID id, and a job
// token of it, and returns the token.
func (e gateEnv) addJobToken(t *testing.T, id job.ID) string {
	t.Helper()

	e.jobs.copyJob(t, "job-1", id)

	s, err := jobtoken.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}

	m := e.jobMatch(t)
	m.Token.Digest = jobtoken.DigestOf(s)
	m.Token.JobID = id
	m.Claim.JobID = id

	matches := maps.Clone(e.tokens.matches)
	matches[m.Token.Digest] = m
	e.tokens.matches = matches

	return s.Reveal()
}

// bindJobToStack makes the Pulumi stack acme/web/prod and moves job-1 and its
// job token to it, so that the job token reaches the Pulumi routes of its unit.
func (e gateEnv) bindJobToStack(t *testing.T) {
	t.Helper()

	f := e.f

	idColl := placementCollectionID(t, f, "acme", "default")
	mustDo(t, f, call{
		method: http.MethodPost, path: "/api/dustpan/v1/namespaces/acme/placement-rules",
		body: `{"tool":"pulumi","project":"*","stack":"*","collectionID":"` + idColl + `"}`,
	}, http.StatusCreated)

	mustDo(t, f, call{
		method: http.MethodPost, path: "/api/stacks/acme/web", body: `{"stackName":"prod"}`,
	}, http.StatusOK)

	ns, err := namespace.ParseName("acme")
	if err != nil {
		t.Fatalf("namespace: %v", err)
	}

	obj, err := identity.UnitObject("pulumi", ns, "web/prod")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	var units access.Units = f.store

	id, found, err := units.UnitID(t.Context(), obj)
	if err != nil || !found {
		t.Fatalf("read the unit ID of the stack: found=%v err=%v", found, err)
	}

	unit := obj.WithUnitID(id)

	e.jobs.update(t, "job-1", func(j *job.Job) { j.Spec.Unit = unit })

	m := e.jobMatch(t)
	m.Token.Unit = unit
	m.Claim.JobUnitID = id

	matches := maps.Clone(e.tokens.matches)
	matches[m.Token.Digest] = m
	e.tokens.matches = matches
}

// gateUpdateStarted is the part of the answer to the start of an update that
// the tests read.
type gateUpdateStarted struct {
	Token string `json:"token"`
}

// startStackUpdate creates and starts an update of acme/web/prod with the admin
// token. The update holds the stack until it ends.
func (e gateEnv) startStackUpdate(t *testing.T) gateUpdate {
	t.Helper()

	id := createPulumiUpdate(t, e.f, "/api/stacks/acme/web/prod/update")

	var started gateUpdateStarted

	status, body := e.f.do(t, http.MethodPost, "/api/stacks/acme/web/prod/update/"+id, adminToken, `{}`)
	if status != http.StatusOK || json.Unmarshal([]byte(body), &started) != nil || started.Token == "" {
		t.Fatalf("start update: status %d, body %s", status, body)
	}

	return gateUpdate{id: id, lease: "update-token " + started.Token}
}

// gateUpdate is a started update: its ID and the Authorization value of its
// lease.
type gateUpdate struct {
	id    string
	lease string
}

// gateDeployment is a deployment body of the Pulumi CLI with no resources.
const gateDeployment = `{"version":3,"deployment":{"manifest":{},"resources":[]}}`

// seedStackVersion saves one checkpoint in a finished update of
// acme/web/prod with the admin token, so that the stack has version 1.
func (e gateEnv) seedStackVersion(t *testing.T) {
	t.Helper()

	u := e.startStackUpdate(t)
	prefix := "/api/stacks/acme/web/prod/update/" + u.id
	lease := u.lease

	status, body := doWithAuth(t, e.f, authRequest{
		method: http.MethodPatch, path: prefix + "/checkpoint", auth: lease, body: gateDeployment,
	})
	if status != http.StatusOK {
		t.Fatalf("save checkpoint: status %d, body %s", status, body)
	}

	status, body = doWithAuth(t, e.f, authRequest{
		method: http.MethodPost, path: prefix + "/complete", auth: lease, body: `{"status":"succeeded"}`,
	})
	if status != http.StatusOK {
		t.Fatalf("complete update: status %d, body %s", status, body)
	}
}

// seedWorkspace writes a first state to the workspace acme/name with the admin
// token and leaves it unlocked.
func (e gateEnv) seedWorkspace(t *testing.T, name string) {
	t.Helper()

	path := "/tofu/state/acme/" + name

	if status, body := tofuDoPath(t, e.f, "LOCK", path, `{"ID":"seed"}`); status != http.StatusOK {
		t.Fatalf("lock %s: status %d, body %s", name, status, body)
	}

	if status, body := tofuDoPath(t, e.f, http.MethodPost, path+"?ID=seed", gateState); status != http.StatusOK {
		t.Fatalf("write %s: status %d, body %s", name, status, body)
	}

	if status, body := tofuDoPath(t, e.f, "UNLOCK", path, `{"ID":"seed"}`); status != http.StatusOK {
		t.Fatalf("unlock %s: status %d, body %s", name, status, body)
	}
}

// adminLock takes the lock admin-lock of acme/web with the admin token.
func (e gateEnv) adminLock(t *testing.T) {
	t.Helper()

	status, body := tofuDoPath(t, e.f, "LOCK", "/tofu/state/acme/web", `{"ID":"admin-lock"}`)
	if status != http.StatusOK {
		t.Fatalf("admin lock: status %d, body %s", status, body)
	}
}

// gateState is a state with a serial above the seed state.
const gateState = `{"version":4,"serial":2,"lineage":"l1","outputs":{},"resources":[]}`

// The credential schemes of the three APIs.
const (
	schemeBasic  = "basic"
	schemeBearer = "bearer"
	schemeToken  = "token"
)

// gateReq is one request with a job token.
type gateReq struct {
	method string
	path   string
	body   string
	// scheme is how the request carries the token: basic auth for the
	// OpenTofu HTTP backend, Bearer for the TFE-compatible API and "token"
	// for the Pulumi Cloud-compatible API.
	scheme string
	// user is the basic-auth username. The server must ignore it.
	user string
}

// tofuReq is a request to the OpenTofu HTTP backend for the workspace
// acme/name, with the token in basic auth.
func tofuReq(method, name, body string) gateReq {
	return gateReq{method: method, path: "/tofu/state/acme/" + name, body: body, scheme: schemeBasic, user: ""}
}

// pulumiReq is a request to the Pulumi route of the stack acme/web/prod with
// the path suffix suffix, with the token in the "token" scheme.
func pulumiReq(method, suffix, body string) gateReq {
	path := "/api/stacks/acme/web/prod" + suffix

	return gateReq{method: method, path: path, body: body, scheme: schemeToken, user: ""}
}

func (e gateEnv) send(t *testing.T, token string, r gateReq) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), r.method, e.f.server.URL+r.path, strings.NewReader(r.body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	switch r.scheme {
	case schemeBasic:
		user := r.user
		if user == "" {
			user = "job"
		}

		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+token)))
	case schemeBearer:
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/vnd.api+json")
	case schemeToken:
		req.Header.Set("Authorization", "token "+token)
	default:
		t.Fatalf("unknown scheme %q", r.scheme)
	}

	resp, err := e.f.server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", r.method, r.path, err)
	}

	data, readErr := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	if readErr != nil {
		t.Fatalf("read body: %v", readErr)
	}

	return resp.StatusCode, string(data)
}

// gateRoute is one route that asks the state.write guard, with what it
// answers for a plan-only job and for a job that passed change.apply.
type gateRoute struct {
	name string
	// prep runs before the request, on the server of the case.
	prep func(t *testing.T, e gateEnv)
	// req builds the request. It runs after prep.
	req       func(t *testing.T, e gateEnv) gateReq
	planOnly  int
	afterPlan int
}

func noPrep(*testing.T, gateEnv) {}

func gateRoutes() []gateRoute {
	return []gateRoute{
		{
			name: "opentofu plan lock",
			prep: noPrep,
			req: func(*testing.T, gateEnv) gateReq {
				return tofuReq("LOCK", "web", `{"ID":"job-lock"}`)
			},
			planOnly: http.StatusOK, afterPlan: http.StatusOK,
		},
		{
			name: "tfe plan lock",
			prep: noPrep,
			req: func(_ *testing.T, e gateEnv) gateReq {
				return gateReq{
					method: http.MethodPost, path: "/api/v2/workspaces/" + e.wsID + "/actions/lock", body: "",
					scheme: schemeBearer, user: "",
				}
			},
			planOnly: http.StatusOK, afterPlan: http.StatusOK,
		},
		{
			name: "opentofu unlocked state write",
			prep: noPrep,
			req: func(*testing.T, gateEnv) gateReq {
				return tofuReq(http.MethodPost, "web", gateState)
			},
			planOnly: http.StatusForbidden, afterPlan: http.StatusOK,
		},
		{
			name: "opentofu force unlock",
			prep: func(t *testing.T, e gateEnv) { t.Helper(); e.adminLock(t) },
			req: func(*testing.T, gateEnv) gateReq {
				return tofuReq("UNLOCK", "web", `{"ID":"admin-lock"}`)
			},
			planOnly: http.StatusForbidden, afterPlan: http.StatusOK,
		},
		{
			name: "tfe force unlock",
			prep: func(t *testing.T, e gateEnv) { t.Helper(); e.adminLock(t) },
			req: func(_ *testing.T, e gateEnv) gateReq {
				return gateReq{
					method: http.MethodPost, path: "/api/v2/workspaces/" + e.wsID + "/actions/force-unlock", body: "",
					scheme: schemeBearer, user: "",
				}
			},
			planOnly: http.StatusForbidden, afterPlan: http.StatusOK,
		},
		{
			name: "tfe restore",
			prep: noPrep,
			req: func(t *testing.T, e gateEnv) gateReq {
				t.Helper()

				sv := e.dataID(t, "/api/v2/workspaces/"+e.wsID+"/current-state-version")

				return gateReq{
					method: http.MethodPost, path: "/api/v2/state-versions/" + sv + "/actions/restore", body: "",
					scheme: schemeBearer, user: "",
				}
			},
			planOnly: http.StatusForbidden, afterPlan: http.StatusOK,
		},
		{
			name: "tfe delete",
			prep: noPrep,
			req: func(*testing.T, gateEnv) gateReq {
				return gateReq{
					method: http.MethodDelete, path: "/api/v2/organizations/acme/workspaces/web", body: "",
					scheme: schemeBearer, user: "",
				}
			},
			planOnly: http.StatusForbidden, afterPlan: http.StatusNoContent,
		},
		{
			name: "pulumi delete",
			prep: func(t *testing.T, e gateEnv) { t.Helper(); e.bindJobToStack(t) },
			req: func(*testing.T, gateEnv) gateReq {
				return pulumiReq(http.MethodDelete, "", "")
			},
			planOnly: http.StatusForbidden, afterPlan: http.StatusNoContent,
		},
		{
			name: "pulumi rename",
			prep: func(t *testing.T, e gateEnv) { t.Helper(); e.bindJobToStack(t) },
			req: func(*testing.T, gateEnv) gateReq {
				return pulumiReq(http.MethodPost, "/rename", `{"newName":"prod"}`)
			},
			planOnly: http.StatusForbidden, afterPlan: http.StatusOK,
		},
		{
			name: "pulumi import",
			prep: func(t *testing.T, e gateEnv) { t.Helper(); e.bindJobToStack(t) },
			req: func(*testing.T, gateEnv) gateReq {
				return pulumiReq(http.MethodPost, "/import", gateDeployment)
			},
			planOnly: http.StatusForbidden, afterPlan: http.StatusOK,
		},
		{
			name: "pulumi restore",
			prep: func(t *testing.T, e gateEnv) {
				t.Helper()
				e.bindJobToStack(t)
				e.seedStackVersion(t)
			},
			req: func(*testing.T, gateEnv) gateReq {
				return pulumiReq(http.MethodPost, "/restore/1", "")
			},
			planOnly: http.StatusForbidden, afterPlan: http.StatusOK,
		},
		{
			name: "pulumi cancel",
			prep: func(t *testing.T, e gateEnv) { t.Helper(); e.bindJobToStack(t) },
			// The update starts here, after prep, because the request names
			// its ID. It holds the stack when the job token cancels it.
			req: func(t *testing.T, e gateEnv) gateReq {
				t.Helper()

				u := e.startStackUpdate(t)

				return pulumiReq(http.MethodPost, "/update/"+u.id+"/cancel", "")
			},
			planOnly: http.StatusForbidden, afterPlan: http.StatusOK,
		},
	}
}

// checkGateAnswer fails the test when the answer is not want. A 403 must be
// the refusal of the guard, and any other answer must not be.
func checkGateAnswer(t *testing.T, status int, body string, want int) {
	t.Helper()

	if status != want {
		t.Fatalf("status %d, body %s, want %d", status, body, want)
	}

	refusedByGuard := strings.Contains(body, gateDeniedCode)
	if (want == http.StatusForbidden) != refusedByGuard {
		t.Fatalf("status %d, body %s: refusal of the guard = %v, want %v",
			status, body, refusedByGuard, want == http.StatusForbidden)
	}
}

// TestJobTokenStateGatePlanOnly proves that the job token of a job in its plan
// phase, which did not pass change.apply, takes the lock of its plan and is
// refused by the guard on each write past that lock.
func TestJobTokenStateGatePlanOnly(t *testing.T) {
	t.Parallel()

	for _, tc := range gateRoutes() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := setupJobTokenStateGate(t)
			tc.prep(t, env)

			status, body := env.send(t, env.token, tc.req(t, env))
			checkGateAnswer(t, status, body, tc.planOnly)
		})
	}
}

// TestJobTokenStateGateApplyPhase proves that the job token of a job in its
// apply phase writes past the lock of its plan on each route.
func TestJobTokenStateGateApplyPhase(t *testing.T) {
	t.Parallel()

	for _, tc := range gateRoutes() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := setupJobTokenStateGate(t)
			tc.prep(t, env)
			env.setApplyPhase(t, "job-1")

			status, body := env.send(t, env.token, tc.req(t, env))
			checkGateAnswer(t, status, body, tc.afterPlan)
		})
	}
}

// TestJobTokenStateGateOtherUnit proves that a job token writes no state of a
// unit that is not the unit of its job, also when its job passed
// change.apply. The rule of job tokens refuses the request before the guard,
// so the answer is the 403 of an authorization refusal on each API.
func TestJobTokenStateGateOtherUnit(t *testing.T) {
	t.Parallel()

	const ruleText = "job token is for one unit only"

	cases := []struct {
		name     string
		apply    bool
		req      func(t *testing.T, e gateEnv) gateReq
		wantText string
	}{
		{
			name: "opentofu lock, plan-only job", apply: false, wantText: ruleText,
			req: func(*testing.T, gateEnv) gateReq {
				return tofuReq("LOCK", "other", `{"ID":"job-lock"}`)
			},
		},
		{
			name: "opentofu lock, apply job", apply: true, wantText: ruleText,
			req: func(*testing.T, gateEnv) gateReq {
				return tofuReq("LOCK", "other", `{"ID":"job-lock"}`)
			},
		},
		{
			name: "opentofu unlocked state write, apply job", apply: true, wantText: ruleText,
			req: func(*testing.T, gateEnv) gateReq {
				return tofuReq(http.MethodPost, "other", gateState)
			},
		},
		{
			// The TFE-compatible API answers its own 403 of a refused action.
			name: "tfe lock, apply job", apply: true, wantText: `"code":"access.denied"`,
			req: func(t *testing.T, e gateEnv) gateReq {
				t.Helper()

				return gateReq{
					method: http.MethodPost, path: "/api/v2/workspaces/" + e.workspaceID(t, "other") + "/actions/lock",
					body: "", scheme: schemeBearer, user: "",
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := setupJobTokenStateGate(t)
			env.seedWorkspace(t, "other")

			if tc.apply {
				env.setApplyPhase(t, "job-1")
			}

			status, body := env.send(t, env.token, tc.req(t, env))
			if status != http.StatusForbidden {
				t.Fatalf("status %d, body %s, want 403", status, body)
			}

			if !strings.Contains(body, tc.wantText) || strings.Contains(body, gateDeniedCode) {
				t.Fatalf("body %s, want %s and no refusal of the guard", body, tc.wantText)
			}
		})
	}
}

// TestJobTokenStateGateEndedJob proves that the job token of a job that ended,
// or of a job that the store does not hold, writes no state. The token record
// still says that the job runs, so the token authenticates (no 401), and the
// rule of job tokens refuses it when it reads the job, before the guard. The
// OpenTofu HTTP backend answers that refusal with 403. The TFE-compatible API
// answers 404 for a workspace that the caller may not read either, so that
// the answer does not tell that the workspace exists.
func TestJobTokenStateGateEndedJob(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		prep     func(t *testing.T, e gateEnv)
		write    gateReq
		want     int
		wantText string
	}{
		{
			name: "canceled job, plan lock",
			prep: func(t *testing.T, e gateEnv) {
				t.Helper()

				e.jobs.update(t, "job-1", func(j *job.Job) { j.State = job.StateCanceled })
			},
			write: tofuReq("LOCK", "web", `{"ID":"job-lock"}`),
			want:  http.StatusForbidden, wantText: "permission denied",
		},
		{
			name: "canceled job in its apply phase, state write",
			prep: func(t *testing.T, e gateEnv) {
				t.Helper()

				e.setApplyPhase(t, "job-1")
				e.jobs.update(t, "job-1", func(j *job.Job) { j.State = job.StateCanceled })
			},
			write: tofuReq(http.MethodPost, "web", gateState),
			want:  http.StatusForbidden, wantText: "permission denied",
		},
		{
			name:  "unknown job, plan lock",
			prep:  func(_ *testing.T, e gateEnv) { e.jobs.remove("job-1") },
			write: tofuReq("LOCK", "web", `{"ID":"job-lock"}`),
			want:  http.StatusForbidden, wantText: "permission denied",
		},
		{
			name:  "unknown job, tfe force unlock",
			prep:  func(_ *testing.T, e gateEnv) { e.jobs.remove("job-1") },
			write: gateReq{method: http.MethodPost, path: "", body: "", scheme: schemeBearer, user: ""},
			want:  http.StatusNotFound, wantText: `"code":"workspace.not-found"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			env := setupJobTokenStateGate(t)
			tc.prep(t, env)

			r := tc.write
			if r.path == "" {
				r.path = "/api/v2/workspaces/" + env.wsID + "/actions/force-unlock"
			}

			status, body := env.send(t, env.token, r)
			if status != tc.want {
				t.Fatalf("status %d, body %s, want %d", status, body, tc.want)
			}

			if !strings.Contains(body, tc.wantText) || strings.Contains(body, gateDeniedCode) {
				t.Fatalf("body %s, want %s: the rule of job tokens refuses first, not the guard", body, tc.wantText)
			}
		})
	}
}

// TestJobTokenStateGateForgedToken proves that a value with the form of a job
// token that the server did not issue is refused with 401 on each state write
// route, and that the server reads no job for it.
func TestJobTokenStateGateForgedToken(t *testing.T) {
	t.Parallel()

	env := setupJobTokenStateGate(t)
	env.setApplyPhase(t, "job-1")

	forged, err := jobtoken.NewSecret()
	if err != nil {
		t.Fatalf("secret: %v", err)
	}

	before := env.jobs.reads()

	for _, r := range []gateReq{
		{method: "LOCK", path: "/tofu/state/acme/web", body: `{"ID":"job-lock"}`, scheme: schemeBasic, user: ""},
		{method: http.MethodPost, path: "/tofu/state/acme/web", body: gateState, scheme: schemeBasic, user: ""},
		{
			method: http.MethodPost, path: "/api/v2/workspaces/" + env.wsID + "/actions/force-unlock", body: "",
			scheme: schemeBearer, user: "",
		},
	} {
		status, body := env.send(t, forged.Reveal(), r)
		if status != http.StatusUnauthorized {
			t.Fatalf("%s %s with a forged token: status %d, body %s, want 401", r.method, r.path, status, body)
		}
	}

	if after := env.jobs.reads(); after != before {
		t.Fatalf("the job store was read %d times for a forged token, want 0", after-before)
	}

	// The control: the issued token makes the server read the job, so the
	// count above can see a read.
	status, body := env.send(t, env.token, gateReq{
		method: http.MethodPost, path: "/tofu/state/acme/web", body: gateState, scheme: schemeBasic, user: "",
	})
	if status != http.StatusOK {
		t.Fatalf("write with the issued token: status %d, body %s, want 200", status, body)
	}

	if env.jobs.reads() == before {
		t.Fatal("the job store was not read for the issued token")
	}
}

// TestJobTokenStateGateClientJobIgnored proves that the job of a write is the
// job of its token and never a job that the client names. No route reads a
// job ID from the request; the one client field that can name a job is the
// basic-auth username of the OpenTofu HTTP backend. job-1 is plan-only and
// job-2, on the same unit, is in its apply phase. A write with the token of
// job-1 and the username job-2 stays refused, while the token of job-2 writes.
func TestJobTokenStateGateClientJobIgnored(t *testing.T) {
	t.Parallel()

	env := setupJobTokenStateGate(t)
	job2Token := env.addJobToken(t, "job-2")

	env.setApplyPhase(t, "job-2")

	write := tofuReq(http.MethodPost, "web", gateState)
	write.user = "job-2"

	status, body := env.send(t, env.token, write)
	checkGateAnswer(t, status, body, http.StatusForbidden)

	checkGateDeniedEntry(t, env, "job-1")

	// The control: job-2 itself may write, so the refusal above is the claim
	// of job-1.
	write.user = "job-1"

	status, body = env.send(t, job2Token, write)
	checkGateAnswer(t, status, body, http.StatusOK)
}

// checkGateDeniedEntry reads the audit log of acme through the API and fails
// the test when it has no denied entry of the guard for the job jobID.
func checkGateDeniedEntry(t *testing.T, env gateEnv, jobID string) {
	t.Helper()

	status, body := doWithAuth(t, env.f, authRequest{
		method: http.MethodGet, path: "/api/audit/entries?organization=acme&pageSize=100",
		auth: "Bearer " + adminToken, body: "",
	})
	if status != http.StatusOK {
		t.Fatalf("audit entries: status %d, body %s", status, body)
	}

	var listing gateAuditListing
	if err := json.Unmarshal([]byte(body), &listing); err != nil {
		t.Fatalf("decode the audit entries: %v", err)
	}

	for _, e := range listing.Entries {
		if e.Outcome == "denied" && strings.Contains(e.Reason, gateDeniedCode) && e.JobID == jobID &&
			e.Object == "opentofu:acme/web" {
			return
		}
	}

	t.Fatalf("no denied entry with %s for job %s in %s", gateDeniedCode, jobID, body)
}

// gateAuditListing is the part of the audit list answer that the tests read.
type gateAuditListing struct {
	Entries []gateAuditEntry `json:"entries"`
}

type gateAuditEntry struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason"`
	JobID   string `json:"jobID"`
	Object  string `json:"object"`
}
