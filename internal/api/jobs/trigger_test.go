package jobs_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/job"
)

// TestJobDocumentNamesTheTrigger proves that the document of a job names the
// trigger that started it and its saver, and that a job with no trigger has no
// such field.
func TestJobDocumentNamesTheTrigger(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		trigger *job.TriggerRef
		want    string
	}{
		{
			"a job that a trigger started",
			&job.TriggerRef{ID: "trg-1", Saver: "user:ada"},
			`"trigger":{"id":"trg-1","saver":"user:ada"}`,
		},
		{"a job that no trigger started", nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			j := runningJob(t, testJob)
			j.Trigger = tt.trigger

			h := newHandler(t, newAuthorizer(holdsBoth()...), newFakeJobs(j))
			rec := serve(t, h, request{method: http.MethodGet, path: jobPath("acme", testJob), token: testToken})

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d %s, want 200", rec.Code, rec.Body.String())
			}

			body := rec.Body.String()
			if tt.want == "" && strings.Contains(body, `"trigger"`) {
				t.Errorf("body = %s, want no trigger field", body)
			}

			if tt.want != "" && !strings.Contains(body, tt.want) {
				t.Errorf("body = %s, want %s", body, tt.want)
			}
		})
	}
}
