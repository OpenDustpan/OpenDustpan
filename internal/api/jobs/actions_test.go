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
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// cancelCall is one call of the fake service.
type cancelCall struct {
	actor  job.Actor
	id     job.ID
	reason string
}

// fakeCanceler is a job.Starter that records each Cancel and answers with its
// job and its error.
type fakeCanceler struct {
	job.Starter

	mu    sync.Mutex
	calls []cancelCall
	job   job.Job
	err   error
}

func (f *fakeCanceler) Cancel(_ context.Context, a job.Actor, id job.ID, reason string) (job.Job, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, cancelCall{actor: a, id: id, reason: reason})

	if f.err != nil {
		return job.Job{}, f.err
	}

	return f.job, nil
}

func (f *fakeCanceler) called() []cancelCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.calls)
}

// cancelRig is a handler with a fake service and an audit store.
type cancelRig struct {
	h       testHandler
	svc     *fakeCanceler
	entries *recordingAudit
}

func newCancelRig(t *testing.T, holds ...string) *cancelRig {
	t.Helper()

	svc := &fakeCanceler{Starter: nil, mu: sync.Mutex{}, calls: nil, job: runningJob(t, testJob), err: nil}
	entries := &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil}

	return &cancelRig{
		h:   newStartHandler(t, newAuthorizer(holds...), newFakeJobs(runningJob(t, testJob)), svc, entries),
		svc: svc, entries: entries,
	}
}

// holdsCancel are the checks of a caller that can read the job and cancel it.
func holdsCancel() []string {
	return append(holdsBoth(), string(identity.ActionJobCancel)+" opentofu:acme/net")
}

func cancelURL(id string) string { return jobPath("acme", id) + "/cancel" }

// send posts body to the cancel route of the job id.
func (r *cancelRig) send(t *testing.T, id, body string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, cancelURL(id), strings.NewReader(body))
	req.Header.Set("Authorization", "token "+testToken)

	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Add(headers[i], headers[i+1])
	}

	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)

	return rec
}

func TestJobActionRoutesAreDeclared(t *testing.T) {
	t.Parallel()

	h := newCancelRig(t).h

	checkCancelRoute(t, h.Routes())
	checkCancelOperation(t, h.Operations())

	// A server without a job service serves no cancel route.
	for _, r := range newHandler(t, newAuthorizer(), newFakeJobs()).Routes() {
		if strings.HasSuffix(r.Path, "/cancel") {
			t.Fatalf("a handler with no job service serves %s %s", r.Method, r.Path)
		}
	}
}

func checkCancelRoute(t *testing.T, routes []jobsapi.Route) {
	t.Helper()

	var found *jobsapi.Route

	for i := range routes {
		if routes[i].Method == http.MethodPost && strings.HasSuffix(routes[i].Path, "/cancel") {
			found = &routes[i]
		}
	}

	if found == nil {
		t.Fatal("a handler with a job service serves no cancel route")
	}

	okScope := found.Scope == jobsapi.ScopeAuthorized
	okReads := slices.Equal(found.Reads, []identity.Action{identity.ActionJobRead})
	okActions := slices.Equal(found.Actions, []identity.Action{identity.ActionJobCancel})
	okFlags := !found.IdempotencyKey && found.ETag && found.Resolve != nil && found.Serve != nil

	if !okScope || !okReads || !okActions || !okFlags {
		t.Fatalf("route = %+v, want an authorized POST that needs job:read and job:cancel", found)
	}
}

func checkCancelOperation(t *testing.T, ops []apidesc.Operation) {
	t.Helper()

	for i := range ops {
		op := &ops[i]
		if op.ID != "cancelJob" {
			continue
		}

		wantObjects := map[string]string{
			"namespace:read": apidesc.KindNamespace, "job:read": apidesc.KindUnit, "job:cancel": apidesc.KindUnit,
		}

		if !slices.Equal(op.Actions, []string{"namespace:read", "job:read", "job:cancel"}) ||
			!maps.Equal(op.ActionObjects, wantObjects) || op.IdempotencyKey || !op.ETag {
			t.Fatalf("operation = %+v, want the three actions on their objects and the ETag", op)
		}

		return
	}

	t.Fatalf("operations = %+v, want cancelJob", ops)
}

func TestJobActionRoutesCancel(t *testing.T) {
	t.Parallel()

	r := newCancelRig(t, holdsCancel()...)

	rec := r.send(t, testJob, `{"reason":"wrong unit"}`)
	hasJob := strings.Contains(rec.Body.String(), testJob)
	if rec.Code != http.StatusOK || rec.Header().Get("ETag") != `"v3"` || !hasJob {
		t.Fatalf("status = %d, ETag = %q, body = %s; want 200, \"v3\" and the job", rec.Code,
			rec.Header().Get("ETag"), rec.Body.String())
	}

	calls := r.svc.called()
	if len(calls) != 1 || calls[0].id != testJob || calls[0].reason != "wrong unit" ||
		calls[0].actor.Principal.Subject != "ada" || calls[0].actor.Agent != "" {
		t.Fatalf("calls = %+v, want ada's cancel of %s with the reason and no agent", calls, testJob)
	}

	if len(r.entries.entries) != 0 {
		t.Errorf("audit entries = %d, want none for an allowed request (the service writes its own)",
			len(r.entries.entries))
	}

	// The reason is optional.
	rec = r.send(t, testJob, `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s, want 200 for a cancel with no reason", rec.Code, rec.Body.String())
	}
}

// TestJobActionRoutesCancelRefusesHostileBodies sends hostile input to the
// boundary alone: the service is a fake that would accept everything.
func TestJobActionRoutesCancelRefusesHostileBodies(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{"empty body", ``, http.StatusBadRequest, "request.body-invalid"},
		{"not JSON", `cancel`, http.StatusBadRequest, "request.body-invalid"},
		{"an array", `[]`, http.StatusBadRequest, "request.body-invalid"},
		{"unknown field", `{"reason":"x","agent":"dusty"}`, http.StatusBadRequest, "request.body-invalid"},
		{"second document", `{}{}`, http.StatusBadRequest, "request.body-invalid"},
		{"reason of the wrong type", `{"reason":1}`, http.StatusBadRequest, "request.body-invalid"},
		{
			"too large", `{"reason":"` + strings.Repeat("a", 17<<10) + `"}`,
			http.StatusRequestEntityTooLarge, "request.body-too-large",
		},
		{
			"reason with a line break", `{"reason":"ok\r\nSigned-off-by: root"}`, http.StatusUnprocessableEntity,
			"job.invalid",
		},
		{
			"reason with an override", `{"reason":"ok` + string(rune(0x202e)) + `"}`,
			http.StatusUnprocessableEntity, "job.invalid",
		},
		{
			"reason over the limit", `{"reason":"` + strings.Repeat("a", audit.MaxReasonLen+1) + `"}`,
			http.StatusUnprocessableEntity, "job.invalid",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newCancelRig(t, holdsCancel()...)

			rec := r.send(t, testJob, tc.body)
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"errorCode":"`+tc.code+`"`) {
				t.Fatalf("status = %d %s, want %d with %s", rec.Code, rec.Body.String(), tc.status, tc.code)
			}

			if n := len(r.svc.called()); n != 0 {
				t.Errorf("service calls = %d, want none for a body that the boundary refuses", n)
			}
		})
	}
}

func TestJobActionRoutesCancelRefusesAnIdempotencyKey(t *testing.T) {
	t.Parallel()

	r := newCancelRig(t, holdsCancel()...)

	rec := r.send(t, testJob, `{}`, "Idempotency-Key", "abc")
	if rec.Code != http.StatusBadRequest || len(r.svc.called()) != 0 {
		t.Fatalf("status = %d %s with %d calls, want 400 and none", rec.Code, rec.Body.String(), len(r.svc.called()))
	}
}

// TestJobActionRoutesCancelChecksTheActions proves the wrapper alone: a caller
// without job:read learns nothing (404), and a caller that reads the job but
// cannot cancel gets 403 with one audit entry. The service is never called.
func TestJobActionRoutesCancelChecksTheActions(t *testing.T) {
	t.Parallel()

	t.Run("no job:read", func(t *testing.T) {
		t.Parallel()

		r := newCancelRig(t, readsNamespace()...)

		rec := r.send(t, testJob, `{}`)
		if rec.Code != http.StatusNotFound || len(r.svc.called()) != 0 {
			t.Fatalf("status = %d with %d calls, want 404 and none", rec.Code, len(r.svc.called()))
		}
	})

	t.Run("no job:cancel", func(t *testing.T) {
		t.Parallel()

		r := newCancelRig(t, holdsBoth()...)

		rec := r.send(t, testJob, `{}`)
		if rec.Code != http.StatusForbidden || len(r.svc.called()) != 0 {
			t.Fatalf("status = %d with %d calls, want 403 and none", rec.Code, len(r.svc.called()))
		}

		if len(r.entries.entries) != 1 || r.entries.entries[0].Action != string(identity.ActionJobCancel) ||
			r.entries.entries[0].Outcome != audit.OutcomeDenied {
			t.Fatalf("audit entries = %+v, want one denial of job:cancel", r.entries.entries)
		}
	})

	t.Run("unknown job", func(t *testing.T) {
		t.Parallel()

		r := newCancelRig(t, holdsCancel()...)

		rec := r.send(t, otherJob, `{}`)
		if rec.Code != http.StatusNotFound || len(r.svc.called()) != 0 {
			t.Fatalf("status = %d with %d calls, want 404 and none", rec.Code, len(r.svc.called()))
		}
	})
}

func TestJobActionRoutesCancelMapsTheErrorsOfTheService(t *testing.T) {
	t.Parallel()

	tests := []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("%w: succeeded", job.ErrFinished), http.StatusConflict, "job.wrong-state"},
		{fmt.Errorf("%w: in git", job.ErrDelivered), http.StatusConflict, "job.wrong-state"},
		{job.ErrConflict, http.StatusConflict, "job.conflict"},
		{namespace.ErrFenced, http.StatusConflict, "namespace.fenced"},
		{fmt.Errorf("%w: unit", identity.ErrDenied), http.StatusForbidden, errcode.AccessDenied},
		{fmt.Errorf("%w: reason", job.ErrInvalid), http.StatusBadRequest, "job.invalid"},
		{job.ErrNotFound, http.StatusNotFound, "job.not-found"},
		{errDriver, http.StatusInternalServerError, errcode.ServerInternal},
	}

	for _, tc := range tests {
		t.Run(tc.err.Error(), func(t *testing.T) {
			t.Parallel()

			r := newCancelRig(t, holdsCancel()...)
			r.svc.err = tc.err

			rec := r.send(t, testJob, `{}`)
			if rec.Code != tc.status {
				t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), tc.status)
			}

			errcodetest.CheckAnswer(t, errcodetest.ShapeCamelCase, rec.Code, rec.Body.Bytes(),
				errcodetest.Want{Code: tc.code, Args: nil, RequestBody: nil})
		})
	}
}

// TestJobActionRoutesCancelAuditsTheDenialOfTheService proves that a denial by
// the service writes one entry of job:cancel on the unit of the route, and that
// a refusal of another kind writes none.
func TestJobActionRoutesCancelAuditsTheDenialOfTheService(t *testing.T) {
	t.Parallel()

	r := newCancelRig(t, holdsCancel()...)
	r.svc.err = fmt.Errorf("%w: unit", identity.ErrDenied)

	r.send(t, testJob, `{}`)

	if len(r.entries.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(r.entries.entries))
	}

	e := r.entries.entries[0]
	if e.Subject != "ada" || e.Action != string(identity.ActionJobCancel) || e.Object != "opentofu:acme/net" ||
		e.Outcome != audit.OutcomeDenied {
		t.Fatalf("entry = %+v, want a refusal for ada of job:cancel on the unit", e)
	}

	for _, err := range []error{job.ErrFinished, job.ErrDelivered, job.ErrConflict, job.ErrNotFound} {
		other := newCancelRig(t, holdsCancel()...)
		other.svc.err = err

		other.send(t, testJob, `{}`)

		if len(other.entries.entries) != 0 {
			t.Errorf("error %v wrote %d audit entries, want none", err, len(other.entries.entries))
		}
	}
}
