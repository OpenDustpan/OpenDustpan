package jobs_test

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/pkg/job"
)

// locationDocument is the location member of a job document.
type locationDocument struct {
	Connection string `json:"connection"`
	Repository string `json:"repository"`
	Ref        string `json:"ref"`
	Directory  string `json:"directory"`
	Revision   string `json:"revision"`
	Source     string `json:"source"`
}

// sourceDocument holds the members of a job document that name the code
// location.
type sourceDocument struct {
	Location  locationDocument `json:"location"`
	Defaulted []string         `json:"defaulted"`
}

// TestJobDocumentShowsTheLocationAndItsSource proves that the location of a
// job document holds the effective value and its source, also for a request
// that gave only a revision.
func TestJobDocumentShowsTheLocationAndItsSource(t *testing.T) {
	t.Parallel()

	full := job.Location{Connection: "", Repository: "acme/infra", Ref: "main", Directory: "net", Revision: ""}
	atRevision := full
	atRevision.Revision = "abcdef0"

	tests := []struct {
		name      string
		location  job.Location
		source    job.LocationSource
		defaulted []string
		wantSrc   string
	}{
		{"the request gave the location", full, job.LocationSourceRequest, nil, "request"},
		{"the unit settings gave the location", full, job.LocationSourceUnit, []string{"location"}, "unit"},
		{
			"the namespace default connection gave the connection",
			full,
			job.LocationSourceNamespace,
			[]string{"location"},
			"namespace",
		},
		{
			"the request gave only a revision",
			atRevision,
			job.LocationSourceUnit,
			[]string{"location"},
			"unit",
		},
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

// checkLocationDocument proves that body holds the location and its source.
func checkLocationDocument(t *testing.T, body []byte, want job.Location, wantSrc string, wantDefaulted []string) {
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

	if doc.Location.Source != wantSrc {
		t.Errorf("location source = %q, want %q", doc.Location.Source, wantSrc)
	}

	if !slices.Equal(doc.Defaulted, wantDefaulted) {
		t.Errorf("defaulted = %v, want %v", doc.Defaulted, wantDefaulted)
	}
}
