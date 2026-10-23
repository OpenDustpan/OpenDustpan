package dustpan_test

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/workspace"
)

// TestRefusedTofuRequestCreatesNoWorkspace proves that a POST or a LOCK that
// the server refuses leaves no workspace: the create and the decision on the
// request are one decision. Each case names a workspace that does not exist.
func TestRefusedTofuRequestCreatesNoWorkspace(t *testing.T) {
	t.Parallel()

	const state = `{"version":4,"serial":1,"lineage":"Q","terraform_version":"1.12.7","outputs":{},"resources":[]}`

	cases := []struct {
		name       string
		method     string
		query      string
		body       string
		wantStatus int
		// lockNamespace puts a manual lock on the namespace first.
		lockNamespace bool
	}{
		{"POST with no lock", http.MethodPost, "", state, http.StatusForbidden, false},
		{"POST of a body that is not JSON", http.MethodPost, "", "garbage", http.StatusForbidden, false},
		{"POST that names a lock nobody holds", http.MethodPost, "?ID=x", state, http.StatusConflict, false},
		{"LOCK under a manual lock of the namespace", "LOCK", "", `{"ID":"z"}`, http.StatusLocked, true},
		{"LOCK with a lock ID that is not valid", "LOCK", "", `{"ID":"a b"}`, http.StatusBadRequest, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t)
			f.createNamespace(t, "acme", region)

			if tc.lockNamespace {
				lockNamespace(t, f)
			}

			status, body := tofuDoPath(t, f, tc.method, "/tofu/state/acme/se1"+tc.query, tc.body)
			if status != tc.wantStatus {
				t.Fatalf("%s answers %d (%s), want %d", tc.method, status, body, tc.wantStatus)
			}

			requireNoWorkspace(t, f, "se1")
		})
	}
}

// requireNoWorkspace fails the test when the namespace acme holds a workspace
// called name.
func requireNoWorkspace(t *testing.T, f fixture, name string) {
	t.Helper()

	parsed, err := workspace.ParseName(name)
	if err != nil {
		t.Fatalf("parse name: %v", err)
	}

	_, err = f.srv.Workspaces().Get(t.Context(), workspace.Ref{Namespace: "acme", Name: parsed})
	if !errors.Is(err, workspace.ErrNotFound) {
		t.Fatalf("workspace %s after the refused request: Get = %v, want workspace.ErrNotFound", name, err)
	}
}

// TestFirstTofuLockCreatesTheWorkspace proves that a LOCK that the server
// accepts still creates the workspace on first use.
func TestFirstTofuLockCreatesTheWorkspace(t *testing.T) {
	t.Parallel()

	f := newFixture(t)
	f.createNamespace(t, "acme", region)

	status, body := tofuDoPath(t, f, "LOCK", "/tofu/state/acme/se1", `{"ID":"z"}`)
	if status != http.StatusOK {
		t.Fatalf("LOCK answers %d (%s), want 200", status, body)
	}

	name, err := workspace.ParseName("se1")
	if err != nil {
		t.Fatalf("parse name: %v", err)
	}

	ws, err := f.srv.Workspaces().Get(t.Context(), workspace.Ref{Namespace: "acme", Name: name})
	if err != nil || ws.Lock.ID != "z" {
		t.Fatalf("workspace after the LOCK = %+v, %v; want it locked with ID z", ws, err)
	}
}

// tofuDoPath sends one request to the OpenTofu HTTP backend routes with the
// bearer token of the administrator, and returns the status and the body.
func tofuDoPath(t *testing.T, f fixture, method, path, body string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), method, f.server.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	req.Header.Set("Authorization", "Bearer "+adminToken)

	resp, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}

	data, readErr := io.ReadAll(resp.Body)
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("close body: %v", closeErr)
	}

	if readErr != nil {
		t.Fatalf("read body: %v", readErr)
	}

	return resp.StatusCode, string(data)
}

// lockNamespace puts a manual lock on the namespace acme.
func lockNamespace(t *testing.T, f fixture) {
	t.Helper()

	status, body := f.do(t, http.MethodPut, "/api/dustpan/v1/namespaces/acme/lock", adminToken, `{"reason":"freeze"}`)
	if status != http.StatusOK && status != http.StatusCreated {
		t.Fatalf("lock namespace = %d %s", status, body)
	}
}
