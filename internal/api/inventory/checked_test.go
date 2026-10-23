package inventory_test

import (
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/api/apidesc/apidesctest"
	apiinventory "opendustpan.org/opendustpan/internal/api/inventory"
)

// testHandler is the handler of the adapter with the check of the success
// statuses on its answers: a test fails when a handler answers a success
// status that the description does not give.
type testHandler struct {
	*apiinventory.Handler

	served http.Handler
}

// ServeHTTP serves with the check.
func (h testHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.served.ServeHTTP(w, r) }

// newCheckedHandler is NewHandler with the check of the success statuses.
func newCheckedHandler(t *testing.T, cfg apiinventory.Config) (testHandler, error) {
	t.Helper()

	h, err := apiinventory.NewHandler(cfg)
	if err != nil {
		return testHandler{Handler: nil, served: nil}, err
	}

	return testHandler{Handler: h, served: apidesctest.CheckStatus(t, h)}, nil
}
