package drift_test

import (
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/fakecred"
)

// TestStartCheckRefusesRepositoryCredentials proves that a drift check start
// with a repository that holds credentials or names another scheme is refused
// at the boundary, never reaches the checker, and is not echoed.
func TestStartCheckRefusesRepositoryCredentials(t *testing.T) {
	t.Parallel()

	for _, repo := range []string{
		fakecred.URL("https", "user", "hunter2", "example.com", "/r.git"), "https://user@example.com/r.git", "file:///etc",
		"ssh://git@example.com/r.git", "git@example.com:r.git",
	} {
		t.Run(repo, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, newAuthorizer(holdsAll()...))

			rec := r.serve(t, startRequest(`{"location":{"repository":"`+repo+`","ref":"main"}}`))
			if rec.Code != http.StatusBadRequest || r.checker.calls() != 0 {
				t.Fatalf("POST = %d %s with %d checks, want 400 and none", rec.Code, rec.Body.String(),
					r.checker.calls())
			}

			if strings.Contains(rec.Body.String(), "hunter2") || strings.Contains(rec.Body.String(), "example.com") {
				t.Fatalf("the answer echoes the repository: %s", rec.Body.String())
			}
		})
	}
}

// TestStartCheckRefusesRepositoryAddressHost proves that a drift check start
// with an https repository URL whose host is an IP address, a number or
// localhost is refused at the boundary, never reaches the checker, and is
// not echoed.
func TestStartCheckRefusesRepositoryAddressHost(t *testing.T) {
	t.Parallel()

	for _, host := range []string{
		"10.0.0.5", "169.254.169.254", "[fd00::1]", "[fe80::1%25eth0]", "[::ffff:127.0.0.1]", "2130706433",
		"0x7f000001", "0177.0.0.1", "localhost", "git.localhost",
	} {
		t.Run(host, func(t *testing.T) {
			t.Parallel()

			r := newRig(t, newAuthorizer(holdsAll()...))

			repo := "https://" + host + "/acme/infra.git"

			rec := r.serve(t, startRequest(`{"location":{"repository":"`+repo+`","ref":"main"}}`))
			if rec.Code != http.StatusBadRequest || r.checker.calls() != 0 {
				t.Fatalf("POST = %d %s with %d checks, want 400 and none", rec.Code, rec.Body.String(),
					r.checker.calls())
			}

			if strings.Contains(rec.Body.String(), host+"/acme") {
				t.Fatalf("the answer echoes the repository: %s", rec.Body.String())
			}
		})
	}
}
