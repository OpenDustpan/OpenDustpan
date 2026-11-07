package client_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli/client"
)

// stubAnswer is the one request that a stub server accepts and the answer it
// gives. A request with another method, path, token or body gets a 500 answer
// that the command passes on as its error, so the test fails. A nil stubAnswer
// means that the command must send no request.
type stubAnswer struct {
	method string
	path   string
	// body is the exact JSON body of the request. Empty skips the check.
	body   string
	status int
	answer string
}

// serveStub starts a server that answers the one request of want.
func serveStub(t *testing.T, want *stubAnswer) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "read request: "+err.Error(), http.StatusInternalServerError)

			return
		}

		switch {
		case want == nil:
			http.Error(w, "unexpected request "+r.Method+" "+r.URL.Path, http.StatusInternalServerError)

			return
		case r.Method != want.method || r.URL.Path != want.path:
			http.Error(w, "unexpected request "+r.Method+" "+r.URL.Path, http.StatusInternalServerError)

			return
		case r.Header.Get("Authorization") != "Bearer test-token":
			http.Error(w, "unexpected authorization", http.StatusInternalServerError)

			return
		case want.body != "" && !bytes.Equal(raw, []byte(want.body)):
			http.Error(w, "unexpected body "+string(raw), http.StatusInternalServerError)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(want.status)

		if _, err := w.Write([]byte(want.answer)); err != nil {
			t.Errorf("write the stub answer: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

// tokenLookup answers the admin token of the tests. It reads no file and no
// other variable, so each test can pass it and run in parallel.
func tokenLookup(key string) (string, bool) {
	if key == client.AdminTokenEnvVar {
		return "test-token", true
	}

	return "", false
}

// withServer returns the flags of a command with --server set to url, then
// the rest of the arguments.
func withServer(url string, rest ...string) []string {
	return append([]string{"--server", url}, rest...)
}

// checkResult fails t unless the output and the error match. An empty wantErr
// means the command must succeed and print wantOut. Otherwise the error must
// contain wantErr.
func checkResult(t *testing.T, out string, err error, wantOut, wantErr string) {
	t.Helper()

	if wantErr == "" {
		if err != nil {
			t.Fatalf("error = %v; want none", err)
		}

		if out != wantOut {
			t.Fatalf("output = %q; want %q", out, wantOut)
		}

		return
	}

	if err == nil || !strings.Contains(err.Error(), wantErr) {
		t.Fatalf("error = %v; want one that contains %q", err, wantErr)
	}
}

// TestInventoryCommandsNeedAdminToken proves that a command without an admin
// token fails before it sends a request. The server URL is never called.
func TestInventoryCommandsNeedAdminToken(t *testing.T) {
	t.Parallel()

	noToken := func(string) (string, bool) { return "", false }

	var stdout, stderr bytes.Buffer

	err := client.CollectionListWithLookup(
		t.Context(), []string{"--server", "https://dustpan.example", "acme"}, noToken, &stdout, &stderr,
	)
	checkResult(t, stdout.String(), err, "", "no admin token is set")
}
