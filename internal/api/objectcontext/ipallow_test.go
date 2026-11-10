package objectcontext_test

import (
	"context"
	"errors"
	"net/http"
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

// TestAddressOutsideTheAllowlistAnswers403 proves that a context read answers
// 403 with access.ip-not-allowed for a client address outside the IP
// allowlist, as the other routes of the namespace do, and not the 404 of an
// object that the caller cannot read. The refusal does not tell whether the
// namespace or the unit exists.
func TestAddressOutsideTheAllowlistAnswers403(t *testing.T) {
	t.Parallel()

	const base = "/api/dustpan/v1/namespaces/"

	handler := newHandler(t, addressRefused{}, newFakeStore())

	for _, path := range []string{
		base + "acme/units/" + testUnitID + "/context",
		base + "acme/units/ws-absent/context",
		base + "absent/units/" + testUnitID + "/context",
		base + "acme/units/" + strings.Repeat("x", 101) + "/context",
	} {
		rec := serve(t, handler, request{method: http.MethodGet, path: path, token: testToken, body: ""})
		errcodetest.Check(t, rec.Code, rec.Body.Bytes(), errcodetest.Cause{
			Name: "read " + path, Err: nil, Code: errcode.AccessIPNotAllowed, Message: "forbidden", Fixed: true,
		})

		for _, named := range []string{"acme", "absent", testUnitID, "ws-absent"} {
			if strings.Contains(rec.Body.String(), named) {
				t.Errorf("read %s: the refusal names %q: %s", path, named, rec.Body.String())
			}
		}
	}
}
