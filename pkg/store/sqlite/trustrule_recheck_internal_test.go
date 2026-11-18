package sqlite

// Test of the unexported recheck of an exchange. The file name keeps the
// white-box package under the testpackage exception in .golangci.yml. The
// recheck refuses a kind before it reads a row, so the test needs no database.

import (
	"errors"
	"testing"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// TestRecheckExchangeRefusesAnUnknownKind sends an exchange of a kind that no
// exchange makes directly to the recheck, as if CheckExchange had failed to
// refuse it. The recheck must fail closed and read no row.
func TestRecheckExchangeRefusesAnUnknownKind(t *testing.T) {
	t.Parallel()

	kinds := []accesstoken.Kind{accesstoken.KindPersonal, accesstoken.KindService, "other", ""}
	for _, kind := range kinds {
		var tok accesstoken.Token
		tok.Kind = kind

		x := trustrule.Exchange{
			Rule:          "",
			Token:         tok,
			Digest:        accesstoken.Digest{},
			SPIFFEID:      "",
			BundleID:      "",
			BundleVersion: 0,
		}

		err := recheckExchange(t.Context(), nil, namespace.Name("acme"), &x)
		if !errors.Is(err, trustrule.ErrInvalid) {
			t.Errorf("kind %q: error = %v, want ErrInvalid", kind, err)
		}
	}
}

// TestRecheckExchangeNamesTheJWTKind checks that a jwt-exchange passes with no
// bundle check and no row read.
func TestRecheckExchangeNamesTheJWTKind(t *testing.T) {
	t.Parallel()

	var tok accesstoken.Token
	tok.Kind = accesstoken.KindJWTExchange

	x := trustrule.Exchange{
		Rule:          "",
		Token:         tok,
		Digest:        accesstoken.Digest{},
		SPIFFEID:      "",
		BundleID:      "",
		BundleVersion: 0,
	}

	if err := recheckExchange(t.Context(), nil, namespace.Name("acme"), &x); err != nil {
		t.Errorf("error = %v, want nil", err)
	}
}
