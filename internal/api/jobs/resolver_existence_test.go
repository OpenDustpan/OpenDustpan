package jobs_test

import (
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
)

// answerOf is the whole answer of one request: status, body and headers.
type answerOf struct {
	code   int
	body   string
	header http.Header
}

// answerFor returns the answer in rec without the headers that carry a
// per-request value.
func answerFor(rec *httptest.ResponseRecorder) answerOf {
	h := rec.Header().Clone()
	h.Del("X-Request-ID")
	h.Del("Date")

	return answerOf{code: rec.Code, body: rec.Body.String(), header: h}
}

// checkAnswerOf fails the test when got differs from want in status, body
// or headers.
func checkAnswerOf(t *testing.T, name string, got, want answerOf) {
	t.Helper()

	if got.code != want.code || got.body != want.body || !maps.EqualFunc(got.header, want.header, slices.Equal) {
		t.Errorf("%s = %d %q %v, want %d %q %v",
			name, got.code, got.body, got.header, want.code, want.body, want.header)
	}
}

// TestJobResolverAnswersAlikeForMissingAndForbidden proves that a caller with
// no read action on a job gets one answer (status, body and headers) for a job
// that exists, an unknown job, a job of another namespace, an ID of the wrong
// kind and a malformed ID, on every route that resolves a job. The answer does
// not show which jobs exist.
func TestJobResolverAnswersAlikeForMissingAndForbidden(t *testing.T) {
	t.Parallel()

	foreign := runningJob(t, otherJob)
	foreign.Spec.Unit = unitOf(t, "other", testUnit)

	callers := map[string][]string{
		"no right":                nil,
		"namespace read, no read": {string(identity.ActionNamespaceRead) + " acme"},
	}

	suffixes := []string{"", "/logs/plan"}

	for callerName, holds := range callers {
		for _, suffix := range suffixes {
			t.Run(callerName+suffix, func(t *testing.T) {
				t.Parallel()

				h := newHandler(t, newAuthorizer(holds...), newFakeJobs(runningJob(t, testJob), foreign))

				get := func(ns, id string) answerOf {
					return answerFor(serve(t, h, request{
						method: http.MethodGet, path: jobPath(ns, id) + suffix, token: testToken,
					}))
				}

				want := get("acme", testJob)
				if want.code != http.StatusNotFound {
					t.Fatalf("existing job, denied caller = %d %s, want %d", want.code, want.body, http.StatusNotFound)
				}

				rows := map[string]answerOf{
					"unknown ID":        get("acme", "job-zzzzzzzzzzzzzzzzzzzz"),
					"job of another ns": get("acme", otherJob),
					"unknown namespace": get("absent", testJob),
					"wrong kind":        get("acme", testUnit),
					"malformed ID":      get("acme", "job-%00..%2F"),
					"empty prefix":      get("acme", "job-"),
				}

				for name, got := range rows {
					checkAnswerOf(t, name, got, want)
				}
			})
		}
	}
}

// TestJobResolverNeverAnswersAJobOfAnotherNamespace proves, at the resolver
// alone, that a job whose unit is in another namespace answers 404 under this
// namespace even for a caller who may read it there. The store looks a job up
// by ID alone, so this check is the only one that stops the answer.
func TestJobResolverNeverAnswersAJobOfAnotherNamespace(t *testing.T) {
	t.Parallel()

	foreign := runningJob(t, otherJob)
	foreign.Spec.Unit = unitOf(t, "other", testUnit)

	holds := append(holdsBoth(), string(identity.ActionNamespaceRead)+" other",
		string(identity.ActionJobRead)+" "+foreign.Spec.Unit.String())
	h := newHandler(t, newAuthorizer(holds...), newFakeJobs(foreign))

	rec := serve(t, h, request{method: http.MethodGet, path: jobPath("acme", otherJob), token: testToken})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), http.StatusNotFound)
	}
}
