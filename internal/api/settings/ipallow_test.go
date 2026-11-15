package settings_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/api/idempotent"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/api/settings"
	"opendustpan.org/opendustpan/internal/denylimit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/errcode/errcodetest"
	"opendustpan.org/opendustpan/internal/unitsettingssvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/idempotency/idempotencytest"
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
	return identity.Scope{}, errNoList
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
	return identity.Scope{}, errNoList
}

// newAddressHandler returns a handler whose authorizer refuses every check as
// an address outside the IP allowlist of the namespace does.
func newAddressHandler(t *testing.T) *settings.Handler {
	t.Helper()

	return newRefusalHandler(
		t, addressRefused{},
		&recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil}, nil,
	)
}

// newRefusalHandler returns a handler with authz, records and limiter for its
// refusals.
func newRefusalHandler(
	t *testing.T, authz identity.Authorizer, records *recordingAudit, limiter *denylimit.Limiter,
) *settings.Handler {
	t.Helper()

	replay := idempotencytest.NewMemory()

	namespaces, err := namespace.NewService(
		namespace.Config{Store: fakeNamespaces{}, Now: nil, Installation: "test-region"},
	)
	if err != nil {
		t.Fatalf("namespace service: %v", err)
	}

	svc, err := unitsettingssvc.New(unitsettingssvc.Config{
		Store: newMemStore(replay), Fences: fences{}, Connections: connections{}, Pools: pools{},
		Now: func() time.Time { return time.Unix(1_800_000_000, 0) },
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	idem, err := idempotent.New(idempotent.Config{
		Store: replay, Logger: slog.New(slog.DiscardHandler), Now: nil, MaxBodyBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("idempotency middleware: %v", err)
	}

	handler, err := settings.NewHandler(settings.Config{
		Idempotency: idem, Records: replay,
		Namespaces: namespaces, Units: fakeInventory{}, Service: svc, Suggestions: suggester{err: nil},
		Authenticator: tokenAuthenticator{}, Authorizer: authz,
		Audit: records, OnAuditFailure: nil,
		DenialLimiter: limiter, Sessions: nil, Logger: slog.New(slog.DiscardHandler),
	})
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	return handler
}

// TestAddressOutsideTheAllowlistAnswers403 proves that a read of the unit
// settings answers 403 with access.ip-not-allowed for a client address outside
// the IP allowlist, as the other routes of the namespace do, and not the 404
// of a unit that the caller cannot read. The refusal does not tell whether the
// namespace or the unit exists.
func TestAddressOutsideTheAllowlistAnswers403(t *testing.T) {
	t.Parallel()

	const base = "/api/dustpan/v1/namespaces/"

	handler := newAddressHandler(t)

	for _, path := range []string{
		base + "acme/units/" + netID + "/settings",
		base + "acme/units/ws-absent/settings",
		base + "absent/units/" + netID + "/settings",
		base + "acme/units/" + strings.Repeat("x", 101) + "/settings",
	} {
		rec := serve(t, handler, request{method: http.MethodGet, path: path, token: testToken, body: "", header: nil})
		errcodetest.Check(t, rec.Code, rec.Body.Bytes(), errcodetest.Cause{
			Name: "read " + path, Err: nil, Code: errcode.AccessIPNotAllowed, Message: "forbidden", Fixed: true,
		})

		for _, named := range []string{"acme", "absent", netID, "ws-absent"} {
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
		reason  string
		scope   namespace.Name
	}{
		{
			name: "address outside the allowlist", authz: addressRefused{},
			path: base + "acme/units/" + netID + "/settings", status: http.StatusForbidden, entries: 1,
			reason: "", scope: "",
		},
		{
			name: "policy refuses the namespace read", authz: newAuthorizer(),
			path: base + "acme/units/" + netID + "/settings", status: http.StatusNotFound, entries: 1,
			reason: "", scope: "",
		},
		{
			name: "unknown namespace under a policy that refuses everything", authz: newAuthorizer(),
			path: base + "absent/units/" + netID + "/settings", status: http.StatusNotFound, entries: 0,
			reason: "", scope: "",
		},
		{
			name: "allowed request", authz: newAuthorizer(fullAccess()...),
			path: base + "acme/units/" + netID + "/settings", status: http.StatusOK, entries: 0,
			reason: "", scope: "",
		},
		{
			name: "authorizer failure", authz: authorizationFailed{},
			path: base + "acme/units/" + netID + "/settings", status: http.StatusInternalServerError,
			entries: 1, reason: refusal.ReasonFailed, scope: "",
		},
		{
			name: "authorizer failure on an unknown namespace", authz: authorizationFailed{},
			path: base + "absent/units/" + netID + "/settings", status: http.StatusInternalServerError,
			entries: 1, reason: refusal.ReasonFailed, scope: audit.ServerScope,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			store := &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil}
			handler := newRefusalHandler(t, tc.authz, store, writingLimiter())

			rec := serve(t, handler, request{
				method: http.MethodGet, path: tc.path, token: testToken, body: "", header: nil,
			})
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.status, rec.Body.String())
			}

			checkDenialEntries(t, store.denied(), tc.entries, tc.reason, tc.scope)
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

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/dustpan/v1/namespaces/acme/units/"+netID+"/settings", http.NoBody,
	)
	req.SetPathValue("namespace", "acme")

	p, authErr := tokenAuthenticator{}.AuthenticateToken(t.Context(), testToken)
	if authErr != nil {
		t.Fatalf("authentication: %v", authErr)
	}

	err := settings.CheckAddress(t.Context(), handler, req, p)
	if !errors.Is(err, ipallow.ErrAddressNotAllowed) {
		t.Fatalf("error = %v, want an address refusal", err)
	}

	checkDenialEntries(t, store.denied(), 1, "", "")
}

// TestCheckAddressRefusesAnAuthorizerError proves that an authorizer error
// stops the address check and writes one denial entry.
func TestCheckAddressRefusesAnAuthorizerError(t *testing.T) {
	t.Parallel()

	store := &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil}
	handler := newRefusalHandler(t, authorizationFailed{}, store, writingLimiter())

	req := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet, "/api/dustpan/v1/namespaces/acme/units/"+netID+"/settings", http.NoBody,
	)
	req.SetPathValue("namespace", "acme")

	p, authErr := tokenAuthenticator{}.AuthenticateToken(t.Context(), testToken)
	if authErr != nil {
		t.Fatalf("authentication: %v", authErr)
	}

	err := settings.CheckAddress(t.Context(), handler, req, p)
	if !errors.Is(err, errAuthorizationFailed) {
		t.Fatalf("error = %v, want the authorizer error", err)
	}

	checkDenialEntries(t, store.denied(), 1, refusal.ReasonFailed, "")
}

// checkSameAnswer checks that two answers have the same status, body and
// headers.
func checkSameAnswer(t *testing.T, a, b *httptest.ResponseRecorder) {
	t.Helper()

	if a.Code != b.Code {
		t.Fatalf("status = %d, want %d", a.Code, b.Code)
	}

	if !bytes.Equal(a.Body.Bytes(), b.Body.Bytes()) {
		t.Fatalf("body = %q, want %q", a.Body.String(), b.Body.String())
	}

	if !maps.EqualFunc(a.Header(), b.Header(), slices.Equal) {
		t.Fatalf("headers = %v, want %v", a.Header(), b.Header())
	}
}

// TestAuthorizerErrorAnswersTheSameForAnyNamespace proves that an authorizer
// error answers the same 500 to a request for a namespace that exists and to
// one for a namespace that does not exist, so the answer does not tell whether
// the namespace exists.
func TestAuthorizerErrorAnswersTheSameForAnyNamespace(t *testing.T) {
	t.Parallel()

	const base = "/api/dustpan/v1/namespaces/"

	store := &recordingAudit{Store: nil, mu: sync.Mutex{}, entries: nil}
	handler := newRefusalHandler(t, authorizationFailed{}, store, writingLimiter())

	known := serve(t, handler, request{
		method: http.MethodGet, path: base + "acme/units/" + netID + "/settings", token: testToken, body: "",
		header: nil,
	})
	unknown := serve(t, handler, request{
		method: http.MethodGet, path: base + "absent/units/" + netID + "/settings", token: testToken, body: "",
		header: nil,
	})

	checkSameAnswer(t, known, unknown)

	for _, rec := range []*httptest.ResponseRecorder{known, unknown} {
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d: %s", rec.Code, http.StatusInternalServerError, rec.Body.String())
		}
	}
}
