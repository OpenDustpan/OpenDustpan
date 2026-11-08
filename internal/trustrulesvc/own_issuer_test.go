package trustrulesvc_test

import (
	"context"
	"errors"
	"testing"

	"opendustpan.org/opendustpan/internal/trustrulesvc"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/trustrule"
)

// fakeOwn is an OwnIssuer with a fixed answer.
type fakeOwn struct {
	owns map[string]bool
	err  error
	asks []string
}

func (f *fakeOwn) OwnsIssuer(_ context.Context, issuer string) (bool, error) {
	f.asks = append(f.asks, issuer)

	return f.owns[issuer], f.err
}

var errLookup = errors.New("the lookup failed")

func ownFixture(t *testing.T, own trustrulesvc.OwnIssuer) fixture {
	t.Helper()

	store := &fakeStore{created: nil, entries: nil, deleted: nil}
	authz := &grants{allow: []identity.Action{identity.ActionPrincipalManageTokens}, checked: nil, err: nil}
	people := fakePrincipals{"prn-worker": principalOf("prn-worker", principal.KindService, "acme")}

	reach := &fakeReach{checked: nil, err: nil}

	svc, err := trustrulesvc.NewService(trustrulesvc.Config{
		Store: store, Namespaces: fakeNamespaces{}, Principals: people, Authorizer: authz,
		Reach:            reach,
		InstallationURLs: []string{"https://dustpan.example.com"}, OwnIssuer: own, CheckURL: nil, Now: nil,
	})
	if err != nil {
		t.Fatalf("service: %v", err)
	}

	return fixture{svc: svc, store: store, authz: authz, reach: reach}
}

// TestCheckOwnIssuerAsksTheIssuerService checks the layers of the own-issuer
// check one by one. The static check of the installation URLs and the check of
// the issuer service each refuse alone, and an error of the issuer service
// refuses too.
func TestCheckOwnIssuerAsksTheIssuerService(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		own     *fakeOwn
		issuer  string
		refused bool
	}{
		{"the static check alone", nil, "https://dustpan.example.com/oidc/ns-1", true},
		{"no issuer service, another issuer", nil, "https://token.example.com", false},
		{
			"the issuer service says it signs",
			&fakeOwn{owns: map[string]bool{"https://files.example.com/acme": true}, err: nil, asks: nil},
			"https://files.example.com/acme",
			true,
		},
		{
			"the issuer service says it does not sign",
			&fakeOwn{owns: nil, err: nil, asks: nil},
			"https://token.example.com",
			false,
		},
		{
			"the issuer service cannot answer",
			&fakeOwn{owns: nil, err: errLookup, asks: nil},
			"https://token.example.com",
			true,
		},
		{
			"the static check wins over a no",
			&fakeOwn{owns: nil, err: nil, asks: nil},
			"https://dustpan.example.com",
			true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var own trustrulesvc.OwnIssuer
			if tc.own != nil {
				own = tc.own
			}

			err := ownFixture(t, own).svc.CheckOwnIssuer(t.Context(), tc.issuer)
			if refused := errors.Is(err, trustrule.ErrIssuerRefused); refused != tc.refused {
				t.Errorf("CheckOwnIssuer(%q) = %v, want refused %v", tc.issuer, err, tc.refused)
			}

			if tc.own != nil && tc.own.err != nil && !errors.Is(err, errLookup) {
				t.Errorf("the refusal hides the cause: %v", err)
			}
		})
	}
}

// TestCreateRefusesAnIssuerThatTheIssuerServiceOwns checks that the create of
// a rule asks the issuer service, and writes nothing when it refuses.
func TestCreateRefusesAnIssuerThatTheIssuerServiceOwns(t *testing.T) {
	t.Parallel()

	own := &fakeOwn{owns: map[string]bool{"https://files.example.com/acme": true}, err: nil, asks: nil}
	f := ownFixture(t, own)

	in := input()
	in.Issuer = "https://files.example.com/acme"

	_, err := f.svc.Create(t.Context(), person(), emptyEntry(), "acme", in)
	if !errors.Is(err, trustrule.ErrIssuerRefused) {
		t.Fatalf("Create = %v, want ErrIssuerRefused", err)
	}

	if len(f.store.created) != 0 {
		t.Fatal("the refused create reached the store")
	}

	if len(own.asks) != 1 || own.asks[0] != in.Issuer {
		t.Errorf("the issuer service was asked %v, want %q", own.asks, in.Issuer)
	}

	// The same service lets an issuer through that the issuer service does not own.
	ok := input()
	if _, err = f.svc.Create(t.Context(), person(), emptyEntry(), "acme", ok); err != nil {
		t.Errorf("Create of an outside issuer: %v", err)
	}
}
