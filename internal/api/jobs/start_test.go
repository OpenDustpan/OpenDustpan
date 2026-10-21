package jobs_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	jobsapi "opendustpan.org/opendustpan/internal/api/jobs"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

const jobsURL = "/api/dustpan/v1/namespaces/acme/jobs"

var errDriver = errors.New("driver: password=hunter2 refused at /var/lib/secret/path")

// fakeStarter is a job.Starter that records the actor and the request of each
// start, and answers with its job, its created flag and its error.
type fakeStarter struct {
	job.Starter

	mu       sync.Mutex
	actors   []job.Actor
	requests []job.StartRequest
	started  job.Job
	created  bool
	err      error
	// keys, when it is not nil, makes a start with a key that it saw before
	// answer created false, as the job store does.
	keys map[string]bool
}

func (f *fakeStarter) StartOnce(_ context.Context, a job.Actor, req job.StartRequest) (job.Job, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.actors = append(f.actors, a)
	f.requests = append(f.requests, req)

	if f.err != nil {
		return job.Job{}, false, f.err
	}

	if f.keys != nil && req.IdempotencyKey != "" {
		seen := f.keys[req.IdempotencyKey]
		f.keys[req.IdempotencyKey] = true

		return f.started, !seen, nil
	}

	return f.started, f.created, nil
}

func (f *fakeStarter) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.requests)
}

// recordingAudit keeps the entries that the handler appends.
type recordingAudit struct {
	audit.Store

	mu      sync.Mutex
	entries []audit.Entry
}

func (a *recordingAudit) Append(_ context.Context, _ namespace.Fence, e audit.Entry) (audit.Entry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.entries = append(a.entries, e)

	return e, nil
}

func newFakeStarter(t *testing.T) *fakeStarter {
	t.Helper()

	j := runningJob(t, testJob)
	j.State = job.StateDispatched
	j.Source = job.SourceAPI

	return &fakeStarter{
		Starter: nil, mu: sync.Mutex{}, actors: nil, requests: nil, started: j, created: true, err: nil, keys: nil,
	}
}

const goodBody = `{"unit":{"tool":"opentofu","name":"net"},"operation":"plan","pool":"ci",` +
	`"location":{"repository":"acme/infra","ref":"main","directory":"net"},"reason":"check"}`

// post sends one POST to the jobs route with the headers.
func post(t *testing.T, h http.Handler, token, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, jobsURL, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "token "+token)
	}

	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func readsNamespace() []string { return []string{string(identity.ActionNamespaceRead) + " acme"} }

func TestStartRouteIsDeclared(t *testing.T) {
	t.Parallel()

	with := newStartHandler(t, newAuthorizer(), newFakeJobs(), newFakeStarter(t), nil)

	checkPostRoute(t, with)

	if rec := post(t, with, "", goodBody); rec.Code != http.StatusUnauthorized {
		t.Fatalf("POST with no token = %d, want 401", rec.Code)
	}

	for _, pattern := range with.RegisteredPatterns() {
		if strings.HasPrefix(pattern, "POST") && pattern != "POST "+jobsURLPattern {
			t.Errorf("served pattern %q is not the declared route", pattern)
		}
	}

	checkStartOperation(t, with.Operations())

	// A server without a job service starts no job.
	without := newHandler(t, newAuthorizer(readsNamespace()...), newFakeJobs())
	for _, r := range without.Routes() {
		if r.Method == http.MethodPost {
			t.Fatalf("a handler with no job service serves %s %s", r.Method, r.Path)
		}
	}

	if rec := post(t, without, testToken, goodBody); rec.Code != http.StatusNotFound {
		t.Fatalf("POST without a job service = %d, want 404", rec.Code)
	}
}

// checkPostRoute checks that the handler declares one authorized POST route.
func checkPostRoute(t *testing.T, h *jobsapi.Handler) {
	t.Helper()

	var found *jobsapi.Route

	for _, r := range h.Routes() {
		if r.Method == http.MethodPost {
			found = &r
		}
	}

	if found == nil {
		t.Fatal("a handler with a job service serves no POST route")
	}

	declares := found.Scope == jobsapi.ScopeAuthorized &&
		slices.Equal(found.Writes, []identity.Action{identity.ActionUnitRunPlan}) && len(found.Reads) == 0
	serves := found.Resolve != nil && found.Serve != nil

	if !declares || !serves || !found.IdempotencyKey || !found.ETag {
		t.Fatalf("route = %+v, want an authorized POST that declares unit:run-plan, the key and the ETag", found)
	}
}

func checkStartOperation(t *testing.T, ops []apidesc.Operation) {
	t.Helper()

	for _, op := range ops {
		if op.ID != "startJob" {
			continue
		}

		if !op.IdempotencyKey || !op.ETag || !slices.Equal(op.Actions, []string{"unit:run-plan"}) {
			t.Fatalf("operation = %+v, want the key, the ETag and unit:run-plan", op)
		}

		return
	}

	t.Fatalf("operations = %+v, want startJob", ops)
}

const jobsURLPattern = "/api/dustpan/v1/namespaces/{namespace}/jobs"

func TestStartJob(t *testing.T) {
	t.Parallel()

	starter := newFakeStarter(t)
	h := newStartHandler(t, newAuthorizer(readsNamespace()...), newFakeJobs(), starter, nil)

	rec := post(t, h, testToken, goodBody, "Idempotency-Key", "key-1", "Dustpan-Channel", "console")
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d %s, want 201", rec.Code, rec.Body.String())
	}

	checkStartResponse(t, rec)

	if starter.calls() != 1 {
		t.Fatalf("starts = %d, want 1", starter.calls())
	}

	checkStartRequest(t, starter.requests[0])

	if actor := starter.actors[0]; actor.Principal.Subject != "ada" || actor.Agent != "" || actor.Mode != "" ||
		actor.ConfirmAuth != "" {
		t.Fatalf("actor = %+v, want ada acting alone", actor)
	}
}

func checkStartResponse(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()

	if got := rec.Header().Get("Location"); got != jobsURL+"/"+testJob {
		t.Errorf("Location = %q, want the job", got)
	}

	if got := rec.Header().Get("ETag"); got != `"v3"` {
		t.Errorf("ETag = %q, want the version of the job in quotes", got)
	}

	if rec.Header().Get("Idempotent-Replayed") != "" {
		t.Error("the first request is marked as a replay")
	}

	if !strings.Contains(rec.Body.String(), `"id":"`+testJob+`"`) || strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("body = %s, want the job document", rec.Body.String())
	}
}

func checkStartLocation(t *testing.T, loc job.Location) {
	t.Helper()

	if loc.Repository != "acme/infra" || loc.Ref != "main" || loc.Directory != "net" {
		t.Fatalf("location = %+v, want the location of the body", loc)
	}
}

func checkStartRequest(t *testing.T, req job.StartRequest) {
	t.Helper()

	if req.Source != job.SourceAPI || req.Operation != job.OperationPlan || req.Executor != "" ||
		req.IdempotencyKey != "key-1" || req.Pool != "ci" || req.Reason != "check" ||
		req.Unit.String() != "opentofu:acme/net" || req.Unit.UnitID() != "" || req.BreakGlass != nil {
		t.Fatalf("request = %+v, want a plan of opentofu:acme/net with the source api and no unit ID", req)
	}

	checkStartLocation(t, req.Location)

	if req.Channel != audit.ChannelConsole {
		t.Errorf("channel = %q, want console", req.Channel)
	}
}

func TestStartJobReplay(t *testing.T) {
	t.Parallel()

	starter := newFakeStarter(t)
	starter.created = false
	h := newStartHandler(t, newAuthorizer(readsNamespace()...), newFakeJobs(), starter, nil)

	rec := post(t, h, testToken, goodBody, "Idempotency-Key", "key-1")
	if rec.Code != http.StatusCreated || rec.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("replay = %d, replayed %q; want the job again marked as a replay", rec.Code,
			rec.Header().Get("Idempotent-Replayed"))
	}

	// With no key a start is never a replay.
	rec = post(t, h, testToken, goodBody)
	if rec.Header().Get("Idempotent-Replayed") != "" {
		t.Fatal("a start with no key is marked as a replay")
	}
}

// TestStartRefusesHostileBodies proves a body that is not valid fails before
// the service sees it. A client never picks the source kind, the executor,
// the steps or the profile of a job.
func TestStartRefusesHostileBodies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		body     string
		wantCode string
	}{
		{"empty", ``, "request.body-invalid"},
		{"not json", `plan please`, "request.body-invalid"},
		{"an array", `[]`, "request.body-invalid"},
		{"a second document", goodBody + goodBody, "request.body-invalid"},
		{"trailing text", goodBody + ` x`, "request.body-invalid"},
		{
			"a source kind",
			strings.Replace(goodBody, `"reason"`, `"source":"schedule","reason"`, 1),
			"request.body-invalid",
		},
		{
			"an executor",
			strings.Replace(goodBody, `"reason"`, `"executor":"write-back","reason"`, 1),
			"request.body-invalid",
		},
		{
			"steps",
			strings.Replace(goodBody, `"reason"`, `"steps":[{"args":["sh","-c","id"]}],"reason"`, 1),
			"request.body-invalid",
		},
		{
			"a profile",
			strings.Replace(goodBody, `"reason"`, `"profile":"prf-prod","reason"`, 1),
			"request.body-invalid",
		},
		{
			"a break-glass",
			strings.Replace(goodBody, `"reason"`, `"breakGlass":{"reason":"x"},"reason"`, 1),
			"request.body-invalid",
		},
		{
			"a unit ID",
			strings.Replace(goodBody, `"name":"net"`, `"name":"net","id":"unt-x"`, 1),
			"request.body-invalid",
		},
		{"no unit", `{"operation":"plan","pool":"ci"}`, "request.body-invalid"},
		{"a unit with no name", `{"unit":{"tool":"opentofu"},"operation":"plan"}`, "request.body-invalid"},
		{"a unit that is null", `{"unit":null,"operation":"plan"}`, "request.body-invalid"},
		{
			"a number for the pool",
			`{"unit":{"tool":"opentofu","name":"net"},"operation":"plan","pool":7}`,
			"request.body-invalid",
		},
		{"an unknown operation", strings.Replace(goodBody, `"plan"`, `"reboot"`, 1), "job.invalid"},
		{"no operation", `{"unit":{"tool":"opentofu","name":"net"}}`, "job.invalid"},
		{"an operation in capitals", strings.Replace(goodBody, `"plan"`, `"PLAN"`, 1), "job.invalid"},
		{"a tool with a slash", strings.Replace(goodBody, `"opentofu"`, `"open/tofu"`, 1), "job.invalid"},
		{"a tool with a colon", strings.Replace(goodBody, `"opentofu"`, `"a:b"`, 1), "job.invalid"},
		{"a unit name with a tab", strings.Replace(goodBody, `"net"`, `"a\tb"`, 1), "job.invalid"},
		{
			"a unit name with a right-to-left override",
			strings.Replace(goodBody, `"net"`, `"a\u202eb"`, 1),
			"job.invalid",
		},
		{"a unit name with a line break", strings.Replace(goodBody, `"net"`, `"a\nb"`, 1), "job.invalid"},
		{"a unit name with a NUL byte", strings.Replace(goodBody, `"net"`, `"a\u0000b"`, 1), "job.invalid"},
		{
			"a body over the limit",
			`{"unit":{"tool":"opentofu","name":"` + strings.Repeat("a", 70<<10) + `"}}`,
			"request.body-invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			starter := newFakeStarter(t)
			h := newStartHandler(t, newAuthorizer(readsNamespace()...), newFakeJobs(), starter, nil)

			rec := post(t, h, testToken, tt.body)
			if rec.Code != http.StatusBadRequest ||
				!strings.Contains(rec.Body.String(), `"errorCode":"`+tt.wantCode+`"`) {
				t.Fatalf("status = %d %s, want 400 with %s", rec.Code, rec.Body.String(), tt.wantCode)
			}

			if starter.calls() != 0 {
				t.Fatalf("the service saw %d requests, want none", starter.calls())
			}
		})
	}
}

func TestStartRefusesABadKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		headers []string
	}{
		{"empty", []string{"Idempotency-Key", ""}},
		{"too long", []string{"Idempotency-Key", strings.Repeat("k", 256)}},
		{"a control character", []string{"Idempotency-Key", "a\tb"}},
		{"not ASCII", []string{"Idempotency-Key", "schlüssel"}},
		{"two keys", []string{"Idempotency-Key", "a", "Idempotency-Key", "b"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			starter := newFakeStarter(t)
			h := newStartHandler(t, newAuthorizer(readsNamespace()...), newFakeJobs(), starter, nil)

			rec := post(t, h, testToken, goodBody, tt.headers...)
			if rec.Code != http.StatusBadRequest ||
				!strings.Contains(rec.Body.String(), `"errorCode":"request.idempotency-key-invalid"`) {
				t.Fatalf("status = %d %s, want 400 with the key code", rec.Code, rec.Body.String())
			}

			if starter.calls() != 0 {
				t.Fatalf("the service saw %d requests, want none", starter.calls())
			}
		})
	}
}

// TestStartNeedsTheNamespace proves a caller who cannot read the namespace,
// and a namespace that does not exist, answer alike and reach no service.
func TestStartNeedsTheNamespace(t *testing.T) {
	t.Parallel()

	starter := newFakeStarter(t)
	h := newStartHandler(t, newAuthorizer(), newFakeJobs(), starter, nil)

	denied := post(t, h, testToken, goodBody)

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost,
		"/api/dustpan/v1/namespaces/absent/jobs", strings.NewReader(goodBody))
	req.Header.Set("Authorization", "token "+testToken)

	unknown := httptest.NewRecorder()
	h.ServeHTTP(unknown, req)

	if denied.Code != http.StatusNotFound || unknown.Code != http.StatusNotFound ||
		denied.Body.String() != unknown.Body.String() {
		t.Fatalf("denied = %d %s, unknown = %d %s; want the same 404", denied.Code, denied.Body.String(),
			unknown.Code, unknown.Body.String())
	}

	if starter.calls() != 0 {
		t.Fatalf("the service saw %d requests, want none", starter.calls())
	}
}

func TestStartMapsTheErrorsOfTheService(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err      error
		status   int
		wantCode string
		wantText string
	}{
		{fmt.Errorf("%w: no pool", job.ErrInvalid), http.StatusBadRequest, "job.invalid", "no pool"},
		{
			fmt.Errorf("%w: plans only", job.ErrUnsupported),
			http.StatusUnprocessableEntity,
			"job.unsupported",
			"plans only",
		},
		{
			fmt.Errorf("%w: plans are off", job.ErrPolicyDenied),
			http.StatusForbidden,
			"job.policy-denied",
			"plans are off",
		},
		{fmt.Errorf("%w: no agents", job.ErrAgentDenied), http.StatusForbidden, "job.agent-denied", "no agents"},
		{job.ErrFrozen, http.StatusConflict, "job.frozen", "freeze"},
		{job.ErrLimit, http.StatusTooManyRequests, "job.limit", "too many"},
		{job.ErrKeyConflict, http.StatusConflict, "request.idempotency-key-reused", "key"},
		{namespace.ErrFenced, http.StatusConflict, "namespace.fenced", ""},
		{fmt.Errorf("%w: unit and action", identity.ErrDenied), http.StatusForbidden, "", "forbidden"},
		{job.ErrNotFound, http.StatusNotFound, "", "not found"},
		{errDriver, http.StatusInternalServerError, "", "internal error"},
	}

	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			t.Parallel()

			starter := newFakeStarter(t)
			starter.err = tt.err
			h := newStartHandler(t, newAuthorizer(readsNamespace()...), newFakeJobs(), starter, nil)

			rec := post(t, h, testToken, goodBody)
			if rec.Code != tt.status {
				t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), tt.status)
			}

			checkErrorBody(t, rec.Body.String(), tt.wantCode, tt.wantText)
		})
	}
}

func checkErrorBody(t *testing.T, body, wantCode, wantText string) {
	t.Helper()

	if wantCode != "" && !strings.Contains(body, `"errorCode":"`+wantCode+`"`) {
		t.Errorf("body = %s, want the code %s", body, wantCode)
	}

	if !strings.Contains(body, wantText) {
		t.Errorf("body = %s, want the text %q", body, wantText)
	}

	for _, leak := range []string{"hunter2", "/var/lib", "unit and action"} {
		if strings.Contains(body, leak) {
			t.Errorf("body = %s, leaks %q", body, leak)
		}
	}
}

// TestStartDeniedWritesTheAuditEntry proves a refusal of the start action is
// in the audit log, with the subject and the unit, and that the answer is 403.
func TestStartDeniedWritesTheAuditEntry(t *testing.T) {
	t.Parallel()

	starter := newFakeStarter(t)
	starter.err = fmt.Errorf("%w: unit:run-plan on opentofu:acme/net", identity.ErrDenied)
	entries := &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil}
	h := newStartHandler(t, newAuthorizer(readsNamespace()...), newFakeJobs(), starter, entries)

	rec := post(t, h, testToken, goodBody)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d %s, want 403", rec.Code, rec.Body.String())
	}

	if len(entries.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(entries.entries))
	}

	e := entries.entries[0]
	if e.Subject != "ada" || e.Action != string(identity.ActionUnitRunPlan) || e.Object != "opentofu:acme/net" ||
		e.Outcome != audit.OutcomeDenied {
		t.Fatalf("entry = %+v, want the refusal of unit:run-plan for ada", e)
	}
}

// TestStartRefusesAJobOfAnotherNamespace proves the handler never answers a
// job of another namespace, whatever the service gives back.
func TestStartRefusesAJobOfAnotherNamespace(t *testing.T) {
	t.Parallel()

	starter := newFakeStarter(t)
	starter.started.Spec.Unit = unitOf(t, "other", testUnit)
	h := newStartHandler(t, newAuthorizer(readsNamespace()...), newFakeJobs(), starter, nil)

	rec := post(t, h, testToken, goodBody)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), testJob) {
		t.Fatalf("status = %d %s, want 500 with no job", rec.Code, rec.Body.String())
	}
}

// TestGetJobAnswersTheETag proves the read route answers the version of the
// job as its ETag.
func TestGetJobAnswersTheETag(t *testing.T) {
	t.Parallel()

	handler := newHandler(t, newAuthorizer(holdsBoth()...), newFakeJobs(runningJob(t, testJob)))
	rec := serve(t, handler, request{method: http.MethodGet, path: jobPath("acme", testJob), token: testToken})

	if rec.Code != http.StatusOK || rec.Header().Get("ETag") != `"v3"` {
		t.Fatalf("status = %d, ETag = %q; want 200 and \"v3\"", rec.Code, rec.Header().Get("ETag"))
	}
}
