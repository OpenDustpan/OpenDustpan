package trustrulestore_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/store/internal/trustrulestore"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

const checkSPIFFEID = "spiffe://example.org/ns/acme/sa/deploy"

// exchangeOfKind returns an exchange of a token of kind with spiffeID.
func exchangeOfKind(kind accesstoken.Kind, spiffeID string) trustrule.Exchange {
	at := time.UnixMilli(1_700_000_000_000).UTC()

	return trustrule.Exchange{
		Token: accesstoken.Token{
			CreatedAt: at, ExpiresAt: at.Add(time.Hour), LastUsedAt: time.Time{}, RevokedAt: time.Time{},
			DisabledAt: time.Time{}, ID: "tok-1", PrincipalID: "prn-worker", Name: "trust rule", Kind: kind,
			ReplacedBy: "", CreatedBy: "trust-rule",
		},
		Digest: accesstoken.Digest{}, Rule: trustrule.NewID(), SPIFFEID: spiffeID,
	}
}

// TestCheckExchangeKinds sends each kind of token, with and without a SPIFFE
// ID, to the check before the transaction of an exchange.
func TestCheckExchangeKinds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		kind     accesstoken.Kind
		spiffeID string
		ok       bool
	}{
		{"a jwt-exchange token", accesstoken.KindJWTExchange, "", true},
		{"an mtls-exchange token with its SPIFFE ID", accesstoken.KindMTLSExchange, checkSPIFFEID, true},
		{"an mtls-exchange token with no SPIFFE ID", accesstoken.KindMTLSExchange, "", false},
		{
			"an mtls-exchange token with a * in the SPIFFE ID", accesstoken.KindMTLSExchange,
			"spiffe://example.org/ns/acme/*", false,
		},
		{"a jwt-exchange token with a SPIFFE ID", accesstoken.KindJWTExchange, checkSPIFFEID, false},
		{"a service token", accesstoken.KindService, "", false},
		{"a personal token", accesstoken.KindPersonal, "", false},
		{"a kind of another version", accesstoken.Kind("x509-exchange"), checkSPIFFEID, false},
	}

	for _, tc := range cases {
		x := exchangeOfKind(tc.kind, tc.spiffeID)

		err := trustrulestore.CheckExchange(&x)
		if tc.ok != (err == nil) || (!tc.ok && !errors.Is(err, trustrule.ErrInvalid)) {
			t.Errorf("%s: CheckExchange = %v, want ok %v", tc.name, err, tc.ok)
		}
	}
}

// ruleRow returns a rule row of acme with the given ID, bundle ID and SPIFFE
// ID pattern.
func ruleRow(id, bundle, pattern string) trustrulestore.Row {
	return trustrulestore.Row{
		ID: id, Namespace: "acme", Name: "deploy " + id, Issuer: "https://token.actions.example.com",
		Audience: "https://dustpan.example.com", KeySource: "discovery", StaticKeys: "", ConditionsJSON: "[]",
		PrincipalID: "prn-worker", CreatedBy: "ada", CreatedAtMs: 1_700_000_000_000, Revision: 1,
		SpiffeIDPattern: pattern, BundleID: bundle,
	}
}

// TestCheckExchangeMatch sends rule rows to the match in the transaction of
// an mTLS exchange, as if the service had matched other rows.
func TestCheckExchangeMatch(t *testing.T) {
	t.Parallel()

	x := exchangeOfKind(accesstoken.KindMTLSExchange, checkSPIFFEID)
	own := ruleRow(string(x.Rule), "trb-1", checkSPIFFEID)
	twin := ruleRow(string(trustrule.NewID()), "trb-1", checkSPIFFEID)
	other := ruleRow(string(trustrule.NewID()), "trb-1", "spiffe://example.org/ns/acme/sa/other")
	wildcard := own
	wildcard.SpiffeIDPattern = "spiffe://example.org/ns/acme/sa/*"
	noBundle := own
	noBundle.BundleID = ""

	cases := []struct {
		name string
		rows []trustrulestore.Row
		ok   bool
	}{
		{"its rule only", []trustrulestore.Row{own, other}, true},
		{"a late duplicate", []trustrulestore.Row{own, twin}, false},
		{"another rule only", []trustrulestore.Row{twin}, false},
		{"no rule", nil, false},
		{"its rule with a /* pattern", []trustrulestore.Row{wildcard}, false},
		{"its rule with no bundle", []trustrulestore.Row{noBundle}, false},
	}

	for _, tc := range cases {
		err := trustrulestore.CheckExchangeMatch(tc.rows, &x)
		if tc.ok != (err == nil) || (!tc.ok && !errors.Is(err, trustrule.ErrNotFound)) {
			t.Errorf("%s: CheckExchangeMatch = %v, want ok %v", tc.name, err, tc.ok)
		}
	}

	jwt := exchangeOfKind(accesstoken.KindJWTExchange, "")
	if err := trustrulestore.CheckExchangeMatch(nil, &jwt); err != nil {
		t.Errorf("CheckExchangeMatch of a jwt-exchange = %v, want nil", err)
	}
}
