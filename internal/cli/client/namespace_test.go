package client_test

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli/client"
)

// serveNamespaceCreate answers a namespace create with status and body.
func serveNamespaceCreate(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)

		_, _ = w.Write([]byte(body)) //nolint:errcheck // The client may close early on an oversized body.
	}))
	t.Cleanup(srv.Close)

	return srv
}

// createNamespace runs namespace create against srv and returns its output
// and error.
func createNamespace(t *testing.T, srv *httptest.Server) (string, error) {
	t.Helper()

	t.Setenv("DUSTPAN_ADMIN_TOKEN", "test-token")

	var stdout, stderr bytes.Buffer

	err := client.CreateNamespace(t.Context(), []string{"--server", srv.URL, "acme"}, &stdout, &stderr)

	return stdout.String(), err
}

// TestCreateNamespacePrintsTheNamespace proves the output of a created
// namespace. The test sets the admin token, so it cannot run in parallel.
//
//nolint:paralleltest // The test sets the admin token, so it cannot run in parallel.
func TestCreateNamespacePrintsTheNamespace(t *testing.T) {
	srv := serveNamespaceCreate(
		t,
		http.StatusCreated,
		`{"name":"acme","primaryInstallation":"local","failoverVersion":1}`,
	)

	out, err := createNamespace(t, srv)
	if err != nil || !strings.Contains(out, "created namespace acme, primary installation local") {
		t.Fatalf("namespace create = %q, %v; want the created namespace", out, err)
	}
}

// TestCreateNamespaceReadsTheAnswerWithACap proves the cap of the answer of a
// namespace create: a success body is read up to MaxClientBodyBytes and a
// larger one is refused, and an error body larger than the cap answers the
// status alone. The test sets the admin token, so it cannot run in parallel.
//
//nolint:paralleltest // The test sets the admin token, so it cannot run in parallel.
func TestCreateNamespaceReadsTheAnswerWithACap(t *testing.T) {
	const head = `{"name":"acme","primaryInstallation":"local","pad":"`

	// padTo makes a success body of exactly size bytes.
	padTo := func(size int) string {
		return head + strings.Repeat("x", size-len(head)-len(`"}`)) + `"}`
	}

	tests := []struct {
		name       string
		status     int
		body       string
		wantOutput string
		wantErrs   []error
		wantText   string
		avoidText  string
	}{
		{
			name:       "body of exactly the cap",
			status:     http.StatusCreated,
			body:       padTo(client.MaxClientBodyBytes),
			wantOutput: "created namespace acme, primary installation local",
			wantErrs:   nil,
			wantText:   "",
			avoidText:  "",
		},
		{
			name:       "body of the cap plus one byte",
			status:     http.StatusCreated,
			body:       padTo(client.MaxClientBodyBytes + 1),
			wantOutput: "",
			wantErrs:   []error{client.ErrNamespaceCreate, client.ErrResponseTooLarge},
			wantText:   "the server created the namespace: check the server log",
			avoidText:  "",
		},
		{
			name: "error body above the cap", status: http.StatusConflict,
			body:       `{"message":"` + strings.Repeat("y", client.MaxClientBodyBytes) + `"}`,
			wantOutput: "", wantErrs: []error{client.ErrNamespaceCreate}, wantText: "409 Conflict", avoidText: "yyyy",
		},
		{
			name:       "error body with a message",
			status:     http.StatusConflict,
			body:       `{"message":"namespace exists"}`,
			wantOutput: "",
			wantErrs:   []error{client.ErrNamespaceCreate},
			wantText:   "409 Conflict: namespace exists",
			avoidText:  "",
		},
		{
			name:       "error body that is not JSON",
			status:     http.StatusBadGateway,
			body:       "upstream down",
			wantOutput: "",
			wantErrs:   []error{client.ErrNamespaceCreate},
			wantText:   "502 Bad Gateway: upstream down",
			avoidText:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := createNamespace(t, serveNamespaceCreate(t, tt.status, tt.body))

			if len(tt.wantErrs) == 0 {
				if err != nil || !strings.Contains(out, tt.wantOutput) {
					t.Fatalf("namespace create = %q, %v; want %q", out, err, tt.wantOutput)
				}

				return
			}

			if msg := checkNamespaceError(err, tt.wantErrs, tt.wantText, tt.avoidText); msg != "" {
				t.Fatal(msg)
			}
		})
	}
}

// checkNamespaceError answers what is wrong with err, or an empty text: err
// wraps each of wantErrs, holds wantText once, holds no avoidText, and says
// its advice once.
func checkNamespaceError(err error, wantErrs []error, wantText, avoidText string) string {
	if err == nil {
		return "namespace create = nil; want an error"
	}

	for _, want := range wantErrs {
		if !errors.Is(err, want) {
			return fmt.Sprintf("err = %.200v; want it to wrap %v", err, want)
		}
	}

	text := err.Error()

	switch {
	case !strings.Contains(text, wantText):
		return fmt.Sprintf("err = %.300v; want it to contain %q", err, wantText)
	case avoidText != "" && strings.Contains(text, avoidText):
		return fmt.Sprintf("err holds the oversized body: %.200v", err)
	case strings.Count(text, "check the server log") > 1:
		return fmt.Sprintf("err repeats its advice: %.300v", err)
	}

	return ""
}
