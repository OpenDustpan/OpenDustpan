package jobs_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/job"
)

// allowAll is an authorizer that allows every check. A test that uses it proves
// that the refusal of a job comes from the resolver of the route and not from
// the authorizer: with this authorizer, a job that the resolver served would
// answer a success or a failure that is not the 404 of an unknown job.
type allowAll struct{}

func (allowAll) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return nil
}

func (allowAll) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{}, errNoListing
}

// jobRoute is one route that resolves a job with the resolver of the job
// routes. The log route is not in the table: its answer depends on the
// artifact store.
type jobRoute struct {
	name   string
	method string
	suffix string
}

func jobRoutes() []jobRoute {
	return []jobRoute{
		{name: "get", method: http.MethodGet, suffix: ""},
		{name: "approvals", method: http.MethodPost, suffix: "/approvals"},
		{name: "cancel", method: http.MethodPost, suffix: "/cancel"},
		{name: "confirmations", method: http.MethodPost, suffix: "/confirmations"},
		{name: "log redactions", method: http.MethodPost, suffix: "/log-redactions"},
	}
}

// answerOf is the status, the body and the headers of one answer.
type answerOf struct {
	code   int
	body   string
	header http.Header
}

func send(t *testing.T, h http.Handler, rt jobRoute, ns, id string) answerOf {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), rt.method, jobPath(ns, id)+rt.suffix, strings.NewReader("{}"))
	req.Header.Set("Authorization", "token "+testToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return answerOf{code: rec.Code, body: rec.Body.String(), header: rec.Header().Clone()}
}

func (a answerOf) sameAs(b answerOf) bool {
	return a.code == b.code && a.body == b.body && reflect.DeepEqual(a.header, b.header)
}

// hostileJobs returns the stored jobs of the hostile table, by the ID in the
// path. The store holds each one under that ID, so that a resolver that skips
// its own guard would find it.
func hostileJobs(t *testing.T) map[string]job.Job {
	t.Helper()

	withUnit := func(id string, unit identity.Object) job.Job {
		j := runningJob(t, job.ID(id))
		j.Spec.Unit = unit

		return j
	}

	return map[string]job.Job{
		testJob: withUnit(testJob, unitOf(t, "acme", testUnit)),
		// A job of another namespace than the path names.
		"job-anothernamespacejob1": withUnit("job-anothernamespacejob1", unitOf(t, "other", testUnit)),
		// A job whose unit reference is a namespace and no unit.
		"job-notaunitjobnotaunit1": withUnit("job-notaunitjobnotaunit1", identity.NamespaceObject("acme")),
		// A job whose unit has no unit ID.
		"job-nounitidnounitidnoun": withUnit("job-nounitidnounitidnoun", unitOf(t, "acme", "")),
		// An ID with no job prefix, an ID with a byte that is not valid, and an ID
		// that is too long, each stored under the ID of the path.
		"xyz-abcdefghijklmnopqrst":       withUnit("xyz-abcdefghijklmnopqrst", unitOf(t, "acme", testUnit)),
		"job-semi;colon":                 withUnit("job-semi;colon", unitOf(t, "acme", testUnit)),
		"job-" + strings.Repeat("a", 80): withUnit("job-"+strings.Repeat("a", 80), unitOf(t, "acme", testUnit)),
	}
}

// hostileJob is one row of the hostile table of the job routes.
type hostileJob struct {
	name string
	// ns is the namespace of the path.
	ns string
	// id is the job ID of the path, as the client sends it (escaped).
	id string
	// reachesStore is true when the ID is well formed, so that the store read is expected.
	reachesStore bool
}

func wellFormed(name, ns, id string) hostileJob {
	return hostileJob{name: name, ns: ns, id: id, reachesStore: true}
}

func malformedID(name, id string) hostileJob {
	return hostileJob{name: name, ns: "acme", id: id, reachesStore: false}
}

func hostileJobRows() []hostileJob {
	return []hostileJob{
		wellFormed("job of another namespace", "acme", "job-anothernamespacejob1"),
		wellFormed("job of the namespace in the path, read through another namespace", "other", testJob),
		wellFormed("job whose unit is no unit", "acme", "job-notaunitjobnotaunit1"),
		wellFormed("job whose unit has no unit ID", "acme", "job-nounitidnounitidnoun"),
		malformedID("ID with no job prefix", "xyz-abcdefghijklmnopqrst"),
		malformedID("ID with a byte that is not valid", "job-semi%3Bcolon"),
		malformedID("ID with a slash", "job-with%2Fslash"),
		malformedID("ID that is too long", "job-"+strings.Repeat("a", 80)),
	}
}

// TestJobRoutesAnswerHostileJobsLikeAnUnknownJob sends each route that
// resolves a job a hostile ID or a job that the resolver must refuse. Each
// answer is byte-identical to the answer for an unknown job, with an authorizer
// that allows every check. A malformed ID must not reach the store.
func TestJobRoutesAnswerHostileJobsLikeAnUnknownJob(t *testing.T) {
	t.Parallel()

	for _, rt := range jobRoutes() {
		for _, tc := range hostileJobRows() {
			t.Run(rt.name+"/"+tc.name, func(t *testing.T) {
				t.Parallel()

				checkHostileJob(t, rt, tc)
			})
		}
	}
}

func checkHostileJob(t *testing.T, rt jobRoute, tc hostileJob) {
	t.Helper()

	// Control: the good job resolves, so the harness reaches the route.
	if good := send(t, newSpiedHandler(t, hostileJobs(t)).h, rt, "acme", testJob); good.code == http.StatusNotFound {
		t.Fatalf("control: %s answered %d %s for a good job", rt.name, good.code, good.body)
	}

	unknown := send(t, newSpiedHandler(t, hostileJobs(t)).h, rt, "acme", "job-unknownunknownunknow")
	if unknown.code != http.StatusNotFound {
		t.Fatalf("unknown job = %d %s, want 404", unknown.code, unknown.body)
	}

	spied := newSpiedHandler(t, hostileJobs(t))

	got := send(t, spied.h, rt, tc.ns, tc.id)
	if !got.sameAs(unknown) {
		t.Errorf("answer = %d %s %v, want the answer for an unknown job: %d %s %v",
			got.code, got.body, got.header, unknown.code, unknown.body, unknown.header)
	}

	// Defense in depth: a malformed ID must stop before the store.
	if ids := spied.store.gets(); !tc.reachesStore && len(ids) != 0 {
		t.Errorf("store Get called with %q for the malformed ID %q, want no call", ids, tc.id)
	}
}

// spyJobs is a job store that records the ID of each Get.
type spyJobs struct {
	*fakeJobs

	idsMu sync.Mutex
	ids   []job.ID
}

func (s *spyJobs) Get(ctx context.Context, id job.ID) (job.Job, error) {
	s.idsMu.Lock()
	s.ids = append(s.ids, id)
	s.idsMu.Unlock()

	return s.fakeJobs.Get(ctx, id)
}

func (s *spyJobs) gets() []job.ID {
	s.idsMu.Lock()
	defer s.idsMu.Unlock()

	return slices.Clone(s.ids)
}

type spiedHandler struct {
	h     http.Handler
	store *spyJobs
}

// newSpiedHandler returns a handler over a spy store that holds jobs, with an
// authorizer that allows every check.
func newSpiedHandler(t *testing.T, jobs map[string]job.Job) spiedHandler {
	t.Helper()

	inner := newFakeJobs()
	for id := range jobs {
		j := jobs[id]
		inner.set(&j)
	}

	store := &spyJobs{fakeJobs: inner, idsMu: sync.Mutex{}, ids: nil}

	svc := &fakeCanceler{Starter: nil, mu: sync.Mutex{}, calls: nil, job: runningJob(t, testJob), err: nil}

	return spiedHandler{h: newStartHandler(t, allowAll{}, store, svc, nil), store: store}
}
