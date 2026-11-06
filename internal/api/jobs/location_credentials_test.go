package jobs_test

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"opendustpan.org/opendustpan/internal/fakecred"
)

// TestStartRefusesRepositoryCredentials proves that a job start with a
// repository that holds credentials or names another scheme is refused at the
// boundary, never reaches the job service, and is not echoed.
func TestStartRefusesRepositoryCredentials(t *testing.T) {
	t.Parallel()

	for _, repo := range []string{
		fakecred.URL("https", "user", "hunter2", "example.com", "/r.git"), "https://user@example.com/r.git", "file:///etc",
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

// TestStartRefusesRepositoryAddressHost proves that a job start with an https
// repository URL whose host is an IP address, a number or localhost is
// refused at the boundary, never reaches the job service, and is not echoed.
// Each host could make a fetch of the code reach an internal service.
func TestStartRefusesRepositoryAddressHost(t *testing.T) {
	t.Parallel()

	for _, host := range []string{
		"10.0.0.5", "169.254.169.254", "[fd00::1]", "[fe80::1%25eth0]", "[::ffff:127.0.0.1]", "2130706433",
		"0x7f000001", "0177.0.0.1", "localhost", "git.localhost",
	} {
		t.Run(host, func(t *testing.T) {
			t.Parallel()

			starter := newFakeStarter(t)
			entries := &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil}
			h := newStartHandler(t, newAuthorizer(readsNamespace()...), newFakeJobs(), starter, entries)

			repo := "https://" + host + "/acme/infra.git"
			body := strings.Replace(goodBody, `"repository":"acme/infra"`, `"repository":"`+repo+`"`, 1)

			rec := post(t, h, testToken, body)
			if rec.Code != http.StatusBadRequest || starter.calls() != 0 {
				t.Fatalf("POST = %d %s with %d starts, want 400 and none", rec.Code, rec.Body.String(),
					starter.calls())
			}

			if strings.Contains(rec.Body.String(), host+"/acme") {
				t.Fatalf("the answer echoes the repository: %s", rec.Body.String())
			}
		})
	}
}
