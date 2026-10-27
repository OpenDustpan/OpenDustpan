package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// namespaceFakeServer answers POST /api/server/namespaces from a scripted
// status and body. It checks the Bearer credential on every request and
// records the token it saw.
type namespaceFakeServer struct {
	server *httptest.Server
	token  atomic.Value
	status int
	body   string
}

// newNamespaceFakeServer starts the fake server expecting token as the Bearer
// credential and answering status with body.
func newNamespaceFakeServer(t *testing.T, token string, status int, body string) *namespaceFakeServer {
	t.Helper()

	fake := &namespaceFakeServer{server: nil, token: atomic.Value{}, status: status, body: body}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/server/namespaces", func(w http.ResponseWriter, r *http.Request) {
		fake.token.Store(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))

		if r.Header.Get("Authorization") != "Bearer "+token {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)

			if err := json.NewEncoder(w).Encode(map[string]any{"code": 401, "message": "unauthenticated"}); err != nil {
				t.Errorf("write 401 body: %v", err)
			}

			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(fake.status)

		if _, err := w.Write([]byte(fake.body)); err != nil {
			t.Errorf("write namespace body: %v", err)
		}
	})

	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)

	return fake
}

// runNamespaceCreate runs `namespace create` against server with the token in
// DUSTPAN_ADMIN_TOKEN and returns its stdout and error. A nil error means
// exit code 0, a non-nil error means exit code 1.
func runNamespaceCreate(t *testing.T, server, token, name string, extra ...string) (string, error) {
	t.Helper()
	t.Setenv(adminTokenEnvVar, token)

	args := append([]string{"--server", server}, extra...)
	args = append(args, name)

	var stdout, stderr bytes.Buffer

	err := createNamespace(t.Context(), args, &stdout, &stderr)

	return stdout.String(), err
}

// TestNamespaceCommandSuccess proves the command posts the name and prints
// the created namespace with its primary installation.
func TestNamespaceCommandSuccess(t *testing.T) {
	t.Setenv(adminTokenEnvVar, "test-token")

	fake := newNamespaceFakeServer(
		t,
		"test-token",
		http.StatusCreated,
		`{"name":"acme","primaryInstallation":"test","failoverVersion":1,"createdAt":"2026-10-11T12:00:00Z"}`,
	)

	out, err := runNamespaceCreate(t, fake.server.URL, "test-token", "acme")
	if err != nil {
		t.Fatalf("namespace create: %v", err)
	}

	if !strings.Contains(out, "created namespace acme, primary installation test") {
		t.Fatalf("namespace output lacks the namespace and installation:\n%s", out)
	}

	got, ok := fake.token.Load().(string)
	if !ok || got != "test-token" {
		t.Fatalf("server saw token %q, want test-token", got)
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestNamespaceCommandUsesFirstToken(t *testing.T) {
	fake := newNamespaceFakeServer(
		t,
		"first-token",
		http.StatusCreated,
		`{"name":"beta","primaryInstallation":"test","failoverVersion":1,"createdAt":"2026-10-11T12:00:00Z"}`,
	)

	out, err := runNamespaceCreate(t, fake.server.URL, "first-token,second-token", "beta")
	if err != nil {
		t.Fatalf("namespace create: %v", err)
	}

	if !strings.Contains(out, "created namespace beta") {
		t.Fatalf("namespace output lacks the namespace:\n%s", out)
	}

	got, ok := fake.token.Load().(string)
	if !ok || got != "first-token" {
		t.Fatalf("server saw token %q, want the first entry first-token", got)
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestNamespaceCommandDuplicate(t *testing.T) {
	fake := newNamespaceFakeServer(
		t,
		"test-token",
		http.StatusConflict,
		`{"code":409,"message":"namespace already exists: \"acme\""}`,
	)

	_, err := runNamespaceCreate(t, fake.server.URL, "test-token", "acme")
	if err == nil {
		t.Fatal("duplicate namespace succeeded, want an error for exit code 1")
	}

	if !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("namespace error = %v, want it to name the duplicate", err)
	}

	if strings.Contains(err.Error(), "again") {
		t.Fatalf("namespace error = %v, want no retry hint for a refused request", err)
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestNamespaceCommandNeedsToken(t *testing.T) {
	if err := os.Unsetenv(adminTokenEnvVar); err != nil {
		t.Fatalf("unset %s: %v", adminTokenEnvVar, err)
	}

	if err := os.Unsetenv(adminTokenFileEnvVar); err != nil {
		t.Fatalf("unset %s: %v", adminTokenFileEnvVar, err)
	}

	var stdout, stderr bytes.Buffer

	err := createNamespace(t.Context(), []string{"--server", "http://127.0.0.1:1", "acme"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("namespace create without a token succeeded, want an error for exit code 1")
	}

	if !strings.Contains(err.Error(), adminTokenEnvVar) {
		t.Fatalf("namespace error = %v, want it to name %s", err, adminTokenEnvVar)
	}

	if !strings.Contains(err.Error(), "opendustpan namespace create") {
		t.Fatalf("namespace error = %v, want it to name the one command that helps", err)
	}
}

func TestNamespaceCommandRejectsTokenFlag(t *testing.T) {
	t.Setenv(adminTokenEnvVar, "test-token")

	var stdout, stderr bytes.Buffer

	err := createNamespace(
		t.Context(),
		[]string{"--server", "http://127.0.0.1:1", "--token", "test-token", "acme"},
		&stdout,
		&stderr,
	)
	if err == nil {
		t.Fatal("namespace create with --token succeeded, want an error: the token comes from the environment only")
	}
}

func TestNamespaceCommandNeedsServer(t *testing.T) {
	t.Setenv(adminTokenEnvVar, "test-token")

	if err := os.Unsetenv(backupServerEnvVar); err != nil {
		t.Fatalf("unset %s: %v", backupServerEnvVar, err)
	}

	var stdout, stderr bytes.Buffer

	err := createNamespace(t.Context(), []string{"acme"}, &stdout, &stderr)
	if err == nil {
		t.Fatal("namespace create without --server succeeded, want an error for exit code 1")
	}

	if !strings.Contains(err.Error(), "--server") {
		t.Fatalf("namespace error = %v, want it to name --server", err)
	}
}

func TestNamespaceCommandServerFromEnvironment(t *testing.T) {
	fake := newNamespaceFakeServer(
		t,
		"test-token",
		http.StatusCreated,
		`{"name":"acme","primaryInstallation":"test","failoverVersion":1,"createdAt":"2026-10-11T12:00:00Z"}`,
	)

	t.Setenv(adminTokenEnvVar, "test-token")
	t.Setenv(backupServerEnvVar, fake.server.URL)

	var stdout, stderr bytes.Buffer

	if err := createNamespace(t.Context(), []string{"acme"}, &stdout, &stderr); err != nil {
		t.Fatalf("namespace create with %s: %v", backupServerEnvVar, err)
	}

	if !strings.Contains(stdout.String(), "created namespace acme") {
		t.Fatalf("namespace output lacks the namespace:\n%s", stdout.String())
	}
}

//nolint:paralleltest // The command reads process environment, so this test sets it and runs in order.
func TestNamespaceCommandBadCAFile(t *testing.T) {
	missing := "/nonexistent-dir/missing-ca.pem"

	_, err := runNamespaceCreate(t, "http://127.0.0.1:1", "test-token", "acme", "--ca-file", missing)
	if err == nil {
		t.Fatal("namespace create with a missing CA file succeeded, want an error for exit code 1")
	}

	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("namespace error = %v, want it to name %s", err, missing)
	}
}

func TestNamespaceCommandUsage(t *testing.T) {
	t.Setenv(adminTokenEnvVar, "test-token")

	var stdout, stderr bytes.Buffer

	usageErr := createNamespace(t.Context(), []string{"--server", "http://127.0.0.1:1"}, &stdout, &stderr)
	if usageErr == nil {
		t.Fatal("namespace create without a name succeeded, want the usage error")
	}
}
