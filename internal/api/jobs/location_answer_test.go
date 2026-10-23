package jobs_test

import (
	"net/http"
	"strings"
	"testing"
)

// TestGetJobNeverAnswersRepositoryCredentials proves that the read of a job
// whose stored repository holds a user name and a password answers no part
// of them, and that a repository the location check accepts is answered as
// stored.
func TestGetJobNeverAnswersRepositoryCredentials(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		repository string
		want       string
	}{
		{"https://user:hunter2@git.example.com/acme/infra.git", `"location":{"ref":"main"`},
		{"ssh://git@git.example.com/acme/infra.git", `"location":{"ref":"main"`},
		{"https://git.example.com/acme/infra.git", `"repository":"https://git.example.com/acme/infra.git"`},
		{"acme/infra", `"repository":"acme/infra"`},
	} {
		t.Run(tc.repository, func(t *testing.T) {
			t.Parallel()

			stored := runningJob(t, testJob)
			stored.Spec.Location.Repository = tc.repository

			handler := newHandler(t, newAuthorizer(holdsBoth()...), newFakeJobs(stored))
			rec := serve(t, handler, request{method: http.MethodGet, path: jobPath("acme", testJob), token: testToken})

			body := rec.Body.String()
			if rec.Code != http.StatusOK || !strings.Contains(body, tc.want) {
				t.Fatalf("status = %d, body = %s; want 200 with %s", rec.Code, body, tc.want)
			}

			if strings.Contains(body, "hunter2") || strings.Contains(body, "user:") || strings.Contains(body, "git@") {
				t.Fatalf("the answer holds the credentials of the repository: %s", body)
			}
		})
	}
}
