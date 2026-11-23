package server_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc/apidesctest"
	apiserver "opendustpan.org/opendustpan/internal/api/server"
	"opendustpan.org/opendustpan/pkg/secret"
)

// testHandler is the handler of the adapter with the check of the success
// statuses on its answers: a test fails when a handler answers a success
// status that the description does not give.
type testHandler struct {
	*apiserver.Handler

	served http.Handler
}

// ServeHTTP serves with the check.
func (h testHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.served.ServeHTTP(w, r) }

// newCheckedHandler is NewHandler with the check of the success statuses.
func newCheckedHandler(t *testing.T, cfg *apiserver.Config) (testHandler, error) {
	t.Helper()

	h, err := apiserver.NewHandler(cfg)
	if err != nil {
		return testHandler{Handler: nil, served: nil}, err
	}

	return testHandler{Handler: h, served: apidesctest.CheckStatus(t, h)}, nil
}

// testCursors returns the wrapper that seals the cursor of a page in a test.
func testCursors(t *testing.T) *secret.BuiltinWrapper {
	t.Helper()

	wrapper, err := secret.NewBuiltinWrapper([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("cursor wrapper: %v", err)
	}

	return wrapper
}
