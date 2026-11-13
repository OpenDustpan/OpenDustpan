package drift_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	driftapi "opendustpan.org/opendustpan/internal/api/drift"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
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

// errAuthorizationFailed is the error of an authorizer that cannot decide.
var errAuthorizationFailed = errors.New("authorizer is unavailable")

// authorizationFailed fails every check with an error that is neither a denial
// nor an address refusal.
type authorizationFailed struct{}

func (authorizationFailed) Authorize(context.Context, identity.Principal, identity.Action, identity.Object) error {
	return errAuthorizationFailed
}

func (authorizationFailed) ListObjects(context.Context, identity.Principal, identity.Action) (identity.Scope, error) {
	return identity.Scope{}, errNoListing
}

// newRefusalHandler returns a handler with authz, records and limiter for its
// refusals.
func newRefusalHandler(
	t *testing.T, authz identity.Authorizer, records audit.Store, limiter *denylimit.Limiter,
) *driftapi.Handler {
	t.Helper()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: fakeNamespaces{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	handler, err := driftapi.NewHandler(driftapi.Config{
		Namespaces: namespaces, Units: fakeUnits{liar: false}, Results: newResults(), Checker: newChecker(),
		Workers: nil, Authenticator: tokenAuthenticator{}, Authorizer: authz,
		Audit: records, OnAuditFailure: nil, DenialLimiter: limiter,
		Sessions: nil, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
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
// the outcome, the action, the start of the reason when reason is not empty,
// and the namespace when scope is not empty.
func checkDenialEntries(t *testing.T, got []audit.Entry, want int, reason string, scope namespace.Name) {
	t.Helper()

	if len(got) != want {
		t.Fatalf("wrote %d denial entries, want %d: %+v", len(got), want, got)
	}

	for i := range got {
		if got[i].Outcome != audit.OutcomeDenied || got[i].Action != string(identity.ActionNamespaceRead) {
			t.Errorf("entry %d = %+v, want a denial of %s", i, got[i], identity.ActionNamespaceRead)
		}

		if reason != "" && !strings.HasPrefix(got[i].Reason, reason) {
			t.Errorf("entry %d has reason %q, want the prefix %q", i, got[i].Reason, reason)
		}

		if scope != "" && got[i].Namespace != scope {
			t.Errorf("entry %d has namespace %q, want %q", i, got[i].Namespace, scope)
		}
	}
}

// TestAddressOutsideTheAllowlistAnswers403 proves that the read of the drift
// status and the start of a drift check answer 403 with
// access.ip-not-allowed for a client address outside the IP allowlist, as the
// other routes of the namespace do, and not the 404 of a unit that the caller
// cannot read. No check starts.
func TestAddressOutsideTheAllowlistAnswers403(t *testing.T) {
	t.Parallel()

	r := newRig(t, newAuthorizer(), func(cfg *driftapi.Config) { cfg.Authorizer = addressRefused{} })

	// A unit that exists, a unit that does not exist and a namespace that
	// does not exist all answer the same 403: the refusal does not tell
	// whether the object exists.
	const base = "/api/dustpan/v1/namespaces/"

	queries := make([]request, 0, 8)

	for _, path := range []string{
		base + "acme/units/" + testUnitID,
		base + "acme/units/ws-absent",
		base + "absent/units/" + testUnitID,
		base + "absent/units/ws-absent",
	} {
		queries = append(queries,
			request{method: http.MethodGet, path: path + "/drift", token: testToken, body: "", headers: nil},
			request{method: http.MethodPost, path: path + "/drift-checks", token: testToken, body: "", headers: nil},
		)
	}

	for _, q := range queries {
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

// TestRefusedReadWritesOneDenialEntry proves that one refused read writes one
// denial entry. An address outside the allowlist writes its entry in
// checkAddress. A policy refusal of the namespace read writes its entry in
// AuthorizeObject, and checkAddress writes none. A policy refusal of a
// namespace that does not exist writes nothing, because the lookup answers 404
// and never checks the policy on that name. An authorizer that cannot decide
// is refused and writes one entry with a fixed reason.
func TestRefusedReadWritesOneDenialEntry(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		authz   identity.Authorizer
		path    string
		status  int
		entries int
		reason  string
		scope   namespace.Name
	}{
		{
			name: "address outside the allowlist", authz: addressRefused{},
			path: driftURL, status: http.StatusForbidden, entries: 1, reason: "", scope: "",
		},
		{
			name: "policy refuses the namespace read", authz: newAuthorizer(),
			path: driftURL, status: http.StatusNotFound, entries: 1, reason: "", scope: "",
		},
		{
			name: "unknown namespace under a policy that refuses everything", authz: newAuthorizer(),
			path:   "/api/dustpan/v1/namespaces/absent/units/" + testUnitID + "/drift",
			status: http.StatusNotFound, entries: 0, reason: "", scope: "",
		},
		{
			name: "allowed request", authz: newAuthorizer(holdsAll()...),
			path: driftURL, status: http.StatusOK, entries: 0, reason: "", scope: "",
		},
		{
			name: "authorizer failure", authz: authorizationFailed{},
			path: driftURL, status: http.StatusInternalServerError, entries: 1,
			reason: refusal.ReasonFailed, scope: "",
		},
		{
			name: "authorizer failure on an unknown namespace", authz: authorizationFailed{},
			path:   "/api/dustpan/v1/namespaces/absent/units/" + testUnitID + "/drift",
			status: http.StatusInternalServerError, entries: 1,
			reason: refusal.ReasonFailed, scope: audit.ServerScope,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil}
			handler := newRefusalHandler(t, tc.authz, store, writingLimiter())

			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, tc.path, http.NoBody)
			req.Header.Set("Authorization", "token "+testToken)

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}

			checkDenialEntries(t, store.entries, tc.entries, tc.reason, tc.scope)
		})
	}
}

// TestCheckAddressRefusesAnAddressOutsideTheAllowlist proves that the address
// check alone refuses an address outside the allowlist and writes one denial
// entry, before any other check of the route runs.
func TestCheckAddressRefusesAnAddressOutsideTheAllowlist(t *testing.T) {
	t.Parallel()

	store := &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil}
	handler := newRefusalHandler(t, addressRefused{}, store, writingLimiter())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, driftURL, http.NoBody)
	req.SetPathValue("namespace", "acme")

	p, authErr := tokenAuthenticator{}.AuthenticateToken(t.Context(), testToken)
	if authErr != nil {
		t.Fatalf("authentication: %v", authErr)
	}

	err := driftapi.CheckAddress(t.Context(), handler, req, p)
	if !errors.Is(err, ipallow.ErrAddressNotAllowed) {
		t.Fatalf("error = %v, want an address refusal", err)
	}

	checkDenialEntries(t, store.entries, 1, "", "")
}

// TestCheckAddressRefusesAnAuthorizerError proves that an authorizer error
// stops the address check and writes one denial entry.
func TestCheckAddressRefusesAnAuthorizerError(t *testing.T) {
	t.Parallel()

	store := &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil}
	handler := newRefusalHandler(t, authorizationFailed{}, store, writingLimiter())

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, driftURL, http.NoBody)
	req.SetPathValue("namespace", "acme")

	p, authErr := tokenAuthenticator{}.AuthenticateToken(t.Context(), testToken)
	if authErr != nil {
		t.Fatalf("authentication: %v", authErr)
	}

	err := driftapi.CheckAddress(t.Context(), handler, req, p)
	if !errors.Is(err, errAuthorizationFailed) {
		t.Fatalf("error = %v, want the authorizer error", err)
	}

	checkDenialEntries(t, store.entries, 1, refusal.ReasonFailed, "")
}
