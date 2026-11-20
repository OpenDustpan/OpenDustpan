package dustpan_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/internal/store/sqlite"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/jobtoken"
	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
	"opendustpan.org/opendustpan/pkg/store/storetest"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// issuerServerURL is the public URL of the server of the job token tests.
// The join challenge and the session challenge sign it.
const issuerServerURL = "https://dustpan.test"

// otherStateMarker is a value of the state of the unit of no job. A job token
// that reads that state would hold it in its answer.
const otherStateMarker = "marker-of-the-other-unit"

// handlerTransport sends the requests of the runner protocol to a handler in
// the process.
type handlerTransport struct {
	h http.Handler
}

// Do implements session.Transport.
func (t handlerTransport) Do(ctx context.Context, req *session.Request) (*session.Response, error) {
	hr := httptest.NewRequestWithContext(ctx, req.Method, req.Path, bytes.NewReader(req.Body))
	for k, v := range req.Header {
		hr.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	t.h.ServeHTTP(rec, hr)

	return &session.Response{Status: rec.Code, Body: rec.Body.Bytes(), RetryAfter: 0}, nil
}

// jobTokenServer is a server that issues job tokens, with its store and the
// client of the runner protocol.
type jobTokenServer struct {
	h      http.Handler
	st     *sqlite.Store
	client *session.Client
}

// newJobTokenServer returns a server over a SQLite store that serves the
// runner protocol with a job claimer and issues job tokens.
func newJobTokenServer(t *testing.T) *jobTokenServer {
	t.Helper()

	var st *sqlite.Store

	h := runnerServer(t, issuerServerURL+"/",
		func(s *sqlite.Store) dustpan.Option {
			st = s

			return dustpan.WithRunners(s.Runners())
		},
		func(s *sqlite.Store) dustpan.Option { return dustpan.WithJobStore(s.Jobs()) },
		func(s *sqlite.Store) dustpan.Option { return dustpan.WithJobTokenStore(s.JobTokens()) },
		func(s *sqlite.Store) dustpan.Option {
			return dustpan.WithJobClaimer(func(g job.GateGuard) job.Claimer {
				return job.NewClaimer(s.Jobs(), s.DurableOperations(), s, g)
			})
		},
	)

	client := &session.Client{
		Server: issuerServerURL, Transport: handlerTransport{h: h}, Release: "0.0.1", Now: nil,
	}

	return &jobTokenServer{h: h, st: st, client: client}
}

// do sends one request with the authorization header auth (empty sends none)
// and returns the status and the body.
func (s *jobTokenServer) do(t *testing.T, method, path, auth, body string) (int, string) {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}

	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, req)

	raw, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatalf("read the answer of %s %s: %v", method, path, err)
	}

	return rec.Code, string(raw)
}

// admin sends one request as the administrator.
func (s *jobTokenServer) admin(t *testing.T, method, path, body string) (int, string) {
	t.Helper()

	return s.do(t, method, path, "Bearer "+runnerAdminToken, body)
}

// putState makes the workspace name in the store and writes one state of it
// as the administrator: it locks the workspace, posts the state, and unlocks
// it.
func (s *jobTokenServer) putState(t *testing.T, name, state string) {
	t.Helper()

	ns, err := s.st.GetNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	ref, err := workspace.ParseName(name)
	if err != nil {
		t.Fatalf("workspace name: %v", err)
	}

	at := time.UnixMilli(1_700_000_000_000).UTC()
	w := workspace.Workspace{
		NamespaceID: "", CreatedAt: at, UpdatedAt: at,
		Lock:        workspace.Lock{LockedAt: time.Time{}, ID: "", Holder: "", Reason: ""},
		LastRelease: workspace.NoRelease(), ID: workspace.ID(ident.NewID("ws-")),
		Ref: workspace.Ref{Namespace: "acme", Name: ref}, Revision: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
		Origin:    identity.StateOrigin{Job: "", Commit: ""},
	}

	if err = s.st.CreateWorkspace(t.Context(), w, ns.Fence()); err != nil {
		t.Fatalf("create workspace %s: %v", name, err)
	}

	// A write needs the unit in a collection.
	status, body := s.admin(t, http.MethodPut, "/api/inventory/unit-collection?unit=opentofu:acme/"+name,
		`{"collection":"infra"}`)
	if status != http.StatusOK {
		t.Fatalf("put %s into the collection = %d %s, want 200", name, status, body)
	}

	path := "/tofu/state/acme/" + name
	lockID := `{"ID":"admin-lock"}`

	if status, body := s.admin(t, "LOCK", path, lockID); status != http.StatusOK {
		t.Fatalf("lock %s = %d %s, want 200", name, status, body)
	}

	if status, body := s.admin(t, http.MethodPost, path+"?ID=admin-lock", state); status != http.StatusOK {
		t.Fatalf("post the state of %s = %d %s, want 200", name, status, body)
	}

	if status, body := s.admin(t, "UNLOCK", path, lockID); status != http.StatusOK {
		t.Fatalf("unlock %s = %d %s, want 200", name, status, body)
	}
}

// unitObject returns the unit of the workspace name with its stable unit ID.
func (s *jobTokenServer) unitObject(t *testing.T, name string) identity.Object {
	t.Helper()

	entries, err := s.st.ListUnits(t.Context(), "acme")
	if err != nil {
		t.Fatalf("list units: %v", err)
	}

	for i := range entries {
		if entries[i].Name == name {
			unit, unitErr := identity.UnitObject(entries[i].Tool, "acme", name)
			if unitErr != nil {
				t.Fatalf("unit object: %v", unitErr)
			}

			return unit.WithUnitID(entries[i].UnitID)
		}
	}

	t.Fatalf("the namespace acme has no unit %s", name)

	return identity.Object{}
}

// createPool makes a pool for jobs and returns its ID.
func (s *jobTokenServer) createPool(t *testing.T) string {
	t.Helper()

	status, body := s.admin(t, http.MethodPost, "/api/dustpan/v1/namespaces/acme/runner-pools",
		`{"name":"prod","purpose":"jobs"}`)
	if status != http.StatusCreated {
		t.Fatalf("create pool = %d %s, want 201", status, body)
	}

	var pool struct {
		ID string `json:"id"`
	}

	decodeJSON(t, body, &pool)

	return pool.ID
}

// jobTokenRunner is a runner that joined a pool, with its key.
type jobTokenRunner struct {
	id   string
	priv ed25519.PrivateKey
}

// join makes a join token of the pool and joins a runner with it.
func (s *jobTokenServer) join(t *testing.T, pool string) jobTokenRunner {
	t.Helper()

	status, body := s.admin(t, http.MethodPost,
		"/api/dustpan/v1/namespaces/acme/runner-pools/"+pool+"/join-tokens", "")
	if status != http.StatusCreated {
		t.Fatalf("create join token = %d %s, want 201", status, body)
	}

	var created struct {
		Token string `json:"token"`
	}

	decodeJSON(t, body, &created)

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate the key of the runner: %v", err)
	}

	raw, err := json.Marshal(protocol.JoinRequest{
		Token: created.Token, Pool: pool, Name: "builder-1", Kind: string(runner.KindMachine),
		Isolation: string(runner.IsolationGVisor), Labels: nil, PublicKey: pub,
		Proof: ed25519.Sign(priv, runner.JoinChallenge(issuerServerURL, pool, "builder-1", created.Token)),
	})
	if err != nil {
		t.Fatalf("encode the join request: %v", err)
	}

	resp, err := s.client.Send(t.Context(), &session.Request{
		Method: http.MethodPost, Path: protocol.PathJoin, Body: raw, Header: nil,
	})
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("join = %+v, %v; want 200", resp, err)
	}

	var joined protocol.JoinResponse

	decodeJSON(t, string(resp.Body), &joined)

	return jobTokenRunner{id: joined.Runner, priv: priv}
}

// call sends a signed POST of the runner r and returns the status and the
// body.
func (s *jobTokenServer) call(t *testing.T, r jobTokenRunner, path string, body any) (int, []byte) {
	t.Helper()

	var raw []byte

	if body != nil {
		var err error

		if raw, err = json.Marshal(body); err != nil {
			t.Fatalf("encode the body of %s: %v", path, err)
		}
	}

	resp, err := s.client.Signed(t.Context(), r.priv, r.id, &session.Request{
		Method: http.MethodPost, Path: path, Body: raw, Header: nil,
	}, nil)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}

	return resp.Status, resp.Body
}

// newJobStarter makes the principal ada, the starter of the job, with the
// role planner on the namespace acme: ada may start a plan and read each unit
// of acme. A job token of ada acts on the unit of its job only, so each refusal
// on another unit is the refusal of the job token and not of a missing grant.
func (s *jobTokenServer) newJobStarter(t *testing.T) {
	t.Helper()

	at := time.UnixMilli(1_700_000_000_000).UTC()
	p := principal.Principal{
		CreatedAt: at, UpdatedAt: at, DisabledAt: time.Time{}, ID: "prn-ada", Subject: "ada",
		DisplayName: "ada", Kind: principal.KindPerson, Source: principal.SourceLocal, Namespace: "",
		GroupCount: 0, BreakGlass: false,
	}

	if err := s.st.CreatePrincipal(t.Context(), p, storetest.ServerChange(at, principal.Ref(p.ID))); err != nil {
		t.Fatalf("create the starter: %v", err)
	}

	storeGrant(t, s.st, "ada", access.RolePlanner, identity.NamespaceObject("acme"))
}

// dispatchJob stores a plan job of the pool on unit, with one tool step
// called plan and the starter ada, dispatched as the durable operation of its
// phase.
func (s *jobTokenServer) dispatchJob(t *testing.T, pool string, unit identity.Object) {
	t.Helper()

	ns, err := s.st.GetNamespace(t.Context(), "acme")
	if err != nil {
		t.Fatalf("get namespace: %v", err)
	}

	var j job.Job

	j.ID = job.ID(ident.NewID(job.IDPrefix))
	j.Spec = job.Spec{
		Unit: unit, Operation: job.OperationPlan, Purpose: "", Executor: job.ExecutorRunner,
		Location: job.Location{
			Connection: "github", Repository: "acme/infra", Ref: "main", Directory: "network", Revision: "abc123",
		},
		LocationSource: job.LocationSourceRequest,
		Steps: []job.Step{{
			Name: "plan", Role: job.RoleTool, Phase: job.PhasePlan, Args: []string{"tofu", "plan"}, Env: nil,
			Dir: "", Timeout: 0, AllowFailure: false, ChangesResources: false, Outputs: nil,
		}},
		Pool: pool, Profile: "prf-default", Edits: nil, Delivery: "", Defaulted: nil,
	}
	j.State = job.StateQueued
	j.Wait = job.WaitNone
	j.Phases = []job.Phase{{
		Kind: job.PhasePlan, Operation: "", Claims: 0, Executor: "", Steps: nil, StateVersion: "", Plan: nil,
		Gates: nil, Approvals: nil, StartedAt: time.Time{}, FinishedAt: time.Time{},
	}}
	j.StartedBy = "ada"
	j.Source = job.SourceAPI
	j.Verification = job.VerificationNone

	start := &durable.StartRequest{Key: job.PhaseKey(j.ID, 0), Kind: job.RunnerKind(pool), Version: 1, Input: nil}

	if _, created, createErr := s.st.Jobs().Create(t.Context(), j, start, ns.Fence(), nil); createErr != nil ||
		!created {
		t.Fatalf("create job = created %v, %v; want a dispatched job", created, createErr)
	}
}

// issueJobToken claims the phase of a job on the unit network through the
// runner protocol, opens its job session, and asks the credentials path for
// the token of the step plan. It returns the token and the job session.
func (s *jobTokenServer) issueJobToken(t *testing.T) (string, *session.Session) {
	t.Helper()

	pool := s.createPool(t)
	r := s.join(t, pool)

	s.newJobStarter(t)
	s.dispatchJob(t, pool, s.unitObject(t, "network"))

	status, body := s.call(t, r, protocol.PathPoll, protocol.PollRequest{
		Wait: protocol.FromStd(2 * time.Second), AssignmentVersion: runner.AssignmentVersion,
	})
	if status != http.StatusOK {
		t.Fatalf("poll = %d %s, want 200", status, body)
	}

	var work protocol.Work

	decodeJSON(t, string(body), &work)

	path := strings.NewReplacer("{operation}", work.Operation).Replace(protocol.PathClaimHandoff)

	status, body = s.call(t, r, path, nil)
	if status != http.StatusOK {
		t.Fatalf("handoff = %d %s, want 200", status, body)
	}

	var handoff protocol.HandoffResponse

	decodeJSON(t, string(body), &handoff)

	sess, err := session.Open(t.Context(), s.client, handoff.HandoffToken)
	if err != nil {
		t.Fatalf("open the job session: %v", err)
	}

	resp, err := sess.Post(t.Context(), protocol.PathSessionCredentials, protocol.CredentialsRequest{Step: "plan"})
	if err != nil || resp.Status != http.StatusOK {
		t.Fatalf("credentials = %+v, %v; want 200", resp, err)
	}

	var creds protocol.CredentialsResponse

	decodeJSON(t, string(resp.Body), &creds)

	if !strings.HasPrefix(creds.Token, jobtoken.SecretPrefix) {
		t.Fatalf("credentials token = %q, want a job token", creds.Token)
	}

	return creds.Token, sess
}

// The texts of the answers of the state routes to a job token.
const (
	oneUnitText  = "job token is for one unit only. Read the job to see its unit."
	tokenEndText = "job token ended. Read the job state."
)

// refusalWant is the answer that a refused request expects: the status and,
// when it is not empty, a text of the body.
type refusalWant struct {
	name   string
	status int
	text   string
}

// checkRefusal checks one refused request: the status is the one of want, the
// answer holds the text of want when that is not empty, and it holds no token
// and no state of the other unit.
func checkRefusal(t *testing.T, want refusalWant, status int, body, token string) {
	t.Helper()

	if status != want.status {
		t.Errorf("%s = %d %s, want %d", want.name, status, body, want.status)
	}

	if want.text != "" && !strings.Contains(body, want.text) {
		t.Errorf("%s: the answer %q holds no text %q", want.name, body, want.text)
	}

	if strings.Contains(body, otherStateMarker) {
		t.Errorf("%s: the answer holds the state of the other unit", want.name)
	}

	if strings.Contains(body, token) {
		t.Errorf("%s: the answer holds the token", want.name)
	}
}

// decodeJSON decodes body into out, or fails the test.
func decodeJSON(t *testing.T, body string, out any) {
	t.Helper()

	if err := json.Unmarshal([]byte(body), out); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
}

// TestJobTokenRefusedOnRESTRoute proves that a job token from the credentials
// path of a job session reaches no REST route and no unit of another job: each
// request answers 403, reads nothing and changes nothing. A state route of
// another unit answers the text of a token for one unit; an unknown token and
// a token that ended answer 401 with the text of a token that ended.
func TestJobTokenRefusedOnRESTRoute(t *testing.T) {
	t.Parallel()

	s := newJobTokenServer(t)

	status, body := s.admin(t, http.MethodPost, "/api/dustpan/v1/namespaces/acme/collections", `{"name":"infra"}`)
	if status != http.StatusCreated {
		t.Fatalf("create collection = %d %s, want 201", status, body)
	}

	otherState := fmt.Sprintf(`{"version":4,"serial":1,"lineage":"l1","terraform_version":"1.9.0",`+
		`"outputs":{"o":{"value":%q,"type":"string"}},"resources":[]}`, otherStateMarker)

	s.putState(t, "network", `{"version":4,"serial":1,"lineage":"l0","outputs":{},"resources":[]}`)
	s.putState(t, "other", otherState)

	token, sess := s.issueJobToken(t)

	cases := []struct {
		name   string
		method string
		path   string
		auth   string
		body   string
		text   string
	}{
		{
			"REST read of the collections", http.MethodGet, "/api/dustpan/v1/namespaces/acme/collections",
			"token " + token, "", "",
		},
		{
			"REST create of a collection", http.MethodPost, "/api/dustpan/v1/namespaces/acme/collections",
			"token " + token, `{"name":"stolen"}`, "",
		},
		{"state read of another unit", http.MethodGet, "/tofu/state/acme/other", "Bearer " + token, "", oneUnitText},
		{
			"lock of another unit", "LOCK", "/tofu/state/acme/other", "Bearer " + token, `{"ID":"job-lock"}`,
			oneUnitText,
		},
		{
			"state write of another unit", http.MethodPost, "/tofu/state/acme/other?ID=admin-lock", "Bearer " + token,
			`{"version":4,"serial":2,"lineage":"l1","outputs":{},"resources":[]}`, oneUnitText,
		},
	}

	for _, tc := range cases {
		got, answer := s.do(t, tc.method, tc.path, tc.auth, tc.body)
		checkRefusal(t, refusalWant{name: tc.name, status: http.StatusForbidden, text: tc.text}, got, answer, token)
	}

	checkNothingChanged(t, s)

	// A token that the server never issued, and the token after its job
	// ended, answer 401 with the text of a token that ended.
	unknown, err := jobtoken.NewSecret()
	if err != nil {
		t.Fatalf("new secret: %v", err)
	}

	status, body = s.do(t, http.MethodGet, "/tofu/state/acme/other", "Bearer "+unknown.Reveal(), "")
	checkRefusal(t, refusalWant{
		name: "state read with an unknown token", status: http.StatusUnauthorized, text: tokenEndText,
	}, status, body, token)

	resp, err := sess.Post(t.Context(), protocol.PathSessionFinish, protocol.Finish{Succeeded: false, Message: ""})
	if err != nil || resp.Status != http.StatusNoContent {
		t.Fatalf("finish = %+v, %v; want 204", resp, err)
	}

	status, body = s.do(t, http.MethodGet, "/tofu/state/acme/other", "Bearer "+token, "")
	checkRefusal(t, refusalWant{
		name: "state read after the job ended", status: http.StatusUnauthorized, text: tokenEndText,
	}, status, body, token)
}

// checkNothingChanged proves that the refused requests changed nothing: the
// namespace has one pool, the state of the other unit is the one that the
// administrator wrote, and the other unit has no lock.
func checkNothingChanged(t *testing.T, s *jobTokenServer) {
	t.Helper()

	status, body := s.admin(t, http.MethodGet, "/api/dustpan/v1/namespaces/acme/collections", "")
	if status != http.StatusOK {
		t.Fatalf("list collections = %d %s, want 200", status, body)
	}

	if strings.Contains(body, "stolen") {
		t.Errorf("the refused create made a collection: %s", body)
	}

	status, body = s.admin(t, http.MethodGet, "/tofu/state/acme/other", "")
	if status != http.StatusOK || !strings.Contains(body, otherStateMarker) || !strings.Contains(body, `"serial":1`) {
		t.Errorf("state of the other unit = %d %s, want serial 1 with the marker", status, body)
	}

	// A lock that the job token took would refuse this lock of the
	// administrator.
	if status, body = s.admin(t, "LOCK", "/tofu/state/acme/other", `{"ID":"admin-check"}`); status != http.StatusOK {
		t.Errorf("lock of the other unit after the refusals = %d %s, want 200", status, body)
	}
}
