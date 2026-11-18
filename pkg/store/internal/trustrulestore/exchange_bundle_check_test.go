package trustrulestore_test

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/store/internal/trustrulestore"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// TestCheckExchangeBundle sends hostile bundle data to the two checks of the
// bundle of an exchange, each one as if the service and the adapter had
// failed: CheckExchange before the transaction, and CheckExchangeBundle in it.
func TestCheckExchangeBundle(t *testing.T) {
	t.Parallel()

	id := string(checkBundleID)
	mtls := func(edit func(*trustrule.Exchange)) trustrule.Exchange {
		x := exchangeOfKind(accesstoken.KindMTLSExchange, checkSPIFFEID)
		edit(&x)

		return x
	}
	jwt := func(edit func(*trustrule.Exchange)) trustrule.Exchange {
		x := exchangeOfKind(accesstoken.KindJWTExchange, "")
		edit(&x)

		return x
	}
	row := &trustrulestore.BundleRow{
		ID: id, Namespace: "acme", Active: checkBundleVersion, Revision: 1, CreatedAtMs: 1_700_000_000_000,
	}
	rotated := *row
	rotated.Active = checkBundleVersion + 1
	older := *row
	older.Active = checkBundleVersion - 1
	twin := *row
	twin.ID = "trb-other"

	same := mtls(func(*trustrule.Exchange) {})
	rules := func(bundle string) []trustrulestore.Row {
		r := ruleRow(string(same.Rule), bundle, checkSPIFFEID)

		return []trustrulestore.Row{r, ruleRow(string(trustrule.NewID()), id, "spiffe://example.org/ns/acme/sa/o")}
	}

	cases := []struct {
		name string
		run  func() error
		want error
	}{
		{"its bundle and its active version", func() error {
			return trustrulestore.CheckExchangeBundle(row, rules(id), &same)
		}, nil},
		{"no bundle row", func() error {
			return trustrulestore.CheckExchangeBundle(nil, rules(id), &same)
		}, trustrule.ErrNotFound},
		{"a row of another bundle", func() error {
			return trustrulestore.CheckExchangeBundle(&twin, rules(id), &same)
		}, trustrule.ErrNotFound},
		{"a rule that names another bundle", func() error {
			return trustrulestore.CheckExchangeBundle(row, rules("trb-other"), &same)
		}, trustrule.ErrNotFound},
		{"a rule that names no bundle", func() error {
			return trustrulestore.CheckExchangeBundle(row, rules(""), &same)
		}, trustrule.ErrNotFound},
		{"no row of the rule", func() error {
			return trustrulestore.CheckExchangeBundle(row, nil, &same)
		}, trustrule.ErrNotFound},
		{"a new active version", func() error {
			return trustrulestore.CheckExchangeBundle(&rotated, rules(id), &same)
		}, trustrule.ErrNotFound},
		{"an older active version", func() error {
			return trustrulestore.CheckExchangeBundle(&older, rules(id), &same)
		}, trustrule.ErrNotFound},
		{"a jwt-exchange token with no bundle row", func() error {
			x := jwt(func(*trustrule.Exchange) {})

			return trustrulestore.CheckExchangeBundle(nil, nil, &x)
		}, nil},
		{"no bundle ID for an mtls-exchange token", func() error {
			x := mtls(func(x *trustrule.Exchange) { x.BundleID = "" })

			return trustrulestore.CheckExchange(&x)
		}, trustrule.ErrInvalid},
		{"a bundle ID that is not valid", func() error {
			x := mtls(func(x *trustrule.Exchange) { x.BundleID = "not a bundle" })

			return trustrulestore.CheckExchange(&x)
		}, trustrule.ErrInvalid},
		{"version 0 for an mtls-exchange token", func() error {
			x := mtls(func(x *trustrule.Exchange) { x.BundleVersion = 0 })

			return trustrulestore.CheckExchange(&x)
		}, trustrule.ErrInvalid},
		{"a negative version", func() error {
			x := mtls(func(x *trustrule.Exchange) { x.BundleVersion = -1 })

			return trustrulestore.CheckExchange(&x)
		}, trustrule.ErrInvalid},
		{"a bundle ID on a jwt-exchange token", func() error {
			x := jwt(func(x *trustrule.Exchange) { x.BundleID = checkBundleID })

			return trustrulestore.CheckExchange(&x)
		}, trustrule.ErrInvalid},
		{"a bundle version on a jwt-exchange token", func() error {
			x := jwt(func(x *trustrule.Exchange) { x.BundleVersion = 1 })

			return trustrulestore.CheckExchange(&x)
		}, trustrule.ErrInvalid},
	}

	for _, tc := range cases {
		err := tc.run()
		if tc.want == nil && err != nil || tc.want != nil && !errors.Is(err, tc.want) {
			t.Errorf("%s: error = %v, want %v", tc.name, err, tc.want)
		}
	}
}
