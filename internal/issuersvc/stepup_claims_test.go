package issuersvc_test

import (
	"testing"
	"time"
)

// freshClaimNames are the claim names that say that a person authenticated
// again, or with a factor: the OpenID Connect claims auth_time, amr and acr,
// and the names that a step-up claim could take.
func freshClaimNames() []string {
	return []string{"auth_time", "amr", "acr", "step_up", "stepup", "fresh_auth", "mfa"}
}

// TestIssuedTokenHasNoFreshAuthenticationClaim signs an issued token with the
// open of a job and checks the claims as an independent verifier reads them.
// The issuer never adds a step-up or fresh-authentication claim: the token
// holds none of these names, whatever credential the request of the open
// carried.
func TestIssuedTokenHasNoFreshAuthenticationClaim(t *testing.T) {
	t.Parallel()

	h := newHarness(t)
	o := h.open(h.ns, defaultSubject)
	o.Claims["job"] = "job-1"

	tok, err := h.svc.Login(t.Context(), o, "aws", "sts.amazonaws.com", 5*time.Minute)
	if err != nil {
		t.Fatalf("Login: %v", err)
	}

	claims, err := h.verify(tok.Value, "sts.amazonaws.com")
	if err != nil {
		t.Fatalf("independent verifier refused the token: %v", err)
	}

	for _, name := range freshClaimNames() {
		if _, ok := claims[name]; ok {
			t.Errorf("the issued token holds the claim %q", name)
		}
	}
}
