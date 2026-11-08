package issuersvc

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/profile"
)

// TestPayloadRefusesAuthenticationClaims sends the claims of an open with an
// authentication claim straight to payload, as if checkRequest had not run.
// payload must refuse each one and build no claims.
func TestPayloadRefusesAuthenticationClaims(t *testing.T) {
	t.Parallel()

	r := signRequest{purpose: purposeLogin, block: "aws", aud: "sts.amazonaws.com", body: nil, lifetime: time.Minute}
	now := time.Unix(1_700_000_000, 0)

	for _, name := range profile.AuthenticationClaims() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var o profile.OpenContext

			o.Claims = map[string]any{"access": "write", name: "x"}
			o.Namespace, o.Subject = "acme", "s"

			raw, err := payload(o, r, "https://issuer.example", "jti-1", now)
			if !errors.Is(err, profile.ErrInvalid) || raw != nil {
				t.Fatalf("payload with the claim %s = %q, %v; want no claims and ErrInvalid", name, raw, err)
			}
		})
	}
}
