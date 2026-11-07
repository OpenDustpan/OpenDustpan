package dustpan_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/dustpan"
)

// Targets of the sign-in routes with a WebAuthn credential.
const (
	webauthnBeginRoute  = "/api/dustpan/v1/sign-in/passkey/begin"
	webauthnFinishRoute = "/api/dustpan/v1/sign-in/passkey/finish"
)

// newWebauthnSourceServer returns a server with the sign-in with a WebAuthn
// credential whose trusted proxies are in 10.0.0.0/8.
func newWebauthnSourceServer(t *testing.T) http.Handler {
	t.Helper()

	st, blobs, authn := requireServerPorts(t)
	opts := append(fullServerOptions(t, st, blobs, authn),
		dustpan.WithPublicURL(localPublicURL),
		dustpan.WithTrustedProxies(netip.MustParsePrefix("10.0.0.0/8")),
	)

	srv, err := dustpan.NewServer(opts...)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}

	return startedHandler(t, srv)
}

// webauthnPost sends a POST from the peer remote with the X-Forwarded-For
// value forwarded, which may be empty.
func webauthnPost(t *testing.T, h http.Handler, target, remote, forwarded string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, target, strings.NewReader("{}"))
	req.RemoteAddr = remote
	req.Header.Set("Content-Type", "application/json")

	if forwarded != "" {
		req.Header.Set("X-Forwarded-For", forwarded)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

// errorCodeOf returns the error code of the JSON answer rec, or "".
func errorCodeOf(rec *httptest.ResponseRecorder) string {
	var doc struct {
		ErrorCode string `json:"errorCode"`
	}

	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		return ""
	}

	return doc.ErrorCode
}

// budgetCase is a route of the sign-in with a WebAuthn credential and the
// requests that spend its budget. A refusal with a code in allowed does not
// end the spend.
type budgetCase struct {
	name    string
	target  string
	allowed []string
	limit   int
}

// spendBudget sends the requests of tc from the peer untrusted, each with a new
// X-Forwarded-For value. It fails the test if the budget refuses one of them,
// except with a code in tc.allowed.
func spendBudget(t *testing.T, h http.Handler, tc *budgetCase, untrusted string) {
	t.Helper()

	for i := range tc.limit {
		rec := webauthnPost(t, h, tc.target, untrusted, "203.0.113."+strconv.Itoa(i+1))
		if rec.Code == http.StatusTooManyRequests && !slices.Contains(tc.allowed, errorCodeOf(rec)) {
			t.Fatalf("request %d was refused before the budget was spent: %s", i+1, rec.Body)
		}
	}
}

// TestWebauthnGatesUseTheResolvedSourceAddress sends requests from a peer that
// is not a trusted proxy, with a new X-Forwarded-For value on each request, to
// both routes of the sign-in with a WebAuthn credential through the whole
// server. The budget of the real address must refuse the peer, whatever the
// header says: a header from a peer that is not trusted is a value that the
// client chose. A client behind a trusted proxy has a budget of its own.
func TestWebauthnGatesUseTheResolvedSourceAddress(t *testing.T) {
	t.Parallel()

	const untrusted = "198.51.100.7:4000"

	for _, tc := range []budgetCase{
		// 20 failed responses refuse the address on the next request.
		{"failed responses", webauthnFinishRoute, nil, 20},
		// A start spends a unit of a budget of 30, also when the count of open
		// challenges of the address (20) refuses it. So 20 starts work, the
		// next 10 are refused by that count and spend the budget, and the
		// start after them is refused by the budget of starts.
		{"starts", webauthnBeginRoute, []string{errcode.MFASignInSourceLimit}, 30},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := newWebauthnSourceServer(t)

			spendBudget(t, h, &tc, untrusted)

			rec := webauthnPost(t, h, tc.target, untrusted, "203.0.113.200")
			if rec.Code != http.StatusTooManyRequests || errorCodeOf(rec) != errcode.AuthRateLimited {
				t.Fatalf("request over the budget with a new header: status = %d, code %q, want 429 %s: %s",
					rec.Code, errorCodeOf(rec), errcode.AuthRateLimited, rec.Body)
			}

			for _, other := range []struct{ name, remote, forwarded string }{
				{"another address", "198.51.100.8:4000", "203.0.113.1"},
				{"a client behind a trusted proxy", "10.1.2.3:4000", "192.0.2.77"},
			} {
				got := webauthnPost(t, h, tc.target, other.remote, other.forwarded)
				if got.Code == http.StatusTooManyRequests {
					t.Fatalf("%s was refused: %s", other.name, got.Body)
				}
			}
		})
	}
}
