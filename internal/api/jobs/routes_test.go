package jobs_test

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	jobsapi "opendustpan.org/opendustpan/internal/api/jobs"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// packageSources holds every Go file in this package, so TestNoMuxBypass can
// prove that no production file reaches the mux without recording.
//
//go:embed *.go
var packageSources embed.FS

const (
	testToken = "jobs-test-token"
	testJob   = "job-abcdefghijklmnopqrst"
	otherJob  = "job-uvwxyzabcdefghijklmn"
	testUnit  = "unt-abcdefghijklmnopqrst"
)

// errStore is the failure of a store that cannot read. Its text must not
// reach a caller.
var errStore = errors.New("driver: database is locked at /var/lib/secret/path")

// fakeNamespaces answers every namespace except "absent", homed in the test
// region.
type fakeNamespaces struct{}

func (fakeNamespaces) CreateNamespace(context.Context, namespace.Namespace) error { return nil }

func (fakeNamespaces) GetNamespace(_ context.Context, name namespace.Name) (namespace.Namespace, error) {
	if name == "absent" {
		return namespace.Namespace{}, namespace.ErrNotFound
	}

	return namespace.Namespace{
		ID: namespace.NewID(), CreatedAt: time.Unix(0, 0), Name: name, HomeRegion: "test-region", FailoverVersion: 1,
		Overrides: namespace.Overrides{Forced: false, Unlocked: false},
	}, nil
}

func (fakeNamespaces) SetOverrides(context.Context, namespace.Name, namespace.Overrides, namespace.Fence) error {
	return nil
}

func (fakeNamespaces) ListNamespaces(context.Context) ([]namespace.Namespace, error) { return nil, nil }

func (fakeNamespaces) Ping(context.Context) error { return nil }

// fakeJobs is a job.Store that reads from a map and counts the other calls.
// Every method but Get fails the test of a route that calls it.
type fakeJobs struct {
	jobs   map[job.ID]job.Job
	getErr error
	calls  []string
	mu     sync.Mutex
}

func newFakeJobs(jobs ...job.Job) *fakeJobs {
	f := &fakeJobs{jobs: map[job.ID]job.Job{}, getErr: nil, calls: nil, mu: sync.Mutex{}}
	for i := range jobs {
		f.jobs[jobs[i].ID] = jobs[i]
	}

	return f
}

var errUnexpected = errors.New("the jobs API called a method that it must not call")

func (f *fakeJobs) Create(context.Context, job.Job, *durable.StartRequest, namespace.Fence, []audit.Entry,
) (job.Job, bool, error) {
	f.note("Create")

	return job.Job{}, false, errUnexpected
}

func (f *fakeJobs) Get(_ context.Context, id job.ID) (job.Job, error) {
	f.note("Get")

	if f.getErr != nil {
		return job.Job{}, f.getErr
	}

	j, ok := f.jobs[id]
	if !ok {
		return job.Job{}, job.ErrNotFound
	}

	return j, nil
}

func (f *fakeJobs) List(context.Context, job.Filter) ([]job.Job, error) {
	f.note("List")

	return nil, errUnexpected
}

func (f *fakeJobs) Apply(context.Context, job.Change, namespace.Fence, []audit.Entry) (job.Job, error) {
	f.note("Apply")

	return job.Job{}, errUnexpected
}

func (f *fakeJobs) ObserveStateVersion(context.Context, job.StateVersion, namespace.Fence) ([]job.Job, error) {
	f.note("ObserveStateVersion")

	return nil, errUnexpected
}

func (f *fakeJobs) SetVerification(context.Context, job.ID, job.Verification, namespace.Fence, []audit.Entry,
) error {
	f.note("SetVerification")

	return errUnexpected
}

func (f *fakeJobs) Orphans(context.Context, int) ([]job.Orphan, error) {
	f.note("Orphans")

	return nil, errUnexpected
}

func (f *fakeJobs) note(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, name)
}

func (f *fakeJobs) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.calls)
}

// tokenAuthenticator signs in the principal ada for testToken.
type tokenAuthenticator struct{}

func (tokenAuthenticator) AuthenticateToken(_ context.Context, token string) (identity.Principal, error) {
	if token != testToken {
		return identity.Principal{}, identity.ErrUnauthenticated
	}

	return identity.Principal{
		Subject: "ada", DisplayName: "ada", Admin: false, TokenID: "", TokenExpiresAt: time.Time{},
		TemporaryAdministrator: false, TemporaryExpiresAt: time.Time{}, Kind: "", Credential: "", JobID: "",
	}, nil
}

// errNoListing reports a call of ListObjects, which no route of this API makes.
var errNoListing = errors.New("no route lists objects")

// recordingAuthorizer allows the checks that holds names, as "<action> <object>",
// and records every check in order.
type recordingAuthorizer struct {
	holds  map[string]bool
	checks []string
	mu     sync.Mutex
}

func newAuthorizer(holds ...string) *recordingAuthorizer {
	a := &recordingAuthorizer{holds: map[string]bool{}, checks: nil, mu: sync.Mutex{}}
	for _, h := range holds {
		a.holds[h] = true
	}

	return a
}

func (a *recordingAuthorizer) Authorize(
	_ context.Context, _ identity.Principal, action identity.Action, obj identity.Object,
) error {
	key := string(action) + " " + obj.String()

	a.mu.Lock()
	defer a.mu.Unlock()

	a.checks = append(a.checks, key)

	if !a.holds[key] {
		return fmt.Errorf("%w: %s", identity.ErrDenied, key)
	}

	return nil
}

func (*recordingAuthorizer) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{}, errNoListing
}

func (a *recordingAuthorizer) seen() []string {
	a.mu.Lock()
	defer a.mu.Unlock()

	return slices.Clone(a.checks)
}

func newHandler(t *testing.T, authz identity.Authorizer, jobs job.Store) *jobsapi.Handler {
	t.Helper()

	return newStartHandler(t, authz, jobs, nil, nil)
}

// newStartHandler is newHandler with a job service and an audit store.
func newStartHandler(
	t *testing.T, authz identity.Authorizer, jobs job.Store, starter job.Starter, entries audit.Store,
) *jobsapi.Handler {
	t.Helper()

	// The handler needs an audit store; a test that reads the entries passes its own.
	if entries == nil {
		entries = newTestAudit()
	}

	namespaces, err := namespace.NewService(namespace.Config{Store: fakeNamespaces{}, Now: nil, Region: "test-region"})
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := jobsapi.NewHandler(jobsapi.Config{
		Namespaces: namespaces, Jobs: jobs, Starter: starter, Authenticator: tokenAuthenticator{}, Authorizer: authz,
		Audit: entries, OnAuditFailure: nil, DenialLimiter: nil, Sessions: nil, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// unitOf returns the unit reference of the workspace net in the namespace ns.
func unitOf(t *testing.T, ns namespace.Name, unitID string) identity.Object {
	t.Helper()

	o, err := identity.UnitObject("opentofu", ns, "net")
	if err != nil {
		t.Fatalf("unit object: %v", err)
	}

	return o.WithUnitID(unitID)
}

// runningJob returns a running runner job of the unit net in acme, with a
// plan phase that holds a plan, a step record with a log, a gate and an
// approval.
func runningJob(t *testing.T, id job.ID) job.Job {
	t.Helper()

	at := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)

	return job.Job{
		ID: id,
		Spec: job.Spec{
			Unit: unitOf(t, "acme", testUnit), Operation: job.OperationApply, Purpose: "", Executor: job.ExecutorRunner,
			Location: job.Location{
				Connection: "", Repository: "acme/infra", Ref: "main", Directory: "net", Revision: "abc123",
			},
			Steps: []job.Step{
				{
					Name:             "plan",
					Role:             job.RoleTool,
					Phase:            job.PhasePlan,
					Args:             []string{"tofu", "plan", "-var=token=hunter2"},
					Env:              map[string]string{"TF_LOG": "debug"},
					Dir:              "",
					Timeout:          0,
					AllowFailure:     false,
					ChangesResources: false,
					Outputs:          nil,
				},
			},
			Pool: "pool-1", Profile: "prf-secret-profile", Edits: nil, Delivery: "",
		},
		State:          job.StateRunning,
		Wait:           job.WaitNone,
		Reason:         "",
		Current:        1,
		StartedBy:      "ada",
		Source:         job.SourceSchedule,
		Agent:          "",
		AgentMode:      "",
		BreakGlass:     nil,
		Trigger:        nil,
		Delivery:       nil,
		Closed:         nil,
		Verification:   job.VerificationNone,
		IdempotencyKey: "key-1",
		RequestHash:    "",
		Version:        3,
		CreatedAt:      at,
		UpdatedAt:      at.Add(time.Minute),
		Phases: []job.Phase{
			{
				Kind: job.PhasePlan, Operation: "dop-internal-operation", Claims: 1, Executor: "runner-1",
				Steps: []job.StepRecord{{
					Name: "plan", Token: 1, ExitCode: 0, StartedAt: at, FinishedAt: at.Add(time.Second),
					Log: &job.LogRef{
						ID: "jlv-1", ItemKey: "ik-secret", Version: 1, Chunks: 2, SHA256: "", Size: 42,
						CreatedAt: at, ClosedAt: at.Add(time.Second), ExpiresAt: time.Time{}, RedactedAt: time.Time{},
						ReplacedBy: "", Matches: 0,
					},
				}},
				StateVersion: "sv-1",
				Plan: &job.PlanRef{
					ID: "pln-1", ItemKey: "ik-plan-secret", StateVersion: "sv-1",
					Parts:     []job.PlanPart{{Kind: job.OutputPlanDisplay, Size: 10, SHA256: "ab", Secret: false}},
					CreatedAt: at, ExpiresAt: at.Add(time.Hour), RedactedAt: time.Time{},
				},
				Gates: []job.Gate{
					{
						Type:      job.GateApproval,
						Approvals: 1,
						Groups:    []string{"ops"},
						AllowSelf: false,
						Timeout:   0,
						BindPlan:  true,
					},
				},
				Approvals: nil, StartedAt: at, FinishedAt: at.Add(time.Second),
			},
			{
				Kind: job.PhaseApply, Operation: "dop-internal-operation-2", Claims: 0, Executor: "", Steps: nil,
				StateVersion: "", Plan: nil, Gates: nil,
				Approvals: []job.Approval{{
					Gate: job.GateApproval, Subject: "bo", Agent: "", Verdict: job.VerdictApprove, Comment: "ok",
					Plan: nil, At: at, Auth: "", AuthAt: time.Time{},
				}},
				StartedAt: time.Time{}, FinishedAt: time.Time{},
			},
		},
	}
}

// request is one call of a test: the method, the path, the token and the body.
type request struct {
	method, path, token string
}

func serve(t *testing.T, h http.Handler, r request) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), r.method, r.path, strings.NewReader(""))
	if r.token != "" {
		req.Header.Set("Authorization", "token "+r.token)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func jobPath(ns, id string) string {
	return "/api/dustpan/v1/namespaces/" + ns + "/jobs/" + id
}

// holdsBoth are the checks of a caller that can read the job testJob.
func holdsBoth() []string {
	return []string{
		string(identity.ActionNamespaceRead) + " acme",
		string(identity.ActionJobRead) + " opentofu:acme/net",
	}
}

// TestRoutesDeclareActions walks every route and fails for a route with no
// scope, no read action or no resolver. It checks both directions: every
// served pattern has a table row, and every table row is served. The API
// serves no write route.
func TestRoutesDeclareActions(t *testing.T) {
	t.Parallel()

	handler := newHandler(t, newAuthorizer(), newFakeJobs())
	byPattern := map[string]jobsapi.Route{}

	for _, route := range handler.Routes() {
		pattern := route.Method + " " + route.Path
		if _, dup := byPattern[pattern]; dup {
			t.Fatalf("duplicate route %q", pattern)
		}

		byPattern[pattern] = route

		checkRoute(t, handler, pattern, &route)
	}

	if len(byPattern) == 0 {
		t.Fatal("the API serves no route")
	}

	for _, pattern := range handler.RegisteredPatterns() {
		if _, ok := byPattern[pattern]; !ok {
			t.Errorf("served route %q has no table row", pattern)
		}
	}
}

// checkRoute fails for a route that declares too little, and proves that the
// route fails closed for a request with no token: it answers 401.
func checkRoute(t *testing.T, handler http.Handler, pattern string, route *jobsapi.Route) {
	t.Helper()

	switch {
	case route.Scope != jobsapi.ScopeAuthorized:
		t.Errorf("route %q is not authorized: this API serves no public route", pattern)
	case !slices.Contains(route.Reads, identity.ActionJobRead):
		t.Errorf("route %q declares no job:read action", pattern)
	case route.Resolve == nil || route.Serve == nil:
		t.Errorf("route %q declares no resolver or no handler", pattern)
	case route.Method != http.MethodGet:
		t.Errorf("route %q: this slice serves reads only", pattern)
	default:
	}

	path := strings.NewReplacer("{namespace}", "acme", "{job_id}", testJob).Replace(route.Path)

	rec := serve(t, handler, request{method: route.Method, path: path, token: ""})
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("route %q with no token = %d, want 401", pattern, rec.Code)
	}
}

// TestNoMuxBypass proves every served route goes through the recorder: it
// fails if any production file in this package builds a ServeMux or touches
// the inner mux of patternMux outside routes.go.
func TestNoMuxBypass(t *testing.T) {
	t.Parallel()

	entries, err := packageSources.ReadDir(".")
	if err != nil {
		t.Fatalf("read package sources: %v", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		if name == "routes.go" || strings.HasSuffix(name, "_test.go") {
			continue
		}

		content, err := packageSources.ReadFile(name)
		if err != nil {
			t.Fatalf("read file %s: %v", name, err)
		}

		for _, needle := range []string{"NewServeMux", "servemux"} {
			if strings.Contains(string(content), needle) {
				t.Fatalf("file %s reaches the mux without recording (contains %q)", name, needle)
			}
		}
	}
}

// TestRouteEnforcesTheChecks proves the order and the answer of the checks:
// a refused namespace read or job read answers the one 404, and a caller who
// holds both passes.
func TestRouteEnforcesTheChecks(t *testing.T) {
	t.Parallel()

	checks := holdsBoth()

	for held := range len(checks) + 1 {
		t.Run(fmt.Sprintf("holding %d of %d checks", held, len(checks)), func(t *testing.T) {
			t.Parallel()

			authz := newAuthorizer(checks[:held]...)
			rec := serve(t, newHandler(t, authz, newFakeJobs(runningJob(t, testJob))),
				request{method: http.MethodGet, path: jobPath("acme", testJob), token: testToken})

			want := http.StatusNotFound
			if held == len(checks) {
				want = http.StatusOK
			}

			if rec.Code != want {
				t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), want)
			}

			wantChecks := checks[:min(held+1, len(checks))]
			if got := authz.seen(); !slices.Equal(got, wantChecks) {
				t.Errorf("checks = %q, want %q, in order", got, wantChecks)
			}

			if want == http.StatusNotFound && !strings.Contains(rec.Body.String(), `"message":"not found"`) {
				t.Errorf("body = %s, want the 404 of an unknown job", rec.Body.String())
			}
		})
	}
}

// TestUnreadableJobAnswersLikeUnknownJob proves a caller cannot tell a job
// that it may not read from a job that does not exist: the status and the
// body are the same.
func TestUnreadableJobAnswersLikeUnknownJob(t *testing.T) {
	t.Parallel()

	handler := newHandler(t, newAuthorizer(string(identity.ActionNamespaceRead)+" acme"),
		newFakeJobs(runningJob(t, testJob)))

	unreadable := serve(t, handler, request{method: http.MethodGet, path: jobPath("acme", testJob), token: testToken})
	unknown := serve(t, handler, request{method: http.MethodGet, path: jobPath("acme", otherJob), token: testToken})

	if unreadable.Code != unknown.Code || unreadable.Body.String() != unknown.Body.String() {
		t.Fatalf("unreadable = %d %s, unknown = %d %s; want the same answer", unreadable.Code,
			unreadable.Body.String(), unknown.Code, unknown.Body.String())
	}
}

// TestUnknownObjectsAnswerAlike proves an unknown namespace, an unknown job,
// a job of another namespace and a malformed job ID answer 404 before any
// check of the caller runs for the job.
func TestUnknownObjectsAnswerAlike(t *testing.T) {
	t.Parallel()

	foreign := runningJob(t, otherJob)
	foreign.Spec.Unit = unitOf(t, "other", testUnit)

	noUnitID := runningJob(t, "job-nounitidnounitidnoun")
	noUnitID.Spec.Unit = unitOf(t, "acme", "")

	authz := newAuthorizer()
	jobs := newFakeJobs(runningJob(t, testJob), foreign, noUnitID)
	handler := newHandler(t, authz, jobs)

	for _, path := range []string{
		jobPath("absent", testJob),
		jobPath("acme", "job-unknownunknownunknow"),
		// The job exists, in another namespace than the path names.
		jobPath("acme", otherJob),
		jobPath("other", testJob),
		// A stored job with no unit ID is no job that a reader may see.
		jobPath("acme", "job-nounitidnounitidnoun"),
		jobPath("BAD%20NAME", testJob),
		jobPath("acme", "not-a-job-id"),
		jobPath("acme", "job-"+strings.Repeat("a", 80)),
		jobPath("acme", "job-with%2Fslash"),
	} {
		rec := serve(t, handler, request{method: http.MethodGet, path: path, token: testToken})
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"message":"not found"`) {
			t.Errorf("GET %s = %d %s, want the 404 of an unknown job", path, rec.Code, rec.Body.String())
		}
	}

	if got := authz.seen(); len(got) != 0 {
		t.Errorf("checks = %q, want none for a job that does not exist", got)
	}
}

// TestWriteMethodsAreNotServed proves a method other than GET answers 405 and
// reaches no store method.
func TestWriteMethodsAreNotServed(t *testing.T) {
	t.Parallel()

	jobs := newFakeJobs(runningJob(t, testJob))
	handler := newHandler(t, newAuthorizer(holdsBoth()...), jobs)

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		rec := serve(t, handler, request{method: method, path: jobPath("acme", testJob), token: testToken})
		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s = %d, want 405", method, rec.Code)
		}
	}

	if got := jobs.called(); len(got) != 0 {
		t.Errorf("store calls = %q, want none", got)
	}
}

// TestJobDocument proves the document of a job read: the fields a person
// needs, and none of the fields that a reader must not get.
func TestJobDocument(t *testing.T) {
	t.Parallel()

	handler := newHandler(t, newAuthorizer(holdsBoth()...), newFakeJobs(runningJob(t, testJob)))
	rec := serve(t, handler, request{method: http.MethodGet, path: jobPath("acme", testJob), token: testToken})

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s, want 200", rec.Code, rec.Body.String())
	}

	body := rec.Body.String()

	for _, want := range []string{
		`"id":"` + testJob + `"`, `"namespace":"acme"`, `"tool":"OpenTofu"`, `"id":"` + testUnit + `"`,
		`"operation":"apply"`, `"executor":"runner"`, `"state":"running"`, `"current":1`, `"startedBy":"ada"`,
		`"version":3`, `"source":"schedule"`, `"kind":"plan"`, `"kind":"apply"`, `"stateVersion":"sv-1"`, `"pool":"pool-1"`,
		`"repository":"acme/infra"`, `"exitCode":0`, `"subject":"bo"`, `"verdict":"approve"`,
		`"name":"plan","role":"tool","phase":"plan","changesResources":false`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body lacks %s: %s", want, body)
		}
	}

	// A step answers its name, role and phase only. The server never answers
	// the arguments or the variables of a step, the internal operation of a
	// phase, the item key of a plan or a log, or the profile of the job.
	for _, secret := range []string{
		"hunter2", "TF_LOG", "dop-internal-operation", "ik-plan-secret", "ik-secret", "prf-secret-profile",
		`"args"`, `"env"`, `"idempotencyKey"`, "key-1",
	} {
		if strings.Contains(body, secret) {
			t.Errorf("body holds %q: %s", secret, body)
		}
	}

	if strings.Contains(body, "0001-01-01") {
		t.Errorf("body holds a zero time: %s", body)
	}
}

// TestJobDocumentShowsThePurpose proves that a drift check says so in its
// document, and that a job with no purpose leaves the member out.
func TestJobDocumentShowsThePurpose(t *testing.T) {
	t.Parallel()

	for purpose, want := range map[job.Purpose]bool{job.PurposeDriftCheck: true, job.PurposeNone: false} {
		j := runningJob(t, testJob)
		j.Spec.Purpose = purpose

		handler := newHandler(t, newAuthorizer(holdsBoth()...), newFakeJobs(j))
		rec := serve(t, handler, request{method: http.MethodGet, path: jobPath("acme", testJob), token: testToken})

		if got := strings.Contains(rec.Body.String(), `"purpose":"drift-check"`); got != want ||
			(!want && strings.Contains(rec.Body.String(), `"purpose"`)) {
			t.Errorf("purpose %q: body = %s, want the purpose member = %t", purpose, rec.Body.String(), want)
		}
	}
}

// TestStoreFailureAnswers500 proves a failure of the store answers 500 with
// no detail of the failure.
func TestStoreFailureAnswers500(t *testing.T) {
	t.Parallel()

	jobs := newFakeJobs()
	jobs.getErr = errStore
	handler := newHandler(t, newAuthorizer(holdsBoth()...), jobs)

	rec := serve(t, handler, request{method: http.MethodGet, path: jobPath("acme", testJob), token: testToken})
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "secret") ||
		strings.Contains(rec.Body.String(), "database") {
		t.Fatalf("status = %d %s, want 500 with no detail", rec.Code, rec.Body.String())
	}
}

// TestNewHandlerNeedsItsDependencies proves a missing dependency fails the
// build of the handler.
func TestNewHandlerNeedsItsDependencies(t *testing.T) {
	t.Parallel()

	_, err := jobsapi.NewHandler(jobsapi.Config{
		Namespaces:     nil,
		Jobs:           nil,
		Starter:        nil,
		Authenticator:  nil,
		Authorizer:     nil,
		Audit:          newTestAudit(),
		OnAuditFailure: nil,
		DenialLimiter:  nil,
		Sessions:       nil,
		Logger:         nil,
	})
	if !errors.Is(err, jobsapi.ErrMissingDependency) {
		t.Fatalf("NewHandler with no dependency = %v, want ErrMissingDependency", err)
	}
}

// kindsOf returns the kind that the description gives to the object of each
// check, by action. A check is written as "<action> <object>".
func kindsOf(t *testing.T, checks []string) map[string]string {
	t.Helper()

	out := map[string]string{}

	for _, check := range checks {
		action, text, _ := strings.Cut(check, " ")

		obj, err := identity.ParseObject(text)
		if err != nil {
			t.Fatalf("object of check %q: %v", check, err)
		}

		switch {
		case obj.IsUnit():
			out[action] = apidesc.KindUnit
		case obj.IsNamespace():
			out[action] = apidesc.KindNamespace
		case obj.IsCollection():
			out[action] = apidesc.KindCollection
		case obj.IsCaller():
			out[action] = apidesc.KindCaller
		case obj.IsServer():
			out[action] = apidesc.KindServer
		default:
			t.Fatalf("check %q has an object of no kind in the description", check)
		}
	}

	return out
}

// TestDescriptionHoldsTheChecksOfTheRoute sends a request that passes every
// check, and compares the checks that the authorizer saw with the actions and
// the objects of the description. A check that the description leaves out, a
// check on another kind of object, and an action that no check makes all fail
// the test.
func TestDescriptionHoldsTheChecksOfTheRoute(t *testing.T) {
	t.Parallel()

	authz := newAuthorizer(holdsBoth()...)
	handler := newHandler(t, authz, newFakeJobs(runningJob(t, testJob)))

	rec := serve(t, handler, request{method: http.MethodGet, path: jobPath("acme", testJob), token: testToken})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s, want 200", rec.Code, rec.Body.String())
	}

	got := kindsOf(t, authz.seen())

	ops := handler.Operations()
	if len(ops) != 1 {
		t.Fatalf("Operations() = %d operations, want 1", len(ops))
	}

	if !maps.Equal(got, ops[0].ActionObjects) {
		t.Errorf("the route checks %v, and the description lists %v", got, ops[0].ActionObjects)
	}
}
