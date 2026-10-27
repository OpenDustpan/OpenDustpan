package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
)

// expiryServer answers every namespace create with 201 and, when expiresAt
// is not empty, with the expiry warning header.
func expiryServer(t *testing.T, expiresAt string) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/server/namespaces", func(w http.ResponseWriter, _ *http.Request) {
		if expiresAt != "" {
			w.Header().Set(accesstoken.ExpiryHeader, expiresAt)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)

		body := `{"name":"acme","primaryInstallation":"test","failoverVersion":1,"createdAt":"2026-10-11T12:00:00Z"}`
		if _, err := w.Write([]byte(body)); err != nil {
			t.Errorf("write body: %v", err)
		}
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	return server
}

// TestNamespaceCommandWarnsBeforeTokenExpiry proves the CLI warning of ADR
// 53, decision 11: an answer with the expiry header prints one warning on
// stderr that names the expiry and never the token, and an answer without
// it prints none.
func TestNamespaceCommandWarnsBeforeTokenExpiry(t *testing.T) {
	const token = "odp_AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"

	expires := time.Now().Add(5 * 24 * time.Hour).UTC().Format(time.RFC3339)

	cases := []struct {
		header   string
		name     string
		warnings int
	}{
		{header: expires, name: "a token that warns", warnings: 1},
		{header: "", name: "a token far from its expiry", warnings: 0},
		{header: "not a time", name: "a header that is not a time", warnings: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(adminTokenEnvVar, token)

			server := expiryServer(t, tc.header)

			var stdout, stderr bytes.Buffer

			args := []string{"--server", server.URL, "acme"}
			if err := createNamespace(t.Context(), args, &stdout, &stderr); err != nil {
				t.Fatalf("namespace create: %v", err)
			}

			got := stderr.String()
			if n := strings.Count(got, "warning: the access token expires at "+tc.header); n != tc.warnings {
				t.Fatalf("stderr = %q, want %d warnings", got, tc.warnings)
			}

			if strings.Contains(got, token) {
				t.Fatalf("stderr names the token: %q", got)
			}
		})
	}
}
