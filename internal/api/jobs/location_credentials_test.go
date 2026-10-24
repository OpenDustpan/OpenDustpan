package jobs_test

import (
	"net/http"
	"strings"
	"sync"
	"testing"
)

// TestStartRefusesRepositoryCredentials proves that a job start with a
// repository that holds credentials or names another scheme is refused at the
// boundary, never reaches the job service, and is not echoed.
func TestStartRefusesRepositoryCredentials(t *testing.T) {
	t.Parallel()

	for _, repo := range []string{
		"https://user:hunter2@example.com/r.git", "https://user@example.com/r.git", "file:///etc",
		"ssh://git@example.com/r.git", "git@example.com:r.git",
	} {
		t.Run(repo, func(t *testing.T) {
			t.Parallel()

			starter := newFakeStarter(t)
			entries := &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil}
			h := newStartHandler(t, newAuthorizer(readsNamespace()...), newFakeJobs(), starter, entries)

			body := strings.Replace(goodBody, `"repository":"acme/infra"`, `"repository":"`+repo+`"`, 1)

			rec := post(t, h, testToken, body)
			if rec.Code != http.StatusBadRequest || starter.calls() != 0 {
				t.Fatalf("POST = %d %s with %d starts, want 400 and none", rec.Code, rec.Body.String(),
					starter.calls())
			}

			if strings.Contains(rec.Body.String(), "hunter2") || strings.Contains(rec.Body.String(), "example.com") {
				t.Fatalf("the answer echoes the repository: %s", rec.Body.String())
			}
		})
	}
}
