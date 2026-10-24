package runnerapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
)

// addressRefused refuses every check as the IP allowlist gate does for a
// client address outside the allowlist of the namespace.
type addressRefused struct{}

func (addressRefused) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return errors.Join(identity.ErrDenied, ipallow.ErrAddressNotAllowed)
}

func (addressRefused) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{}, errNoListing
}

// TestAddressOutsideTheAllowlistAnswers403 proves that a runner management
// route answers 403 with access.ip-not-allowed for a client address outside
// the IP allowlist, as the other routes of the namespace do, and not the 404
// of a namespace that the caller cannot read.
func TestAddressOutsideTheAllowlistAnswers403(t *testing.T) {
	t.Parallel()

	for _, q := range []struct{ method, path, body string }{
		{method: http.MethodGet, path: "/api/runners/acme/pools", body: ""},
		{method: http.MethodPost, path: "/api/runners/acme/pools", body: `{"name":"p1","purpose":"jobs"}`},
		// A pool that does not exist and a namespace that does not exist answer
		// the same 403: the refusal does not tell whether the object exists.
		{method: http.MethodGet, path: "/api/runners/acme/pools/absent", body: ""},
		{method: http.MethodGet, path: "/api/runners/absent/pools", body: ""},
		{method: http.MethodPost, path: "/api/runners/absent/pools", body: `{"name":"p1","purpose":"jobs"}`},
		{method: http.MethodGet, path: "/api/runners/absent/pools/absent", body: ""},
	} {
		req := httptest.NewRequestWithContext(t.Context(), q.method, q.path, strings.NewReader(q.body))
		req.Header.Set("Authorization", "token "+adminToken)

		rec := httptest.NewRecorder()
		handlerAuthorizing(t, addressRefused{}).ServeHTTP(rec, req)

		errcodetest.Check(t, rec.Code, rec.Body.Bytes(), errcodetest.Cause{
			Name: q.method + " " + q.path, Err: nil, Code: errcode.AccessIPNotAllowed, Message: "forbidden",
			Fixed: true,
		})
	}
}
