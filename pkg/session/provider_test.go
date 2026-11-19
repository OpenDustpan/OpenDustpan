package session_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

var errProviderStoreDown = errors.New("provider store down")

// memoryProviders keeps the first issuer of each provider ID.
type memoryProviders struct {
	err     error
	issuers map[string]string
}

func (m *memoryProviders) BindProvider(_ context.Context, id, issuer string, _ time.Time) (string, error) {
	if m.err != nil {
		return "", m.err
	}

	if recorded, ok := m.issuers[id]; ok {
		return recorded, nil
	}

	m.issuers[id] = issuer

	return issuer, nil
}

func TestBindProviders(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := &memoryProviders{err: nil, issuers: map[string]string{}}
	corp := session.Provider{
		ID:           "corp",
		Issuer:       "https://idp.example",
		UserinfoURL:  "",
		SubjectClaim: "",
		DirectoryURL: "",
	}

	if err := session.BindProviders(ctx, store, []session.Provider{corp}, time.Now()); err != nil {
		t.Fatalf("first start: %v", err)
	}

	if err := session.BindProviders(ctx, store, []session.Provider{corp}, time.Now()); err != nil {
		t.Fatalf("second start with the same issuer: %v", err)
	}

	moved := session.Provider{
		ID:           "corp",
		Issuer:       "https://other.example",
		UserinfoURL:  "",
		SubjectClaim: "",
		DirectoryURL: "",
	}

	err := session.BindProviders(ctx, store, []session.Provider{moved}, time.Now())
	if err == nil || !errors.Is(err, session.ErrIssuerChanged) {
		t.Fatalf("start with another issuer = %v, want ErrIssuerChanged", err)
	}

	want := `sign-in provider "corp" has the issuer "https://idp.example" in the database and the issuer ` +
		`"https://other.example" in the configuration, so set the issuer (--oidc-issuer) back to ` +
		`"https://idp.example", or set a new provider ID only if the identity provider changed`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}

	store.err = errProviderStoreDown

	err = session.BindProviders(ctx, store, []session.Provider{corp}, time.Now())
	if !errors.Is(err, errProviderStoreDown) {
		t.Fatalf("start with the store down = %v, want the store error", err)
	}
}

// TestBindProvidersNormalizesTheIssuer proves that a change of the case of
// the scheme or the host, or a trailing slash, is the same issuer, and
// that the store records the normalized form.
func TestBindProvidersNormalizesTheIssuer(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := &memoryProviders{err: nil, issuers: map[string]string{}}

	first := session.Provider{
		ID:           "corp",
		Issuer:       "HTTPS://IdP.Example/tenant/",
		UserinfoURL:  "",
		SubjectClaim: "",
		DirectoryURL: "",
	}
	if err := session.BindProviders(ctx, store, []session.Provider{first}, time.Now()); err != nil {
		t.Fatalf("first start: %v", err)
	}

	if got := store.issuers["corp"]; got != "https://idp.example/tenant" {
		t.Fatalf("recorded issuer = %q, want the normalized form", got)
	}

	for _, issuer := range []string{"https://idp.example/tenant", "https://IDP.example/tenant/"} {
		same := session.Provider{ID: "corp", Issuer: issuer, UserinfoURL: "", SubjectClaim: "", DirectoryURL: ""}
		if err := session.BindProviders(ctx, store, []session.Provider{same}, time.Now()); err != nil {
			t.Errorf("start with %q: %v, want the same issuer", issuer, err)
		}
	}

	other := session.Provider{
		ID:           "corp",
		Issuer:       "https://idp.example/other",
		UserinfoURL:  "",
		SubjectClaim: "",
		DirectoryURL: "",
	}
	if err := session.BindProviders(ctx, store, []session.Provider{other}, time.Now()); !errors.Is(
		err, session.ErrIssuerChanged,
	) {
		t.Fatalf("start with another path = %v, want ErrIssuerChanged", err)
	}
}

func TestNormalizeIssuer(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ in, want string }{
		{"https://idp.example", "https://idp.example"},
		{"https://idp.example/", "https://idp.example"},
		{"HTTPS://IDP.Example:8443/Realms/Corp/", "https://idp.example:8443/Realms/Corp"},
		{"not a url/", "not a url"},
	} {
		if got := session.NormalizeIssuer(tc.in); got != tc.want {
			t.Errorf("NormalizeIssuer(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestBindProvidersRefusesAnotherDirectory proves that an LDAP provider ID
// keeps the directory URL of its first start: the same URL, in another case
// or with a trailing slash, starts, and any other URL, or an issuer of
// another kind of provider, is refused with an error that names the ID and
// both URLs.
func TestBindProvidersRefusesAnotherDirectory(t *testing.T) {
	t.Parallel()

	ldap := func(id, url string) session.Provider {
		return session.Provider{ID: id, Issuer: "", UserinfoURL: "", SubjectClaim: "", DirectoryURL: url}
	}

	for name, tc := range map[string]struct {
		next    session.Provider
		refused bool
	}{
		"the same URL":                  {next: ldap("corp", "ldaps://ldap.example.org"), refused: false},
		"the same URL in another case":  {next: ldap("corp", "LDAPS://LDAP.Example.org/"), refused: false},
		"another host":                  {next: ldap("corp", "ldaps://ldap.other.example"), refused: true},
		"another port":                  {next: ldap("corp", "ldaps://ldap.example.org:3636"), refused: true},
		"another scheme":                {next: ldap("corp", "ldap://ldap.example.org"), refused: true},
		"an OpenID issuer of that text": {next: session.Provider{ID: "corp", Issuer: "ldaps://ldap.example.org", UserinfoURL: "", SubjectClaim: "", DirectoryURL: ""}, refused: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := &memoryProviders{err: nil, issuers: map[string]string{}}

			err := session.BindProviders(
				t.Context(),
				store,
				[]session.Provider{ldap("corp", "ldaps://ldap.example.org")},
				time.Now(),
			)
			if err != nil {
				t.Fatalf("first start: %v", err)
			}

			err = session.BindProviders(t.Context(), store, []session.Provider{tc.next}, time.Now())
			if !tc.refused {
				if err != nil {
					t.Fatalf("second start = %v, want nil", err)
				}

				return
			}

			if !errors.Is(err, session.ErrIssuerChanged) {
				t.Fatalf("second start = %v, want ErrIssuerChanged", err)
			}
		})
	}
}

// TestDirectoryChangedError proves the text of the refusal of another
// directory: it names the provider ID, both URLs and the fix.
func TestDirectoryChangedError(t *testing.T) {
	t.Parallel()

	ldap := func(url string) session.Provider {
		return session.Provider{ID: "corp", Issuer: "", UserinfoURL: "", SubjectClaim: "", DirectoryURL: url}
	}

	store := &memoryProviders{err: nil, issuers: map[string]string{}}
	first := ldap("ldaps://ldap.example.org")

	if err := session.BindProviders(t.Context(), store, []session.Provider{first}, time.Now()); err != nil {
		t.Fatalf("first start: %v", err)
	}

	err := session.BindProviders(t.Context(), store, []session.Provider{ldap("ldaps://ldap.other.example")},
		time.Now())
	if err == nil {
		t.Fatal("start with another directory = nil, want an error")
	}

	want := `sign-in provider "corp" has the directory URL "ldaps://ldap.example.org" in the database and the ` +
		`directory URL "ldaps://ldap.other.example" in the configuration, so set the URL (--ldap-url) back to ` +
		`"ldaps://ldap.example.org", or set a new provider ID (--ldap-provider) only if the directory changed`
	if err.Error() != want {
		t.Errorf("error = %q, want %q", err, want)
	}
}
