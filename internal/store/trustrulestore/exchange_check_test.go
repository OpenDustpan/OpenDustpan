package trustrulestore_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/store/trustrulestore"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

const checkSPIFFEID = "spiffe://example.org/ns/acme/sa/deploy"

// checkBundleID and checkBundleVersion are the verified bundle of the
// exchanges of these tests.
const (
	checkBundleID      trustrule.BundleID = "trb-0123456789abcdef0123456789abcdef"
	checkBundleVersion int64              = 3
)

// exchangeOfKind returns an exchange of a token of kind with spiffeID.
func exchangeOfKind(kind accesstoken.Kind, spiffeID string) trustrule.Exchange {
	at := time.UnixMilli(1_700_000_000_000).UTC()

	var bundle trustrule.BundleID

	var version int64

	if kind == accesstoken.KindMTLSExchange {
		bundle, version = checkBundleID, checkBundleVersion
	}

	return trustrule.Exchange{
		Token: accesstoken.Token{
			CreatedAt: at, ExpiresAt: at.Add(time.Hour), LastUsedAt: time.Time{}, RevokedAt: time.Time{},
			DisabledAt: time.Time{}, ID: "tok-1", PrincipalID: "prn-worker", Name: "trust rule", Kind: kind,
			ReplacedBy: "", CreatedBy: "trust-rule",
		},
		Digest: accesstoken.Digest{}, Rule: trustrule.NewID(), SPIFFEID: spiffeID,
		BundleID: bundle, BundleVersion: version,
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

// matchRow returns the narrow row that the match query returns.
func matchRow(id, bundle, pattern string) trustrulestore.MatchRow {
	return trustrulestore.MatchRow{ID: id, BundleID: bundle, SpiffeIDPattern: pattern}
}

// matchOfRow returns the narrow form of a rule row.
func matchOfRow(r trustrulestore.Row) trustrulestore.MatchRow {
	return matchRow(r.ID, r.BundleID, r.SpiffeIDPattern)
}

// zeroRow returns a rule row with no value.
func zeroRow() trustrulestore.Row {
	return trustrulestore.Row{
		ID: "", Namespace: "", Name: "", Issuer: "", Audience: "", KeySource: "", StaticKeys: "",
		ConditionsJSON: "", PrincipalID: "", CreatedBy: "", CreatedAtMs: 0, Revision: 0,
		SpiffeIDPattern: "", BundleID: "",
	}
}

// TestCheckExchangeMatchRows sends hostile rows to the match in the
// transaction of an mTLS exchange, as if the query, the index and the adapter
// had failed. The match reads only the rows that name the SPIFFE ID in the
// bundle of the rule of the exchange.
func TestCheckExchangeMatchRows(t *testing.T) {
	t.Parallel()

	x := exchangeOfKind(accesstoken.KindMTLSExchange, checkSPIFFEID)
	own := ruleRow(string(x.Rule), "trb-1", checkSPIFFEID)
	otherID := string(trustrule.NewID())
	twin := ruleRow(otherID, "trb-1", checkSPIFFEID)
	otherBundle := ruleRow(otherID, "trb-2", checkSPIFFEID)
	otherPattern := ruleRow(otherID, "trb-1", "spiffe://example.org/ns/acme/sa/other")
	wildcard := own
	wildcard.SpiffeIDPattern = "spiffe://example.org/ns/acme/sa/*"
	noBundle := own
	noBundle.BundleID = ""
	emptyID := own
	emptyID.ID = ""
	otherRule := ruleRow(otherID, "trb-1", checkSPIFFEID)

	otherRows := make([]trustrulestore.MatchRow, 0, 1001)
	otherRows = append(otherRows, matchOfRow(own))
	for i := range 1000 {
		otherRows = append(otherRows, trustrulestore.MatchRow{
			ID:       "trb-1-other-" + string(rune('a'+i%26)),
			BundleID: "trb-other",
			SpiffeIDPattern: "spiffe://example.org/ns/acme/sa/other-" +
				string(rune('a'+i%26)),
		})
	}

	rows := func(m ...trustrulestore.MatchRow) []trustrulestore.MatchRow { return m }
	differsByCase := "spiffe://example.org/ns/acme/sa/Deploy"
	percent := "spiffe://example.org/ns/acme/sa/%"

	cases := []struct {
		name    string
		rule    trustrulestore.Row
		matches []trustrulestore.MatchRow
		ok      bool
	}{
		{"no matches at all", own, nil, false},
		{"the rule row with another ID than the exchange rule", otherRule, rows(matchOfRow(otherRule)), false},
		{"the rule row with an empty bundle ID", noBundle, rows(matchOfRow(noBundle)), false},
		{
			"the rule row with a pattern that is not the SPIFFE ID of the exchange",
			otherPattern, rows(matchOfRow(otherPattern)), false,
		},
		{"one match that is the rule", own, rows(matchOfRow(own)), true},
		{"one match that is another rule", own, rows(matchOfRow(twin)), false},
		{
			"two matches the rule and a twin of the same bundle",
			own, rows(matchOfRow(own), matchOfRow(twin)), false,
		},
		{
			"a twin of another bundle does not count",
			own, rows(matchOfRow(own), matchOfRow(otherBundle)), true,
		},
		{
			"a twin with an empty bundle ID does not count",
			own, rows(matchOfRow(own), matchRow(twin.ID, "", checkSPIFFEID)), true,
		},
		{
			"a row with a wildcard pattern does not count",
			own, rows(matchOfRow(own), matchOfRow(wildcard)), true,
		},
		{
			"a pattern that differs by case does not count",
			own, rows(matchOfRow(own), matchRow(twin.ID, "trb-1", differsByCase)), true,
		},
		{
			"a pattern that differs by a trailing slash does not count",
			own, rows(matchOfRow(own), matchRow(twin.ID, "trb-1", checkSPIFFEID+"/")), true,
		},
		{
			"a pattern that differs by a trailing space does not count",
			own, rows(matchOfRow(own), matchRow(twin.ID, "trb-1", checkSPIFFEID+" ")), true,
		},
		{
			"a pattern that differs by a % does not count",
			own, rows(matchOfRow(own), matchRow(twin.ID, "trb-1", percent)), true,
		},
		{"a row with an empty ID does not count as the rule", own, rows(matchOfRow(emptyID)), false},
		{
			"matches of 1000 rows of other bundles and patterns and the one match still counts",
			own, otherRows, true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := trustrulestore.CheckExchangeMatch(tc.rule, tc.matches, &x)
			if tc.ok != (err == nil) || (!tc.ok && !errors.Is(err, trustrule.ErrNotFound)) {
				t.Errorf("CheckExchangeMatch = %v, want ok %v", err, tc.ok)
			}
		})
	}

	jwt := exchangeOfKind(accesstoken.KindJWTExchange, "")
	if err := trustrulestore.CheckExchangeMatch(own, nil, &jwt); err != nil {
		t.Errorf("CheckExchangeMatch of a jwt-exchange = %v, want nil", err)
	}
}
