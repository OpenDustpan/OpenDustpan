package jobs_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"testing"

	"opendustpan.org/opendustpan/pkg/job"
)

// locationDocument is the location member of a job document. Source keeps the
// raw member, so that a test tells an absent source member from an empty one.
type locationDocument struct {
	Connection string          `json:"connection"`
	Repository string          `json:"repository"`
	Ref        string          `json:"ref"`
	Directory  string          `json:"directory"`
	Revision   string          `json:"revision"`
	Source     json.RawMessage `json:"source"`
}

// sourceDocument holds the members of a job document that name the code
// location.
type sourceDocument struct {
	Location  locationDocument `json:"location"`
	Defaulted []string         `json:"defaulted"`
}

// TestJobDocumentShowsTheLocationAndItsSource proves that the location of a
// job document holds the effective value and its source.
func TestJobDocumentShowsTheLocationAndItsSource(t *testing.T) {
	t.Parallel()

	full := job.Location{Connection: "", Repository: "acme/infra", Ref: "main", Directory: "net", Revision: ""}
	zero := job.Location{Connection: "", Repository: "", Ref: "", Directory: "", Revision: ""}

	tests := []struct {
		name      string
		location  job.Location
		source    job.LocationSource
		defaulted []string
		// wantSrc is the wanted source member: nil wants no member at all.
		wantSrc *string
	}{
		{"the request gave the location", full, job.LocationSourceRequest, nil, new("request")},
		{"the unit settings gave the location", full, job.LocationSourceUnit, []string{"location"}, new("unit")},
		{
			"the namespace default connection gave the connection",
			full,
			job.LocationSourceNamespace,
			[]string{"location"},
			new("namespace"),
		},
		{"a defaulted location without a stored source", full, "", []string{"location"}, new("unit")},
		{"no stored source and no defaulted location", full, "", nil, new("request")},
		{"a stored source that this server does not know", full, "elsewhere", []string{"location"}, nil},
		{"a zero location with a stored source", zero, job.LocationSourceRequest, nil, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			stored := runningJob(t, testJob)
			stored.Spec.Location = tt.location
			stored.Spec.LocationSource = tt.source
			stored.Spec.Defaulted = tt.defaulted

			handler := newHandler(t, newAuthorizer(holdsBoth()...), newFakeJobs(stored))
			rec := serve(t, handler, request{method: http.MethodGet, path: jobPath("acme", testJob), token: testToken})
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d %s, want 200", rec.Code, rec.Body.String())
			}

			checkLocationDocument(t, rec.Body.Bytes(), tt.location, tt.wantSrc, tt.defaulted)
		})
	}
}

// checkLocationDocument proves that body holds the location and its source. A
// nil wantSrc requires the location to have no source member at all, not a
// member with an empty value.
func checkLocationDocument(
	t *testing.T, body []byte, want job.Location, wantSrc *string, wantDefaulted []string,
) {
	t.Helper()

	var doc sourceDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}

	got := job.Location{
		Connection: doc.Location.Connection, Repository: doc.Location.Repository, Ref: doc.Location.Ref,
		Directory: doc.Location.Directory, Revision: doc.Location.Revision,
	}

	if got != want {
		t.Errorf("location = %+v, want %+v", got, want)
	}

	var gotSrc *string
	if doc.Location.Source != nil {
		var src string
		if err := json.Unmarshal(doc.Location.Source, &src); err != nil {
			t.Fatalf("decode the source of %s: %v", body, err)
		}

		gotSrc = &src
	}

	if !sameSource(gotSrc, wantSrc) {
		t.Errorf("location source = %s, want %s", sourceText(gotSrc), sourceText(wantSrc))
	}

	if !slices.Equal(doc.Defaulted, wantDefaulted) {
		t.Errorf("defaulted = %v, want %v", doc.Defaulted, wantDefaulted)
	}
}

// sameSource reports whether got and want name the same source member: both
// absent, or both present with the same value.
func sameSource(got, want *string) bool {
	if got == nil {
		return want == nil
	}

	return want != nil && *got == *want
}

// sourceText names the source member of a test answer.
func sourceText(src *string) string {
	if src == nil {
		return "no source member"
	}

	return strconv.Quote(*src)
}
