package server_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestGitHostWritesRefuseAnAccessToken proves that an access token never gives
// the fresh authentication that the four writes of the Git host registration
// need: each write answers 403 with mfa.fresh-step-required, and the
// registration and the assignment do not change.
func TestGitHostWritesRefuseAnAccessToken(t *testing.T) {
	t.Parallel()

	writes := []struct {
		name, method, target, body string
	}{
		{"register the app", http.MethodPut, hostAppPath, hostAppBody()},
		{"delete the app", http.MethodDelete, hostAppPath, ""},
		{"assign an installation", http.MethodPut, hostInstallationsPath + "/102", `{"namespace":"acme"}`},
		{"unassign an installation", http.MethodDelete, hostInstallationsPath + "/101", ""},
	}

	for _, w := range writes {
		t.Run(w.name, func(t *testing.T) {
			t.Parallel()

			e := newHostEnv(t, hostAllowAll())
			e.register(t)

			var body io.Reader = http.NoBody
			if w.body != "" {
				body = strings.NewReader(w.body)
			}

			req := httptest.NewRequestWithContext(t.Context(), w.method, w.target, body)
			req.RemoteAddr = hostRemote
			req.Header.Set("Authorization", "Bearer test-token")

			rec := httptest.NewRecorder()
			e.handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden ||
				!strings.Contains(rec.Body.String(), `"errorCode":"mfa.fresh-step-required"`) {
				t.Fatalf("%s %s with an access token = %d %s, want 403 with mfa.fresh-step-required",
					w.method, w.target, rec.Code, rec.Body.String())
			}

			app := e.send(t, http.MethodGet, hostAppPath, "")
			wantHost(t, app, http.StatusOK)

			if !strings.Contains(app.body, `"version":1`) {
				t.Errorf("app after the refused write = %s, want version 1", app.body)
			}

			listed := e.send(t, http.MethodGet, hostInstallationsPath, "")
			wantHost(t, listed, http.StatusOK)

			if !strings.Contains(listed.body, `"101"`) || strings.Contains(listed.body, `"102"`) {
				t.Errorf("installations after the refused write = %s, want 101 only", listed.body)
			}
		})
	}
}
