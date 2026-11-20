package jobs_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	jobsapi "opendustpan.org/opendustpan/internal/api/jobs"
	"opendustpan.org/opendustpan/pkg/identity"
	coreinventory "opendustpan.org/opendustpan/pkg/inventory"
	"opendustpan.org/opendustpan/pkg/job"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// damagedText is the text of the 409 for a damaged job.
const damagedText = "The stored job is damaged. The server cannot read it. " +
	"Cancel the job with POST /api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/cancel."

// damagedRoute is a route that must refuse a damaged job with 409.
type damagedRoute struct{ name, method, suffix string }

// damagedReadRoutes returns the routes that must refuse a damaged job.
func damagedReadRoutes() []damagedRoute {
	return []damagedRoute{
		{"get", http.MethodGet, ""},
		{"log", http.MethodGet, "/logs/plan"},
		{"approvals", http.MethodPost, "/approvals"},
		{"confirmations", http.MethodPost, "/confirmations"},
		{"log redactions", http.MethodPost, "/log-redactions"},
	}
}

// damagedSecret is text of the row and of a store failure that no answer may
// hold.
const damagedSecret = "the body is not JSON"

// damagedReadRig is a handler whose store holds one damaged row of namespace
// ns. The row has the reason damagedSecret, which no answer may show.
func damagedReadRig(
	t *testing.T, ns namespace.Name, unitID string, units coreinventory.Reader, authz identity.Authorizer,
) (testHandler, *fakeCanceler) {
	t.Helper()

	svc := &fakeCanceler{Starter: nil, mu: sync.Mutex{}, calls: nil, job: runningJob(t, testJob), err: nil}
	store := newFakeJobs()
	store.damaged = map[job.ID]fakeDamaged{
		testJob: {row: job.DamagedRow{ID: testJob, UnitID: unitID, Reason: damagedSecret}, ns: ns},
	}

	h := newWorkerHandler(t, authz, handlerDeps{
		jobs: store, units: units, starter: svc,
		entries:   &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil},
		artifacts: newFakeLogs(fakeLog{data: "", bad: "", err: nil}), logs: fastLogs(), cursors: nil,
	}, nil)

	return h, svc
}

func readerHolds() []string {
	return append(readsUnits(testUnit),
		"job:approve opentofu:acme/net-"+testUnit,
		"unit:redact opentofu:acme/net-"+testUnit,
		"job:read-logs opentofu:acme/net-"+testUnit,
	)
}

func sendTo(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader("{}"))
	req.Header.Set("Authorization", "token "+testToken)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// TestDamagedJobAnswers409ForAReader proves that a caller who may read the
// unit of a damaged job gets 409 with the code job.damaged and the fixed text,
// on every route except cancel. The answer holds no text of the row, and the
// job service is not called.
func TestDamagedJobAnswers409ForAReader(t *testing.T) {
	t.Parallel()

	for _, rt := range damagedReadRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()

			h, svc := damagedReadRig(t, "acme", testUnit, unitsOf(testUnit), newAuthorizer(readerHolds()...))

			rec := sendTo(t, h, rt.method, jobPath("acme", testJob)+rt.suffix)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), http.StatusConflict)
			}

			checkDamagedBody(t, rec.Body.String())

			if n := len(svc.called()); n != 0 {
				t.Errorf("the service got %d calls, want 0", n)
			}
		})
	}
}

// checkDamagedBody checks the code and the text of the 409 answer, and that
// the answer holds no value of the row.
func checkDamagedBody(t *testing.T, body string) {
	t.Helper()

	var got struct {
		ErrorCode string `json:"errorCode"`
		Message   string `json:"message"`
	}

	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}

	if got.ErrorCode != "job.damaged" || got.Message != damagedText {
		t.Errorf("answer = %q %q, want job.damaged %q", got.ErrorCode, got.Message, damagedText)
	}

	if strings.Contains(body, damagedSecret) || strings.Contains(body, testUnit) {
		t.Errorf("body holds a value of the row: %s", body)
	}
}

// hostileDamaged is one hostile damaged row, or one hostile caller of a
// damaged row. Each must answer like an unknown job.
type hostileDamaged struct {
	name   string
	rowNs  namespace.Name
	unitID string
	units  coreinventory.Reader
	// holds is what the caller holds. A nil value means readerHolds.
	holds []string
	// allowAll swaps the authorizer for one that allows every check, so that
	// only the resolver can refuse.
	allowAll bool
}

// resolverRow is a hostile row that only the resolver can refuse: the
// authorizer allows every check.
func resolverRow(name string, rowNs namespace.Name, unitID string, units coreinventory.Reader) hostileDamaged {
	return hostileDamaged{name: name, rowNs: rowNs, unitID: unitID, units: units, holds: nil, allowAll: true}
}

// callerRow is a hostile caller: the authorizer holds only holds.
func callerRow(name string, holds []string) hostileDamaged {
	return hostileDamaged{
		name: name, rowNs: "acme", unitID: testUnit, units: unitsOf(testUnit), holds: holds, allowAll: false,
	}
}

func hostileDamagedRows() []hostileDamaged {
	foreignUnit := unitsOf(testUnit)
	e := foreignUnit[testUnit]
	e.Namespace = "other"
	foreignUnit[testUnit] = e

	long := "unt-" + strings.Repeat("a", 200)

	return []hostileDamaged{
		callerRow("caller without job:read on the unit", readsNamespace()),
		callerRow("caller without any right", []string{"nothing"}),
		resolverRow("row of another namespace", "other", testUnit, unitsOf(testUnit)),
		resolverRow("unit that does not exist", "acme", testUnit, unitsOf()),
		resolverRow("no inventory", "acme", testUnit, nil),
		resolverRow("unit ID with a byte that is not valid", "acme", "unt-bad;id", unitsOf("unt-bad;id")),
		resolverRow("empty unit ID", "acme", "", unitsOf("")),
		resolverRow("unit ID that is too long", "acme", long, unitsOf(long)),
		resolverRow("unit of another namespace in the inventory", "acme", testUnit, foreignUnit),
	}
}

// TestDamagedJobHostileRowsAnswerLikeAnUnknownJob is the hostile table of the
// route resolve and of the wrapper. For each route, each hostile row or caller
// gets the answer for an unknown job, byte for byte: the same status, body and
// headers. No answer tells that the damaged row exists.
func TestDamagedJobHostileRowsAnswerLikeAnUnknownJob(t *testing.T) {
	t.Parallel()

	for _, rt := range damagedReadRoutes() {
		for _, hc := range hostileDamagedRows() {
			t.Run(rt.name+"/"+hc.name, func(t *testing.T) {
				t.Parallel()

				checkLikeUnknownJob(t, rt, hc)
			})
		}
	}
}

// holdsOf returns what the caller of a hostile row holds.
func holdsOf(hc hostileDamaged) []string {
	if hc.holds == nil {
		return readerHolds()
	}

	return hc.holds
}

// checkLikeUnknownJob sends the route for the damaged job and for an unknown
// job, and requires the same answer.
func checkLikeUnknownJob(t *testing.T, rt damagedRoute, hc hostileDamaged) {
	t.Helper()

	var authz identity.Authorizer = allowAll{}
	if !hc.allowAll {
		authz = newAuthorizer(holdsOf(hc)...)
	}

	h, svc := damagedReadRig(t, hc.rowNs, hc.unitID, hc.units, authz)
	path := func(id string) string { return jobPath("acme", id) + rt.suffix }

	got := answerFor(sendTo(t, h, rt.method, path(testJob)))
	want := answerFor(sendTo(t, h, rt.method, path("job-zzzzzzzzzzzzzzzzzzzz")))

	if want.code != http.StatusNotFound {
		t.Fatalf("the unknown job answers %d, want 404", want.code)
	}

	checkAnswerOf(t, "damaged row", got, want)

	if n := len(svc.called()); n != 0 {
		t.Errorf("the service got %d calls, want 0", n)
	}
}

// TestDamagedJobFailureOfTheInventoryAnswers500WithNoRowText proves that a
// failure of the inventory is no 409 and no 404, and that its text and the
// text of the row reach neither the answer nor the log.
func TestDamagedJobFailureOfTheInventoryAnswers500WithNoRowText(t *testing.T) {
	t.Parallel()

	for _, rt := range damagedReadRoutes() {
		t.Run(rt.name, func(t *testing.T) {
			t.Parallel()

			h, _ := damagedReadRig(t, "acme", testUnit, downUnits{unitsOf(testUnit)}, allowAll{})

			rec := sendTo(t, h, rt.method, jobPath("acme", testJob)+rt.suffix)
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d %s, want 500", rec.Code, rec.Body.String())
			}

			noText(t, rec.Body.String(), "the answer")

			if strings.Contains(rec.Body.String(), damagedSecret) {
				t.Errorf("answer holds the row: %s", rec.Body.String())
			}
		})
	}
}

// TestDamagedJobHandlersRefuseWithoutTheWrapper is the test of the handler
// layer. It calls the handler of each route that must refuse a damaged job
// with no wrapper before it: no sign-in, no check, no resolver. Each handler
// answers 409 and does not touch its service.
func TestDamagedJobHandlersRefuseWithoutTheWrapper(t *testing.T) {
	t.Parallel()

	h, svc := damagedReadRig(t, "acme", testUnit, unitsOf(testUnit), allowAll{})

	routes := jobsapi.DamagedRoutes(h.Handler)
	refusing, serving := routes.Refusing, routes.Serving

	// The table of the routes is the table of the server: a new route that
	// resolves a job must be here, and must refuse a damaged job.
	wantServing := [][2]string{{http.MethodPost, "/api/dustpan/v1/namespaces/{namespace}/jobs/{job_id}/cancel"}}
	if !slices.Equal(serving, wantServing) {
		t.Fatalf("routes that serve a damaged job = %v, want only %v", serving, wantServing)
	}

	if len(refusing) < len(damagedReadRoutes()) {
		t.Fatalf("routes that refuse a damaged job = %v, want at least %d", refusing, len(damagedReadRoutes()))
	}

	unit := unitOf(t, "acme", testUnit)

	for _, row := range refusing {
		t.Run(row[0]+" "+row[1], func(t *testing.T) {
			t.Parallel()

			path := strings.NewReplacer("{namespace}", "acme", "{job_id}", testJob, "{phase}", "plan").Replace(row[1])
			req := httptest.NewRequestWithContext(t.Context(), row[0], path, strings.NewReader("{}"))
			req.SetPathValue("namespace", "acme")
			req.SetPathValue("job_id", testJob)
			req.SetPathValue("phase", "plan")

			rec := httptest.NewRecorder()
			if !jobsapi.ServeHandlerOnly(h.Handler, rec, req, row[1], unit) {
				t.Fatalf("no route %s %s", row[0], row[1])
			}

			// The routes that do not resolve a stored job are not in the table.
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d %s, want %d", rec.Code, rec.Body.String(), http.StatusConflict)
			}

			if n := len(svc.called()); n != 0 {
				t.Errorf("the service got %d calls, want 0", n)
			}
		})
	}
}

// TestDamagedJobWrapperChecksBeforeItAnswers is the test of the wrapper layer
// alone. A probe route has a handler that writes "served". The wrapper must
// answer 404 for a caller who may not read the unit, 409 for one who may, and
// never run the handler of a route that does not serve a damaged job. A route
// that serves a damaged job runs its handler for a reader.
func TestDamagedJobWrapperChecksBeforeItAnswers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		holds         []string
		servesDamaged bool
		wantCode      int
		wantServed    bool
	}{
		{"reader, route refuses", readerHolds(), false, http.StatusConflict, false},
		{"no job:read, route refuses", readsNamespace(), false, http.StatusNotFound, false},
		{"no job:read, route serves", readsNamespace(), true, http.StatusNotFound, false},
		{"reader, route serves", readerHolds(), true, http.StatusOK, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h, _ := damagedReadRig(t, "acme", testUnit, unitsOf(testUnit), newAuthorizer(tc.holds...))
			mux := http.NewServeMux()
			mux.Handle(jobsapi.ProbePattern, jobsapi.ProbeWrapper(h.Handler, tc.servesDamaged))

			rec := sendTo(t, mux, http.MethodGet, jobPath("acme", testJob))
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d %s, want %d", rec.Code, rec.Body.String(), tc.wantCode)
			}

			if served := strings.Contains(rec.Body.String(), "served"); served != tc.wantServed {
				t.Errorf("handler ran = %t, want %t", served, tc.wantServed)
			}
		})
	}
}
