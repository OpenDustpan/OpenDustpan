package trustrules

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

var errUnrelated = errors.New("unrelated")

// TestClassifyMtlsRows checks the status and the code of each refusal of the
// mTLS exchange, with the sentinel wrapped as the service wraps it. The
// table is written out here, one case for each refusal of the three classify
// functions.
func TestClassifyMtlsRows(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err    error
		status int
		code   string
	}{
		{trustrulesvc.ErrNoCertificate, http.StatusUnauthorized, "mtls.no-client-certificate"},
		{trustrulesvc.ErrUnknownIssuer, http.StatusUnauthorized, "mtls.unknown-issuer"},
		{trustrulesvc.ErrInvalid, http.StatusUnauthorized, "mtls.unknown-issuer"},
		{trustrulesvc.ErrExpired, http.StatusUnauthorized, "mtls.expired"},
		{trustrulesvc.ErrNotYetValid, http.StatusUnauthorized, "mtls.not-yet-valid"},
		{trustrulesvc.ErrRevoked, http.StatusUnauthorized, "mtls.revoked"},
		{trustrulesvc.ErrRevocationUnknown, http.StatusUnauthorized, "mtls.revocation-unknown"},
		{trustrulesvc.ErrNoMatchingRule, http.StatusUnauthorized, "mtls.no-matching-rule"},
		{trustrulesvc.ErrAmbiguousRule, http.StatusUnauthorized, "mtls.ambiguous-rule"},
		{trustrulesvc.ErrBundleUnknown, http.StatusUnauthorized, "mtls.bundle-disabled"},
		{
			fmt.Errorf("%w: %w", trustrulesvc.ErrBundleUnknown, trustrule.ErrNotFound),
			http.StatusUnauthorized, "mtls.bundle-disabled",
		},
		{trustrulesvc.ErrRateLimited, http.StatusTooManyRequests, "mtls.rate-limited"},
		{trustrulesvc.ErrBodyTooLarge, http.StatusRequestEntityTooLarge, "request.body-too-large"},
		{trustrule.ErrPrincipal, http.StatusUnauthorized, "mtls.principal-disabled"},
		{trustrule.ErrNotFound, http.StatusUnauthorized, "mtls.no-matching-rule"},
		{trustrulesvc.ErrIssueFailed, http.StatusInternalServerError, "token.issue-failed"},
	}

	for _, c := range cases {
		got, ok := classifyMtls(fmt.Errorf("outer: %w", c.err))
		if !ok || got.Status != c.status || got.Code != c.code {
			t.Errorf("classifyMtls(%v) = %d %s %v, want %d %s", c.err, got.Status, got.Code, ok, c.status, c.code)
		}
	}

	if _, ok := classifyMtls(errUnrelated); ok {
		t.Error("classifyMtls(unrelated) classified: want pass-through, so the writer fails closed with 500")
	}
}

// TestCountMtlsRefusedReason checks the reason that the handler gives to the
// counter: body-size for a body over the cap, rate-limit for the rest.
func TestCountMtlsRefusedReason(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name string
		err  error
		want string
	}{
		{name: "body too large", err: trustrulesvc.ErrBodyTooLarge, want: "body-size"},
		{name: "wrapped body too large", err: fmt.Errorf("x: %w", trustrulesvc.ErrBodyTooLarge), want: "body-size"},
		{name: "rate limited", err: trustrulesvc.ErrRateLimited, want: "rate-limit"},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			var got []string

			h := new(Handler)
			h.onMtlsRefused = func(r string) { got = append(got, r) }
			h.countMtlsRefused(row.err)

			if len(got) != 1 || got[0] != row.want {
				t.Fatalf("reasons = %v, want [%s]", got, row.want)
			}
		})
	}
}
