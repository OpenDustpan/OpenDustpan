package objectcontext_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/drift"
	"opendustpan.org/opendustpan/pkg/graph"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

var errDriftStore = errors.New("drift store broken")

// fakeDriftResults answers GetLatest with the result or the error that it
// holds, and keeps no other method.
type fakeDriftResults struct {
	err    error
	latest drift.Latest
}

func (f fakeDriftResults) Record(context.Context, drift.Result, namespace.Fence) (drift.Transition, error) {
	return drift.Transition{}, errDriftStore
}

func (f fakeDriftResults) GetLatest(context.Context, graph.Unit) (drift.Latest, error) {
	return f.latest, f.err
}

func (f fakeDriftResults) DeleteLatest(context.Context, graph.Unit, namespace.Fence) error {
	return errDriftStore
}

// driftUnit returns the unit that fakeUnits holds.
func driftUnit() graph.Unit {
	return graph.Unit{Namespace: "acme", Tool: "opentofu", ID: testUnitID}
}

// driftLatest returns a drifted result of driftUnit with n addresses.
func driftLatest(n int) drift.Latest {
	resources := make([]drift.Resource, n)
	for i := range resources {
		resources[i] = drift.Resource{Address: "aws_s3_bucket.b" + strconv.Itoa(i), Kind: drift.KindChanged}
	}

	return drift.Latest{
		CheckedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), Unit: driftUnit(), JobID: "job-9",
		Status: drift.StatusDrifted, Resources: resources, Truncated: false,
	}
}

// noDriftLatest returns the zero result that a store gives with an error.
func noDriftLatest() drift.Latest {
	return drift.Latest{
		CheckedAt: time.Time{}, Unit: graph.Unit{Namespace: "", Tool: "", ID: ""}, JobID: "",
		Status: "", Resources: nil, Truncated: false,
	}
}

// driftData is the data of the drift section of a context document.
type driftData struct {
	CheckedAt *time.Time `json:"checkedAt"`
	Status    string     `json:"status"`
	JobID     string     `json:"jobID"`
	Addresses []string   `json:"addresses"`
	Truncated bool       `json:"truncated"`
}

// driftAnswer is the drift section of a context document.
type driftAnswer struct {
	Data   *driftData `json:"data"`
	Action string     `json:"action"`
	Object string     `json:"object"`
	Status string     `json:"status"`
}

// driftWant is what a case expects from the drift section.
type driftWant struct {
	section string
	drift   string
	count   int
	noData  bool
	timed   bool
	cut     bool
}

// wantData expects a section with data.
func wantData(section, status string, count int, timed, cut bool) driftWant {
	return driftWant{section: section, drift: status, count: count, noData: false, timed: timed, cut: cut}
}

// wantNone expects a section with no data.
func wantNone(section string) driftWant {
	return driftWant{section: section, drift: "", count: 0, noData: true, timed: false, cut: false}
}

const driftContextPath = "/api/dustpan/v1/namespaces/acme/units/" + testUnitID + "/context"

// driftHandler returns a handler whose caller holds the read actions of the
// namespace and the unit.
func driftHandler(t *testing.T, results drift.Store) testHandler {
	t.Helper()

	return newHandlerWithDrift(t, newAuthorizer(
		string(identity.ActionNamespaceRead)+" acme",
		string(identity.ActionUnitRead)+" opentofu:acme/net",
	), newFakeStore(), results)
}

// getContext reads path as the test caller.
func getContext(t *testing.T, h http.Handler, path string) *httptest.ResponseRecorder {
	t.Helper()

	return serve(t, h, request{method: http.MethodGet, path: path, token: testToken, body: ""})
}

// checkDriftBody checks the drift section of the body of a context document.
func checkDriftBody(t *testing.T, body []byte, want driftWant) {
	t.Helper()

	var doc struct {
		Sections map[string]driftAnswer `json:"sections"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	got, ok := doc.Sections["drift"]
	if !ok || got.Status != want.section || got.Action != "unit:read" || got.Object != "target" {
		t.Fatalf("drift section %+v, want status %s with unit:read on the target", got, want.section)
	}

	checkDriftData(t, got.Data, want)
}

// checkDriftData checks the data of the drift section.
func checkDriftData(t *testing.T, d *driftData, want driftWant) {
	t.Helper()

	if want.noData {
		if d != nil {
			t.Errorf("data %+v, want none", d)
		}

		return
	}

	if d == nil || d.Status != want.drift || len(d.Addresses) != want.count || d.Truncated != want.cut ||
		(d.CheckedAt != nil) != want.timed || (d.JobID != "") != want.timed {
		t.Errorf("data %+v, want status %s, %d addresses, truncated %t, a check time %t",
			d, want.drift, want.count, want.cut, want.timed)
	}
}

// TestDriftSection reads the drift section of a unit over HTTP: a drifted
// unit, a unit with no check, a server with no drift service, a result of
// another unit, a list over the bound, and a broken store.
func TestDriftSection(t *testing.T) {
	t.Parallel()

	other := driftLatest(1)
	other.Unit = graph.Unit{Namespace: "acme", Tool: "opentofu", ID: "ws-other"}

	unknownStatus := driftLatest(1)
	unknownStatus.Status = "bogus"

	tests := []struct {
		results drift.Store
		want    driftWant
		name    string
	}{
		{
			name: "a drifted unit", results: fakeDriftResults{latest: driftLatest(2), err: nil},
			want: wantData("ok", "drifted", 2, true, false),
		},
		{
			name:    "a unit with no check is unknown",
			results: fakeDriftResults{latest: noDriftLatest(), err: drift.ErrNotFound},
			want:    wantData("ok", "unknown", 0, false, false),
		},
		{name: "no drift service is unavailable", results: nil, want: wantNone("unavailable")},
		{
			name: "a result of another unit is unknown", results: fakeDriftResults{latest: other, err: nil},
			want: wantData("ok", "unknown", 0, false, false),
		},
		{
			name:    "a result with a status that is not known is unknown",
			results: fakeDriftResults{latest: unknownStatus, err: nil},
			want:    wantData("ok", "unknown", 0, false, false),
		},
		{
			name:    "more addresses than the bound are cut",
			results: fakeDriftResults{latest: driftLatest(250), err: nil},
			want:    wantData("partial", "drifted", 100, true, true),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			rec := getContext(t, driftHandler(t, tc.results), driftContextPath+"?include=drift")
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
			}

			checkDriftBody(t, rec.Body.Bytes(), tc.want)
		})
	}
}

// TestDriftSectionBrokenStore proves that a store error fails the call, and shows
// nothing of the error.
func TestDriftSectionBrokenStore(t *testing.T) {
	t.Parallel()

	h := driftHandler(t, fakeDriftResults{latest: noDriftLatest(), err: errDriftStore})

	rec := getContext(t, h, driftContextPath+"?include=drift")
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), errDriftStore.Error()) {
		t.Errorf("status %d body %s, want 500 with no text of the store error", rec.Code, rec.Body)
	}
}

// TestDriftSectionIsOffByDefault proves that a call with no include does not
// read the drift store.
func TestDriftSectionIsOffByDefault(t *testing.T) {
	t.Parallel()

	h := driftHandler(t, fakeDriftResults{latest: noDriftLatest(), err: errDriftStore})

	rec := getContext(t, h, driftContextPath)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d, want 200: %s", rec.Code, rec.Body)
	}

	var doc struct {
		Sections map[string]json.RawMessage `json:"sections"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if _, ok := doc.Sections["drift"]; ok {
		t.Errorf("sections %v hold drift, want it only when asked", doc.Sections)
	}
}

// TestDriftSectionWithoutUnitRead proves that a caller without unit:read gets
// the answer of an object that it cannot read, as for any other section, and
// that the drift store is not read.
func TestDriftSectionWithoutUnitRead(t *testing.T) {
	t.Parallel()

	for _, include := range []string{"drift", "health"} {
		h := newHandlerWithDrift(t, newAuthorizer(string(identity.ActionNamespaceRead)+" acme"), newFakeStore(),
			fakeDriftResults{latest: driftLatest(2), err: nil})

		rec := getContext(t, h, driftContextPath+"?include="+include)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "aws_s3_bucket") {
			t.Errorf("include=%s: status %d body %s, want 404 with no address", include, rec.Code, rec.Body)
		}
	}
}
