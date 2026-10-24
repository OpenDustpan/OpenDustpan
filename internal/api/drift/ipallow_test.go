package drift_test

import (
	"context"
	"errors"
	"net/http"
	"testing"

	driftapi "opendustpan.org/opendustpan/internal/api/drift"
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

// TestAddressOutsideTheAllowlistAnswers403 proves that the read of the drift
// status and the start of a drift check answer 403 with
// access.ip-not-allowed for a client address outside the IP allowlist, as the
// other routes of the namespace do, and not the 404 of a unit that the caller
// cannot read. No check starts.
func TestAddressOutsideTheAllowlistAnswers403(t *testing.T) {
	t.Parallel()

	r := newRig(t, newAuthorizer(), func(cfg *driftapi.Config) { cfg.Authorizer = addressRefused{} })

	for _, q := range []request{
		{method: http.MethodGet, path: driftURL, token: testToken, body: "", headers: nil},
		{method: http.MethodPost, path: checksURL, token: testToken, body: "", headers: nil},
	} {
		rec := r.serve(t, q)
		errcodetest.Check(t, rec.Code, rec.Body.Bytes(), errcodetest.Cause{
			Name: q.method + " " + q.path, Err: nil, Code: errcode.AccessIPNotAllowed, Message: "forbidden",
			Fixed: true,
		})
	}

	if r.checker.calls() != 0 {
		t.Fatalf("the checker got %d calls, want none", r.checker.calls())
	}
}
