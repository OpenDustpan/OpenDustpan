package jobs_test

import (
	"context"
	"fmt"
	"maps"
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

// approveCall is one call of the fake service.
type approveCall struct {
	actor   job.Actor
	id      job.ID
	verdict job.Verdict
	plan    job.PlanBinding
	comment string
}

// fakeApprover is a job.Starter that records each Approve and answers with its
// job and its error.
type fakeApprover struct {
	job.Starter

	mu    sync.Mutex
	calls []approveCall
	job   job.Job
	err   error
}

//nolint:revive // argument-limit: the signature is the one of job.Service.
func (f *fakeApprover) Approve(
	_ context.Context, a job.Actor, id job.ID, v job.Verdict, plan job.PlanBinding, comment string,
) (job.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, approveCall{actor: a, id: id, verdict: v, plan: plan, comment: comment})

	if f.err != nil {
		return job.Job{}, f.err
	}

	return f.job, nil
}

func (f *fakeApprover) called() []approveCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.calls)
}

func newFakeApprover(t *testing.T) *fakeApprover {
	t.Helper()

	return &fakeApprover{Starter: nil, mu: sync.Mutex{}, calls: nil, job: runningJob(t, testJob), err: nil}
}

// holdsApprove are the checks of a caller that can read the job and approve on
// its unit.
func holdsApprove() []string {
	return append(holdsBoth(), string(identity.ActionJobApprove)+" opentofu:acme/net")
}

const goodApprove = `{"verdict":"approve","planBinding":{"plan":"pln-1","displaySha256":"d1",` +
	`"applySha256":"a1","stateVersion":"sv-1"},"comment":"checked the plan"}`

func approvalsURL(id string) string { return jobPath("acme", id) + "/approvals" }

// approveRig is a handler with a fake service and an audit store.
type approveRig struct {
	h       testHandler
	svc     *fakeApprover
	entries *recordingAudit
	authz   *recordingAuthorizer
}

func newApproveRig(t *testing.T, holds ...string) *approveRig {
	t.Helper()

	svc := newFakeApprover(t)
	entries := &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil}
	authz := newAuthorizer(holds...)

	return &approveRig{
		h: newStartHandler(t, authz, newFakeJobs(runningJob(t, testJob)), svc, entries), svc: svc, entries: entries,
		authz: authz,
	}
}

// send posts body to the approvals route of the job id.
func (r *approveRig) send(t *testing.T, id, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, approvalsURL(id), strings.NewReader(body))
	req.Header.Set("Authorization", "token "+testToken)

	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}

	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)

	return rec
}

func TestApproveRouteIsDeclared(t *testing.T) {
	t.Parallel()

	h := newApproveRig(t).h

	checkApproveRoute(t, h)
	checkApproveOperation(t, h.Operations())

	// A server without a job service serves no approvals route.
	for _, r := range newHandler(t, newAuthorizer(), newFakeJobs()).Routes() {
		if strings.HasSuffix(r.Path, "/approvals") {
			t.Fatalf("a handler with no job service serves %s %s", r.Method, r.Path)
		}
	}
}

// checkApproveRoute checks the row of the route table.
func checkApproveRoute(t *testing.T, h testHandler) {
	t.Helper()

	var found *jobsapi.Route

	routes := h.Routes()
	for i := range routes {
		if routes[i].Method == http.MethodPost && strings.HasSuffix(routes[i].Path, "/approvals") {
			found = &routes[i]
		}
	}

	if found == nil {
		t.Fatal("a handler with a job service serves no approvals route")
	}

	wantReads := []identity.Action{identity.ActionJobRead}
	wantActions := []identity.Action{identity.ActionJobApprove}

	if found.Scope != jobsapi.ScopeAuthorized || !slices.Equal(found.Reads, wantReads) ||
		!slices.Equal(found.Actions, wantActions) || found.IdempotencyKey || !found.ETag ||
		found.Resolve == nil || found.Serve == nil {
		t.Fatalf("route = %+v, want an authorized POST that needs job:read and job:approve", found)
	}
}

// checkApproveOperation checks the description of the route.
func checkApproveOperation(t *testing.T, ops []apidesc.Operation) {
	t.Helper()

	for i := range ops {
		op := &ops[i]
		if op.ID != "approveJob" {
			continue
		}

		wantObjects := map[string]string{
			"namespace:read": apidesc.KindNamespace, "job:read": apidesc.KindUnit, "job:approve": apidesc.KindUnit,
		}

		if !slices.Equal(op.Actions, []string{"namespace:read", "job:read", "job:approve"}) ||
			!maps.Equal(op.ActionObjects, wantObjects) || op.IdempotencyKey || !op.ETag {
			t.Fatalf("operation = %+v, want the three actions on their objects and the ETag", op)
		}

		return
	}

	t.Fatalf("operations = %+v, want approveJob", ops)
}

func TestApproveJob(t *testing.T) {
	t.Parallel()

	r := newApproveRig(t, holdsApprove()...)

	rec := r.send(t, testJob, goodApprove)
	if rec.Code != http.StatusCreated || rec.Header().Get("ETag") != `"v3"` ||
		!strings.Contains(rec.Body.String(), testJob) {
		t.Fatalf("status = %d, ETag = %q, body = %s; want 201, \"v3\" and the job", rec.Code,
			rec.Header().Get("ETag"), rec.Body.String())
	}

	calls := r.svc.called()
	if len(calls) != 1 {
		t.Fatalf("service calls = %d, want 1", len(calls))
	}

	checkApproveCall(t, &calls[0])

	if len(r.entries.entries) != 0 {
		t.Errorf("audit entries = %d, want none for an allowed request (the service writes its own)",
			len(r.entries.entries))
	}
}

// checkApproveCall checks what the handler gave the service for goodApprove.
func checkApproveCall(t *testing.T, c *approveCall) {
	t.Helper()

	want := job.PlanBinding{Plan: "pln-1", DisplaySHA256: "d1", ApplySHA256: "a1", StateVersion: "sv-1"}

	if c.id != testJob || c.verdict != job.VerdictApprove || c.plan != want || c.comment != "checked the plan" ||
		c.actor.Principal.Subject != "ada" {
		t.Errorf("call = %+v, want ada's approval of %s with the binding and the comment", *c, testJob)
	}

	// No client gives the agent or the authentication: the server sets them.
	if c.actor.Agent != "" || c.actor.Mode != "" || c.actor.ConfirmAuth != "" || !c.actor.AuthAt.IsZero() {
		t.Errorf("actor = %+v, want no agent and no authentication", c.actor)
	}
}

func TestApproveRejectionNeedsNoBinding(t *testing.T) {
	t.Parallel()

	r := newApproveRig(t, holdsApprove()...)

	rec := r.send(t, testJob, `{"verdict":"reject","comment":"wrong region"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d %s, want 201", rec.Code, rec.Body.String())
	}

	var none job.PlanBinding

	calls := r.svc.called()
	if len(calls) != 1 || calls[0].verdict != job.VerdictReject || calls[0].plan != none {
		t.Fatalf("calls = %+v, want one rejection with an empty binding", calls)
	}
}

// TestApproveRefusesHostileBodies sends hostile input to the boundary alone:
// the service is a fake that would accept everything, so a refusal here is the
// work of the handler.
func TestApproveRefusesHostileBodies(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a", audit.MaxReasonLen+1)

	tests := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"empty body", ``, http.StatusBadRequest, "request.body-invalid"},
		{"not JSON", `approve`, http.StatusBadRequest, "request.body-invalid"},
		{"an array", `[]`, http.StatusBadRequest, "request.body-invalid"},
		{"unknown field", `{"verdict":"approve","actor":"root"}`, http.StatusBadRequest, "request.body-invalid"},
		{"agent in the body", `{"verdict":"reject","agent":"dusty"}`, http.StatusBadRequest, "request.body-invalid"},
		{"MFA in the body", `{"verdict":"approve","confirmAuth":"mfa"}`, http.StatusBadRequest, "request.body-invalid"},
		{"second document", `{"verdict":"reject"}{"verdict":"approve"}`, http.StatusBadRequest, "request.body-invalid"},
		{"verdict of the wrong type", `{"verdict":1}`, http.StatusBadRequest, "request.body-invalid"},
		{"lone surrogate", `{"verdict":"reject","comment":"\ud800"}`, http.StatusBadRequest, "request.body-invalid"},
		{
			"too large", `{"verdict":"reject","comment":"` + strings.Repeat("a", 17<<10) + `"}`,
			http.StatusRequestEntityTooLarge, "request.body-too-large",
		},
		{"no verdict", `{}`, http.StatusUnprocessableEntity, "job.invalid"},
		{"verdict with a command", `{"verdict":"approve; drop"}`, http.StatusUnprocessableEntity, "job.invalid"},
		{"verdict in other case", `{"verdict":"Approve"}`, http.StatusUnprocessableEntity, "job.invalid"},
		{
			"comment with a line break", `{"verdict":"reject","comment":"ok\r\nSigned-off-by: root"}`,
			http.StatusUnprocessableEntity, "job.invalid",
		},
		{
			"comment with an override", `{"verdict":"reject","comment":"ok` + string(rune(0x202e)) + `"}`,
			http.StatusUnprocessableEntity, "job.invalid",
		},
		{
			"comment over the limit", `{"verdict":"reject","comment":"` + long + `"}`,
			http.StatusUnprocessableEntity, "job.invalid",
		},
		{
			"binding text over the limit",
			`{"verdict":"approve","planBinding":{"plan":"` + strings.Repeat("p", 129) + `"}}`,
			http.StatusUnprocessableEntity,
			"job.invalid",
		},
		{
			"binding text with a control character", `{"verdict":"approve","planBinding":{"plan":"pln\u0000-1"}}`,
			http.StatusUnprocessableEntity, "job.invalid",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newApproveRig(t, holdsApprove()...)

			rec := r.send(t, testJob, tc.body)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"errorCode":"`+tc.code+`"`) {
				t.Fatalf("status = %d %s, want %d with %s", rec.Code, rec.Body.String(), tc.status, tc.code)
			}

			if n := len(r.svc.called()); n != 0 {
				t.Errorf("service calls = %d, want none for a body that the boundary refuses", n)
			}

			if n := len(r.entries.entries); n != 0 {
				t.Errorf("audit entries = %d, want none for a request that is not valid", n)
			}
		})
	}
}

func TestApproveRefusesAnIdempotencyKey(t *testing.T) {
	t.Parallel()

	r := newApproveRig(t, holdsApprove()...)

	rec := r.send(t, testJob, goodApprove, "Idempotency-Key", "abc")
	if rec.Code != http.StatusBadRequest || len(r.svc.called()) != 0 {
		t.Fatalf("status = %d %s with %d calls, want 400 and none", rec.Code, rec.Body.String(), len(r.svc.called()))
	}
}

// TestApproveRouteChecksTheActions proves the wrapper alone: a caller without
// job:read learns nothing (404), and a caller that reads the job but cannot
// approve gets 403 with one audit entry. The service is never called.
func TestApproveRouteChecksTheActions(t *testing.T) {
	t.Parallel()

	t.Run("no job:read", func(t *testing.T) {
		t.Parallel()

		r := newApproveRig(t, readsNamespace()...)

		rec := r.send(t, testJob, goodApprove)
		if rec.Code != http.StatusNotFound || len(r.svc.called()) != 0 {
			t.Fatalf("status = %d with %d calls, want 404 and none", rec.Code, len(r.svc.called()))
		}
	})

	t.Run("no job:approve", func(t *testing.T) {
		t.Parallel()

		r := newApproveRig(t, holdsBoth()...)

		rec := r.send(t, testJob, goodApprove)
		if rec.Code != http.StatusForbidden || len(r.svc.called()) != 0 {
			t.Fatalf("status = %d with %d calls, want 403 and none", rec.Code, len(r.svc.called()))
		}

		if len(r.entries.entries) != 1 || r.entries.entries[0].Action != string(identity.ActionJobApprove) ||
			r.entries.entries[0].Outcome != audit.OutcomeDenied {
			t.Fatalf("audit entries = %+v, want one denial of job:approve", r.entries.entries)
		}
	})

	t.Run("unknown job", func(t *testing.T) {
		t.Parallel()

		r := newApproveRig(t, holdsApprove()...)

		rec := r.send(t, otherJob, goodApprove)
		if rec.Code != http.StatusNotFound || len(r.svc.called()) != 0 {
			t.Fatalf("status = %d with %d calls, want 404 and none", rec.Code, len(r.svc.called()))
		}
	})
}

func TestApproveMapsTheErrorsOfTheService(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("%w: no", job.ErrAgentDenied), http.StatusForbidden, "job.agent-denied"},
		{fmt.Errorf("%w: ask another approver", job.ErrSelfApproval), http.StatusForbidden, "job.self-approval"},
		{fmt.Errorf("%w: MFA", job.ErrAuthStale), http.StatusForbidden, "job.auth-stale"},
		{fmt.Errorf("%w: wait", job.ErrAlreadyApproved), http.StatusConflict, "job.already-approved"},
		{fmt.Errorf("%w: unit", identity.ErrDenied), http.StatusForbidden, ""},
		{fmt.Errorf("%w: queued", job.ErrTransition), http.StatusConflict, "job.not-awaiting-approval"},
		{fmt.Errorf("%w: succeeded", job.ErrFinished), http.StatusConflict, "job.finished"},
		{fmt.Errorf("%w: other plan", job.ErrPlanChanged), http.StatusConflict, "job.plan-changed"},
		{fmt.Errorf("%w: old", job.ErrPlanStale), http.StatusConflict, "job.plan-stale"},
		{job.ErrConflict, http.StatusConflict, "job.conflict"},
		{namespace.ErrFenced, http.StatusConflict, "namespace.fenced"},
		{fmt.Errorf("%w: verdict", job.ErrInvalid), http.StatusBadRequest, "job.invalid"},
		{job.ErrNotFound, http.StatusNotFound, "job.not-found"},
		{errDriver, http.StatusInternalServerError, ""},
	}

	for _, tc := range tests {
		t.Run(tc.err.Error(), func(t *testing.T) {
			t.Parallel()

			r := newApproveRig(t, holdsApprove()...)
			r.svc.err = tc.err

			rec := r.send(t, testJob, goodApprove)
			if rec.Code != tc.status {
				t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), tc.status)
			}

			if tc.code != "" && !strings.Contains(rec.Body.String(), `"errorCode":"`+tc.code+`"`) {
				t.Errorf("body = %s, want the code %s", rec.Body.String(), tc.code)
			}

			for _, leak := range []string{"hunter2", "/var/lib"} {
				if strings.Contains(rec.Body.String(), leak) {
					t.Errorf("body = %s, leaks %q", rec.Body.String(), leak)
				}
			}
		})
	}
}

// TestApproveRefusalWritesOneAuditEntry proves each refusal of security by the
// service writes one entry that names job:approve on the unit of the job, and
// that a refusal of another kind writes none.
func TestApproveRefusalWritesOneAuditEntry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		err    error
		reason string
	}{
		{"denied", fmt.Errorf("%w: unit", identity.ErrDenied), "denied by policy"},
		{"agent", fmt.Errorf("%w: no", job.ErrAgentDenied), "an agent cannot do this"},
		{"starter", fmt.Errorf("%w: other", job.ErrSelfApproval), "the person who started the job cannot approve it"},
		{
			"missing MFA step",
			fmt.Errorf("%w: MFA", job.ErrAuthStale),
			"the approval of a destroy needs a fresh MFA step",
		},
		{"other plan", fmt.Errorf("%w: plan", job.ErrPlanChanged), "the plan of the approval is not the stored plan"},
		{"stale plan", fmt.Errorf("%w: old", job.ErrPlanStale), "the stored plan is too old or was redacted"},
		{"repeat approval", fmt.Errorf("%w: wait", job.ErrAlreadyApproved), "the approver approved this phase already"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newApproveRig(t, holdsApprove()...)
			r.svc.err = tc.err

			r.send(t, testJob, goodApprove)

			if len(r.entries.entries) != 1 {
				t.Fatalf("audit entries = %d, want 1", len(r.entries.entries))
			}

			e := r.entries.entries[0]
			if e.Subject != "ada" || e.Action != string(identity.ActionJobApprove) || e.Object != "opentofu:acme/net" ||
				e.Outcome != audit.OutcomeDenied || !strings.HasPrefix(e.Reason, tc.reason) {
				t.Fatalf("entry = %+v, want a refusal for ada of job:approve on the unit with %q", e, tc.reason)
			}
		})
	}

	for _, err := range []error{
		job.ErrTransition, job.ErrFinished, job.ErrConflict, job.ErrNotFound, fmt.Errorf("%w: x", job.ErrInvalid),
		errDriver,
	} {
		r := newApproveRig(t, holdsApprove()...)
		r.svc.err = err

		r.send(t, testJob, goodApprove)

		if len(r.entries.entries) != 0 {
			t.Errorf("error %v wrote %d audit entries, want none", err, len(r.entries.entries))
		}
	}
}

// TestApproveAuditsTheDenialsOfTheGroupCheck proves that the denials of the
// group check of the service, in the form that the service gives them, answer
// 403 and write one entry of job:approve on the unit of the route. The route
// names the object, whatever object the error names.
func TestApproveAuditsTheDenialsOfTheGroupCheck(t *testing.T) {
	t.Parallel()

	unit := unitOf(t, "acme", testUnit)
	other := unitOf(t, "other", testUnit)

	denial := func(object identity.Object, reason string, cause error) error {
		err := fmt.Errorf("%w: %s", identity.ErrDenied, reason)
		if cause != nil {
			err = fmt.Errorf("%w: %s: %w", identity.ErrDenied, reason, cause)
		}

		return &job.DeniedError{Action: identity.ActionJobApprove, Object: object, Err: err}
	}

	tests := []struct {
		name string
		err  error
	}{
		{"a non-member with job:approve", denial(unit, "the subject is not a member of a group of the gate", nil)},
		{"a member of another group", denial(unit, "the subject is not a member of a group of the gate", nil)},
		{"a failure of the membership read", denial(unit, "the group membership cannot be read", errDriver)},
		{"no membership reader", denial(unit, "this server cannot read group membership", nil)},
		{"a denial that names an object of another namespace", denial(other, "not a member", nil)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newApproveRig(t, holdsApprove()...)
			r.svc.err = tc.err

			rec := r.send(t, testJob, goodApprove)
			if rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), "hunter2") {
				t.Fatalf("status = %d %s, want 403 with no text of the failure", rec.Code, rec.Body.String())
			}

			if len(r.entries.entries) != 1 {
				t.Fatalf("audit entries = %d, want 1", len(r.entries.entries))
			}

			e := r.entries.entries[0]
			if e.Subject != "ada" || e.Action != string(identity.ActionJobApprove) || e.Object != "opentofu:acme/net" ||
				e.Outcome != audit.OutcomeDenied {
				t.Fatalf("entry = %+v, want a refusal for ada of job:approve on the unit of the route", e)
			}
		})
	}
}

func TestApproveRefusesAJobOfAnotherNamespace(t *testing.T) {
	t.Parallel()

	r := newApproveRig(t, holdsApprove()...)
	r.svc.job.Spec.Unit = unitOf(t, "other", testUnit)

	rec := r.send(t, testJob, goodApprove)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "other") {
		t.Fatalf("status = %d %s, want 500 with no job", rec.Code, rec.Body.String())
	}
}
