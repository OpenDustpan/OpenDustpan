package objectcontext_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/objectcontext"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/audit"
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

// writingLimiter returns a limiter whose window ends between two refusals, so
// that each refusal writes one entry and the test can count them.
func writingLimiter() *denylimit.Limiter {
	var mu sync.Mutex

	now := time.Unix(1_800_000_000, 0)

	return denylimit.New(denylimit.Config{
		Window: time.Second, MaxKeys: 0, MaxFlush: 0, MaxKeysPerPrincipal: 0,
		Now: func() time.Time {
			mu.Lock()
			defer mu.Unlock()

			now = now.Add(2 * time.Second)

			return now
		},
	})
}

// checkDenialEntries checks the denial entries of one request: the count, and
// the outcome and the action.
func checkDenialEntries(t *testing.T, got []audit.Entry, want int) {
	t.Helper()

	if len(got) != want {
		t.Fatalf("wrote %d denial entries, want %d: %+v", len(got), want, got)
	}

	for i := range got {
		if got[i].Outcome != audit.OutcomeDenied || got[i].Action != string(identity.ActionNamespaceRead) {
			t.Errorf("entry %d = %+v, want a denial of %s", i, got[i], identity.ActionNamespaceRead)
		}
	}
}

// TestRefusedReadWritesOneDenialEntry proves that one refused read writes one
// denial entry. An address outside the allowlist writes its entry in
// checkAddress. A policy refusal of the namespace read writes its entry in
// AuthorizeObject, and checkAddress writes none. A policy refusal of a
// namespace that does not exist writes nothing, because the lookup answers 404
// and never checks the policy on that name.
func TestRefusedReadWritesOneDenialEntry(t *testing.T) {
	t.Parallel()

	const base = "/api/dustpan/v1/namespaces/"

	cases := []struct {
		name    string
		authz   identity.Authorizer
		path    string
		status  int
		entries int
	}{
		{
			name: "address outside the allowlist", authz: addressRefused{},
			path: base + "acme/units/" + testUnitID + "/context", status: http.StatusForbidden, entries: 1,
		},
		{
			name: "policy refuses the namespace read", authz: newAuthorizer(),
			path: base + "acme/units/" + testUnitID + "/context", status: http.StatusNotFound, entries: 1,
		},
		{
			name: "unknown namespace under a policy that refuses everything", authz: newAuthorizer(),
			path: base + "absent/units/" + testUnitID + "/context", status: http.StatusNotFound, entries: 0,
		},
		{
			name: "allowed request",
			authz: newAuthorizer(
				"namespace:read acme", "unit:read opentofu:acme/net",
			),
			path: base + "acme/units/" + testUnitID + "/context", status: http.StatusOK, entries: 0,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := newTestAudit()
			handler := newHandlerWithGuard(
				t, tc.authz, newFakeStore(), nil,
				guardOptions{records: store, limiter: writingLimiter()},
			)

			rec := serve(t, handler, request{
				method: http.MethodGet, path: tc.path, token: testToken, body: "",
			})
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}

			checkDenialEntries(t, store.denied(), tc.entries)
		})
	}
}

// TestCheckAddressRefusesAnAddressOutsideTheAllowlist proves that the address
// check alone refuses an address outside the allowlist and writes one denial
// entry, before any other check of the route runs.
func TestCheckAddressRefusesAnAddressOutsideTheAllowlist(t *testing.T) {
	t.Parallel()

	store := newTestAudit()
	handler := newHandlerWithGuard(
		t, addressRefused{}, newFakeStore(), nil,
		guardOptions{records: store, limiter: writingLimiter()},
	)

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/dustpan/v1/namespaces/acme/units/"+testUnitID+"/context", http.NoBody,
	)
	req.SetPathValue("namespace", "acme")

	p, authErr := tokenAuthenticator{}.AuthenticateToken(t.Context(), testToken)
	if authErr != nil {
		t.Fatalf("authentication: %v", authErr)
	}

	err := objectcontext.CheckAddress(t.Context(), handler.Handler, req, p)
	if !errors.Is(err, ipallow.ErrAddressNotAllowed) {
		t.Fatalf("error = %v, want an address refusal", err)
	}

	checkDenialEntries(t, store.denied(), 1)
}
