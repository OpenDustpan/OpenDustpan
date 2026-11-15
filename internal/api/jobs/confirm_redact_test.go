package jobs_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	jobsapi "opendustpan.org/opendustpan/internal/api/jobs"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/history"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/secret"
)

// fakeActions is a job.Starter that records each Confirm and RedactLog and
// answers with its job, its log and its error.
type fakeActions struct {
	job.Starter

	mu       sync.Mutex
	confirms []confirmCall
	redacts  []redactCall
	job      job.Job
	log      job.LogRef
	err      error
}

type confirmCall struct {
	actor   job.Actor
	id      job.ID
	verdict job.Verdict
	plan    job.PlanBinding
}

type redactCall struct {
	actor job.Actor
	id    job.ID
	req   job.RedactRequest
}

func (f *fakeActions) Confirm(_ context.Context, a job.Actor, id job.ID, v job.Verdict, p job.PlanBinding,
) (job.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.confirms = append(f.confirms, confirmCall{actor: a, id: id, verdict: v, plan: p})

	if f.err != nil {
		return job.Job{}, f.err
	}

	return f.job, nil
}

func (f *fakeActions) RedactLog(_ context.Context, a job.Actor, id job.ID, req job.RedactRequest) (job.LogRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.redacts = append(f.redacts, redactCall{actor: a, id: id, req: req})

	if f.err != nil {
		return job.LogRef{}, f.err
	}

	return f.log, nil
}

func (f *fakeActions) confirmed() []confirmCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.confirms)
}

func (f *fakeActions) redacted() []redactCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.redacts)
}

type actionsRig struct {
	h       testHandler
	svc     *fakeActions
	entries *recordingAudit
}

func newActionsRig(t *testing.T, holds ...string) *actionsRig {
	t.Helper()

	closed := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	svc := &fakeActions{
		Starter: nil, mu: sync.Mutex{}, confirms: nil, redacts: nil, job: runningJob(t, testJob), err: nil,
		log: job.LogRef{
			ID: "jlv-2", ItemKey: "", Version: 2, Chunks: 1, SHA256: "", Size: 42, CreatedAt: closed, ClosedAt: closed,
			ExpiresAt: time.Time{}, RedactedAt: time.Time{}, ReplacedBy: "ada", Matches: 3,
		},
	}
	entries := &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil}

	return &actionsRig{
		h:   newStartHandler(t, newAuthorizer(holds...), newFakeJobs(runningJob(t, testJob)), svc, entries),
		svc: svc, entries: entries,
	}
}

func holdsRedact() []string {
	return append(holdsBoth(), string(identity.ActionUnitRedact)+" opentofu:acme/net")
}

func confirmURL(id string) string { return jobPath("acme", id) + "/confirmations" }

func redactURL() string { return jobPath("acme", testJob) + "/log-redactions" }

const (
	goodConfirm = `{"verdict":"approve","planBinding":{"plan":"pln-1","displaySha256":"d1","applySha256":"a1",` +
		`"stateVersion":"sv-1"}}`
	goodRedact = `{"version":"jlv-1","substitutions":[{"match":"hunter2"}],"reason":"a token in the log"}`
)

func (r *actionsRig) post(t *testing.T, url, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, url, strings.NewReader(body))
	req.Header.Set("Authorization", "token "+testToken)

	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}

	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)

	return rec
}

// routeWithSuffix returns the POST route whose path ends with suffix, or nil.
func routeWithSuffix(routes []jobsapi.Route, suffix string) *jobsapi.Route {
	for i := range routes {
		if routes[i].Method == http.MethodPost && strings.HasSuffix(routes[i].Path, suffix) {
			return &routes[i]
		}
	}

	return nil
}

func TestJobActionRoutesConfirmIsDeclared(t *testing.T) {
	t.Parallel()

	rt := routeWithSuffix(newActionsRig(t).h.Routes(), "/confirmations")
	if rt == nil {
		t.Fatal("a handler with a job service serves no confirm route")
	}

	okReads := slices.Equal(rt.Reads, []identity.Action{identity.ActionJobRead})
	okWrites := slices.Contains(rt.Writes, identity.ActionUnitRunApply) && len(rt.Actions) == 0

	if rt.Scope != jobsapi.ScopeAuthorized || !okReads || !okWrites || !rt.ETag || rt.IdempotencyKey {
		t.Fatalf("route = %+v, want an authorized POST that needs job:read and declares the run actions", rt)
	}
}

func TestJobActionRoutesRedactIsDeclared(t *testing.T) {
	t.Parallel()

	rt := routeWithSuffix(newActionsRig(t).h.Routes(), "/log-redactions")
	if rt == nil {
		t.Fatal("a handler with a job service serves no redact route")
	}

	okReads := slices.Equal(rt.Reads, []identity.Action{identity.ActionJobRead})
	okActions := slices.Equal(rt.Actions, []identity.Action{identity.ActionUnitRedact})

	if rt.Scope != jobsapi.ScopeAuthorized || !okReads || !okActions || rt.IdempotencyKey {
		t.Fatalf("route = %+v, want an authorized POST that needs job:read and unit:redact", rt)
	}
}

// TestJobActionRoutesServeNoActionWithoutAJobService proves that a server with
// no job service serves neither route.
func TestJobActionRoutesServeNoActionWithoutAJobService(t *testing.T) {
	t.Parallel()

	routes := newHandler(t, newAuthorizer(), newFakeJobs()).Routes()

	for _, suffix := range []string{"/confirmations", "/log-redactions"} {
		if routeWithSuffix(routes, suffix) != nil {
			t.Fatalf("a handler with no job service serves the route %s", suffix)
		}
	}
}

func TestJobActionRoutesConfirm(t *testing.T) {
	t.Parallel()

	r := newActionsRig(t, holdsBoth()...)

	rec := r.post(t, confirmURL(testJob), goodConfirm)
	hasJob := strings.Contains(rec.Body.String(), testJob)
	if rec.Code != http.StatusCreated || rec.Header().Get("ETag") != `"v3"` || !hasJob {
		t.Fatalf("status = %d, ETag = %q, body = %s; want 201, \"v3\" and the job", rec.Code,
			rec.Header().Get("ETag"), rec.Body.String())
	}

	calls := r.svc.confirmed()
	if len(calls) != 1 || calls[0].id != testJob || calls[0].verdict != job.VerdictApprove ||
		calls[0].plan.Plan != "pln-1" || calls[0].actor.Principal.Subject != "ada" || calls[0].actor.Agent != "" ||
		calls[0].actor.ConfirmAuth != "" {
		t.Fatalf("calls = %+v, want ada's approve with the binding, no agent and no fresh step", calls)
	}
}

// bodyCase is one hostile request body and the answer that the handler gives
// before it calls the service.
type bodyCase struct {
	name    string
	body    string
	headers []string
	status  int
	code    string
}

func refusedBody(name, body string, status int, code string, headers ...string) bodyCase {
	return bodyCase{name: name, body: body, headers: headers, status: status, code: code}
}

// checkRefusedBody posts tc to url and checks the answer. It returns false when
// the answer is wrong.
func checkRefusedBody(t *testing.T, r *actionsRig, url string, tc bodyCase, limit int) {
	t.Helper()

	rec := r.post(t, url, tc.body, tc.headers...)
	if rec.Code != tc.status {
		t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), tc.status)
	}

	var args map[string]any
	if tc.status == http.StatusRequestEntityTooLarge {
		args = map[string]any{"maxBytes": float64(limit)}
	}

	errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, rec.Code, rec.Body.Bytes(),
		errcodetest.Want{Code: tc.code, Args: args, RequestBody: nil})
}

// TestJobActionRoutesConfirmRefuses sends hostile input to the handler: the
// service is never called.
func TestJobActionRoutesConfirmRefuses(t *testing.T) {
	t.Parallel()

	const bad = http.StatusBadRequest

	hidden := `{"verdict":"approve","planBinding":{"plan":"p` + string(rune(0x202e)) + `"}}`
	large := `{"verdict":"approve","planBinding":{"plan":"` + strings.Repeat("a", 20<<10) + `"}}`

	tests := []bodyCase{
		refusedBody("unknown field", `{"verdict":"approve","extra":1}`, bad, errcode.RequestBodyInvalid),
		refusedBody("not JSON", `nope`, bad, errcode.RequestBodyInvalid),
		refusedBody("empty body", ``, bad, errcode.RequestBodyInvalid),
		refusedBody("unknown verdict", `{"verdict":"maybe"}`, http.StatusUnprocessableEntity, errcode.JobInvalid),
		refusedBody("empty verdict", `{}`, http.StatusUnprocessableEntity, errcode.JobInvalid),
		refusedBody("hidden character in the binding", hidden, http.StatusUnprocessableEntity, errcode.JobInvalid),
		refusedBody("too large", large, http.StatusRequestEntityTooLarge, errcode.RequestBodyTooLarge),
		refusedBody("idempotency key", goodConfirm, bad, errcode.RequestIdempotencyKeyInvalid, "Idempotency-Key", "k1"),
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newActionsRig(t, holdsBoth()...)

			checkRefusedBody(t, r, confirmURL(testJob), tc, 16<<10)

			if len(r.svc.confirmed()) != 0 {
				t.Error("a refused request called the service")
			}
		})
	}
}

func TestJobActionRoutesConfirmNeedsReadOfTheJob(t *testing.T) {
	t.Parallel()

	r := newActionsRig(t, string(identity.ActionNamespaceRead)+" acme")

	rec := r.post(t, confirmURL(testJob), goodConfirm)
	if rec.Code != http.StatusNotFound || len(r.svc.confirmed()) != 0 {
		t.Fatalf("status = %d with %d calls, want 404 and none", rec.Code, len(r.svc.confirmed()))
	}

	if rec := r.post(t, confirmURL(otherJob), goodConfirm); rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an unknown job", rec.Code)
	}
}

func TestJobActionRoutesConfirmMapsTheErrorsOfTheService(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err    error
		status int
		code   string
		audit  bool
	}{
		{fmt.Errorf("%w: ended", job.ErrFinished), http.StatusConflict, errcode.JobWrongState, false},
		{fmt.Errorf("%w: no gate", job.ErrTransition), http.StatusConflict, errcode.JobWrongState, false},
		{job.ErrPlanChanged, http.StatusConflict, errcode.JobPlanChanged, true},
		{job.ErrPlanStale, http.StatusConflict, errcode.JobPlanStale, true},
		{job.ErrConflict, http.StatusConflict, errcode.JobConflict, false},
		{job.ErrNotRequester, http.StatusForbidden, errcode.AccessDenied, true},
		{job.ErrAgentDenied, http.StatusForbidden, errcode.JobAgentDenied, true},
		{job.ErrAuthStale, http.StatusForbidden, errcode.MFAFreshStepRequired, true},
		{fmt.Errorf("%w: unit", identity.ErrDenied), http.StatusForbidden, errcode.AccessDenied, true},
		{job.ErrNotFound, http.StatusNotFound, errcode.JobNotFound, false},
		{errDriver, http.StatusInternalServerError, errcode.ServerInternal, false},
	}

	for _, tc := range tests {
		t.Run(tc.err.Error(), func(t *testing.T) {
			t.Parallel()

			r := newActionsRig(t, holdsBoth()...)
			r.svc.err = tc.err

			rec := r.post(t, confirmURL(testJob), goodConfirm)
			if rec.Code != tc.status {
				t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), tc.status)
			}

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, rec.Code, rec.Body.Bytes(),
				errcodetest.Want{Code: tc.code, Args: nil, RequestBody: nil})

			if tc.audit {
				checkConfirmDenied(t, r)

				return
			}

			if len(r.entries.entries) != 0 {
				t.Fatalf("audit entries = %d, want none", len(r.entries.entries))
			}
		})
	}
}

// checkConfirmDenied checks that a refusal of security wrote one entry of the
// start action on the unit.
func checkConfirmDenied(t *testing.T, r *actionsRig) {
	t.Helper()

	if len(r.entries.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(r.entries.entries))
	}

	e := r.entries.entries[0]
	if e.Action != string(identity.ActionUnitRunApply) || e.Outcome != audit.OutcomeDenied ||
		e.Object != "opentofu:acme/net" {
		t.Errorf("entry = %+v, want a refusal of unit:run-apply on the unit", e)
	}
}

func TestJobActionRoutesRedact(t *testing.T) {
	t.Parallel()

	r := newActionsRig(t, holdsRedact()...)

	rec := r.post(t, redactURL(), goodRedact)
	body := rec.Body.String()

	if rec.Code != http.StatusOK || !strings.Contains(body, `"id":"jlv-2"`) || !strings.Contains(body, `"matches":3`) ||
		!strings.Contains(body, `"redactedVersion":"jlv-1"`) {
		t.Fatalf("status = %d, body = %s; want 200 with the new version, the old one and 3 matches", rec.Code, body)
	}

	if strings.Contains(body, "hunter2") {
		t.Errorf("the answer repeats the text of a substitution: %s", body)
	}

	checkRedactCall(t, r.svc.redacted())
}

// checkRedactCall checks that the service got ada's redaction of the job.
func checkRedactCall(t *testing.T, calls []redactCall) {
	t.Helper()

	if len(calls) != 1 || calls[0].id != testJob || calls[0].actor.Principal.Subject != "ada" {
		t.Fatalf("calls = %+v, want ada's redaction of the job", calls)
	}

	req := calls[0].req
	if req.Version != "jlv-1" || req.Reason != "a token in the log" || len(req.Substitutions) != 1 ||
		string(req.Substitutions[0].Match) != "hunter2" {
		t.Fatalf("request = %+v, want jlv-1 with one match and the reason", req)
	}
}

// TestJobActionRoutesRedactRefuses sends hostile input to the handler: the
// service is never called.
func TestJobActionRoutesRedactRefuses(t *testing.T) {
	t.Parallel()

	const bad = http.StatusBadRequest

	one := `"substitutions":[{"match":"a"}]`
	manySubs := strings.Repeat(`{"match":"x"},`, job.MaxSubstitutions) + `{"match":"x"}`
	longMatch := strings.Repeat("a", job.MaxSubstitutionBytes+1)
	hiddenReason := `{"version":"jlv-1",` + one + `,"reason":"r` + string(rune(0x202e)) + `"}`

	tests := []bodyCase{
		refusedBody("unknown field", `{"version":"jlv-1","x":1}`, bad, errcode.RequestBodyInvalid),
		refusedBody("not JSON", `nope`, bad, errcode.RequestBodyInvalid),
		refusedBody("empty match", `{"version":"jlv-1","substitutions":[{"match":""}],"reason":"r"}`, bad,
			errcode.JobInvalid),
		refusedBody("no substitution", `{"version":"jlv-1","substitutions":[],"reason":"r"}`, bad, errcode.JobInvalid),
		refusedBody("too many substitutions", `{"version":"jlv-1","substitutions":[`+manySubs+`],"reason":"r"}`, bad,
			errcode.JobInvalid),
		refusedBody("match too long", `{"version":"jlv-1","substitutions":[{"match":"`+longMatch+`"}],"reason":"r"}`,
			bad, errcode.JobInvalid),
		refusedBody("no version", `{`+one+`,"reason":"r"}`, bad, errcode.JobInvalid),
		refusedBody("long version", `{"version":"`+strings.Repeat("v", 65)+`",`+one+`,"reason":"r"}`, bad,
			errcode.JobInvalid),
		refusedBody("no reason", `{"version":"jlv-1",`+one+`,"reason":""}`, bad, errcode.JobInvalid),
		refusedBody("hidden character in the reason", hiddenReason, bad, errcode.JobInvalid),
		refusedBody("too large", `{"version":"jlv-1","reason":"`+strings.Repeat("a", 2<<20)+`"}`,
			http.StatusRequestEntityTooLarge, errcode.RequestBodyTooLarge),
		refusedBody("idempotency key", goodRedact, bad, errcode.RequestIdempotencyKeyInvalid, "Idempotency-Key", "k1"),
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newActionsRig(t, holdsRedact()...)

			checkRefusedBody(t, r, redactURL(), tc, 1<<20)

			if len(r.svc.redacted()) != 0 {
				t.Error("a refused request called the service")
			}
		})
	}
}

func TestJobActionRoutesRedactChecksTheActions(t *testing.T) {
	t.Parallel()

	// A caller that reads the job but cannot redact gets 403 and no call.
	r := newActionsRig(t, holdsBoth()...)

	rec := r.post(t, redactURL(), goodRedact)
	if rec.Code != http.StatusForbidden || len(r.svc.redacted()) != 0 {
		t.Fatalf("status = %d with %d calls, want 403 and none", rec.Code, len(r.svc.redacted()))
	}

	// A caller that cannot read the job gets the 404 of an unknown job.
	r = newActionsRig(t, string(identity.ActionNamespaceRead)+" acme",
		string(identity.ActionUnitRedact)+" opentofu:acme/net")

	rec = r.post(t, redactURL(), goodRedact)
	if rec.Code != http.StatusNotFound || len(r.svc.redacted()) != 0 {
		t.Fatalf("status = %d with %d calls, want 404 and none", rec.Code, len(r.svc.redacted()))
	}
}

func TestJobActionRoutesRedactMapsTheErrorsOfTheService(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("%w: open", history.ErrInProgress), http.StatusConflict, errcode.JobWrongState},
		{fmt.Errorf("%w: twice", history.ErrRedacted), http.StatusConflict, errcode.JobWrongState},
		{fmt.Errorf("%w: current", history.ErrLatest), http.StatusConflict, errcode.JobWrongState},
		{fmt.Errorf("%w: shredded", secret.ErrShredded), http.StatusConflict, errcode.JobWrongState},
		{job.ErrConflict, http.StatusConflict, errcode.JobConflict},
		{fmt.Errorf("%w: no store", job.ErrUnsupported), http.StatusUnprocessableEntity, errcode.JobUnsupported},
		{fmt.Errorf("%w: no match", job.ErrInvalid), http.StatusBadRequest, errcode.JobInvalid},
		{fmt.Errorf("%w: unit", identity.ErrDenied), http.StatusForbidden, errcode.AccessDenied},
		{job.ErrNotFound, http.StatusNotFound, errcode.JobNotFound},
		{errDriver, http.StatusInternalServerError, errcode.ServerInternal},
	}

	for _, tc := range tests {
		t.Run(tc.err.Error(), func(t *testing.T) {
			t.Parallel()

			r := newActionsRig(t, holdsRedact()...)
			r.svc.err = tc.err

			rec := r.post(t, redactURL(), goodRedact)
			if rec.Code != tc.status {
				t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), tc.status)
			}

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, rec.Code, rec.Body.Bytes(),
				errcodetest.Want{Code: tc.code, Args: nil, RequestBody: nil})

			// Only a denial writes an entry.
			want := tc.status == http.StatusForbidden
			if got := len(r.entries.entries); got != map[bool]int{true: 1, false: 0}[want] {
				t.Fatalf("audit entries = %d, want one: %t", got, want)
			}
		})
	}
}

// TestJobActionRoutesConfirmRefusesTheJobOfAnotherNamespace sends the answer of
// a faulty service to the handler: a job of another ID, and a job of another
// namespace. The handler must not answer with either job.
func TestJobActionRoutesConfirmRefusesTheJobOfAnotherNamespace(t *testing.T) {
	t.Parallel()

	other := unitOf(t, "other", testUnit)

	tests := []struct {
		name string
		mod  func(*job.Job)
	}{
		{"a job with another ID", func(j *job.Job) { j.ID = "job-other" }},
		{"a job of another namespace", func(j *job.Job) { j.Spec.Unit = other }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newActionsRig(t, holdsBoth()...)
			tc.mod(&r.svc.job)

			rec := r.post(t, confirmURL(testJob), goodConfirm)
			if rec.Code < http.StatusInternalServerError || rec.Header().Get("ETag") != "" ||
				strings.Contains(rec.Body.String(), "job-other") {
				t.Fatalf("status = %d, ETag = %q, body = %s; want a refusal that names no job", rec.Code,
					rec.Header().Get("ETag"), rec.Body.String())
			}
		})
	}
}
