package session_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/fakecred"
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
		Issuer:       "https://IdP.Example/tenant/",
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
		"an OpenID issuer of that text": {next: session.Provider{ID: "corp", Issuer: "https://ldap.example.org", UserinfoURL: "", SubjectClaim: "", DirectoryURL: ""}, refused: true},
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

// TestBindProvidersRefusesAnInvalidDirectoryURL sends hostile directory URLs
// straight to the session layer, as if the check of the LDAP client was
// absent. The fake credential has no literal "user:pass@" in the source.
func TestBindProvidersRefusesAnInvalidDirectoryURL(t *testing.T) {
	t.Parallel()

	withUser := fakecred.URL("ldaps", "alice", "hunter2-fake", "ldap.example.org", "")

	for name, raw := range map[string]string{
		"user information":       withUser,
		"a path":                 "ldaps://ldap.example.org/dc=example",
		"a query":                "ldaps://ldap.example.org?x=1",
		"a fragment":             "ldaps://ldap.example.org#x",
		"the http scheme":        "http://ldap.example.org",
		"the https scheme":       "https://ldap.example.org",
		"no scheme":              "ldap.example.org",
		"an empty host":          "ldaps://",
		"an empty host and port": "ldaps://:636",
		"only a root dot":        "ldaps://.",
		"an opaque URL":          "ldaps:ldap.example.org",
		"a port above 65535":     "ldaps://ldap.example.org:65536",
		"a space":                " ldaps://ldap.example.org",
		"port 0":                 "ldaps://ldap.example.org:0",
		"port 0 with zeros":      "ldap://ldap.example.org:000",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := &memoryProviders{err: nil, issuers: map[string]string{}}
			p := session.Provider{ID: "corp", Issuer: "", UserinfoURL: "", SubjectClaim: "", DirectoryURL: raw}

			err := session.BindProviders(t.Context(), store, []session.Provider{p}, time.Now())
			if !errors.Is(err, session.ErrDirectoryURL) {
				t.Fatalf("BindProviders = %v, want ErrDirectoryURL", err)
			}

			if len(store.issuers) != 0 {
				t.Errorf("the store has %d records, want none", len(store.issuers))
			}

			for _, secret := range []string{"hunter2-fake", "alice", "ldap.example.org"} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error %q holds %q", err, secret)
				}
			}
		})
	}
}

// TestNormalizeDirectoryURL proves the rules of the LDAP normalizer: URLs of
// one directory give one binding, and URLs of two directories give two.
func TestNormalizeDirectoryURL(t *testing.T) {
	t.Parallel()

	same := [][2]string{
		{"ldaps://h", "ldaps://h:636"},
		{"ldap://h", "ldap://h:389"},
		{"ldaps://h:0636", "ldaps://h"},
		{"ldaps://h.", "ldaps://h"},
		{"ldaps://h.:636", "ldaps://h"},
		{"LDAPS://H.Example.ORG", "ldaps://h.example.org"},
		{"ldaps://h/", "ldaps://h"},
		{"ldaps://h:", "ldaps://h"},
		{"ldaps://[::1]", "ldaps://[0:0::1]"},
		{"ldaps://[::1]:636", "ldaps://[0:0:0:0:0:0:0:1]"},
		{"ldaps://[2001:DB8::1]:3636", "ldaps://[2001:db8:0:0:0:0:0:1]:3636"},
	}
	for _, c := range same {
		a, errA := session.NormalizeDirectoryURL(c[0])
		b, errB := session.NormalizeDirectoryURL(c[1])

		if errA != nil || errB != nil {
			t.Errorf("normalize %q, %q: %v, %v", c[0], c[1], errA, errB)

			continue
		}

		if a != b {
			t.Errorf("%q gives %q and %q gives %q, want the same binding", c[0], a, c[1], b)
		}
	}

	different := [][2]string{
		{"ldaps://h", "ldap://h"},
		{"ldaps://h", "ldaps://h:3636"},
		{"ldap://h:636", "ldap://h"},
		{"ldaps://h:389", "ldaps://h"},
		{"ldaps://h1.example.org", "ldaps://h2.example.org"},
		{"ldaps://10.0.0.1", "ldaps://10.0.0.2"},
		{"ldaps://[::1]", "ldaps://[::2]"},
		{"ldaps://bücher.example", "ldaps://xn--bcher-kva.example"},
		{"ldap://dir.\u212Aorp", "ldap://dir.korp"},
		{"ldap://BÜCHER.example", "ldap://bücher.example"},
		{"ldap://[fe80::1%25eth0]", "ldap://[fe80::1%25ETH0]"},
	}
	for _, c := range different {
		a, errA := session.NormalizeDirectoryURL(c[0])
		b, errB := session.NormalizeDirectoryURL(c[1])

		if errA != nil || errB != nil {
			t.Errorf("normalize %q, %q: %v, %v", c[0], c[1], errA, errB)

			continue
		}

		if a == b {
			t.Errorf("%q and %q give %q, want two bindings", c[0], c[1], a)
		}
	}
}

// TestBindProvidersStartsWithAnotherFormOfTheSameDirectory proves that the
// start with another spelling of the recorded directory URL does not stop,
// and that the start with another directory does.
func TestBindProvidersStartsWithAnotherFormOfTheSameDirectory(t *testing.T) {
	t.Parallel()

	ldap := func(url string) session.Provider {
		return session.Provider{ID: "corp", Issuer: "", UserinfoURL: "", SubjectClaim: "", DirectoryURL: url}
	}

	for name, tc := range map[string]struct {
		first, next string
		refused     bool
	}{
		"ldaps and its default port":             {first: "ldaps://h", next: "ldaps://h:636", refused: false},
		"the default port and ldaps":             {first: "ldaps://h:636", next: "ldaps://h", refused: false},
		"ldap and its default port":              {first: "ldap://h", next: "ldap://h:389", refused: false},
		"a port with a leading zero":             {first: "ldaps://h", next: "ldaps://h:0636", refused: false},
		"a root dot":                             {first: "ldaps://h", next: "ldaps://h.", refused: false},
		"an IPv6 address in two forms":           {first: "ldaps://[::1]", next: "ldaps://[0:0::1]", refused: false},
		"ldaps and the port of ldap":             {first: "ldaps://h", next: "ldaps://h:389", refused: true},
		"ldap and ldaps":                         {first: "ldap://h", next: "ldaps://h", refused: true},
		"another host":                           {first: "ldaps://h", next: "ldaps://g", refused: true},
		"another IPv6 address":                   {first: "ldaps://[::1]", next: "ldaps://[::2]", refused: true},
		"an IDN host and its punycode":           {first: "ldaps://bücher.example", next: "ldaps://xn--bcher-kva.example", refused: true},
		"another port with a leading zero":       {first: "ldaps://h", next: "ldaps://h:03636", refused: true},
		"a host that differs by the Kelvin sign": {first: "ldap://dir.\u212Aorp", next: "ldap://dir.korp", refused: true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := &memoryProviders{err: nil, issuers: map[string]string{}}

			first := []session.Provider{ldap(tc.first)}

			err := session.BindProviders(t.Context(), store, first, time.Now())
			if err != nil {
				t.Fatalf("first start: %v", err)
			}

			err = session.BindProviders(t.Context(), store, []session.Provider{ldap(tc.next)}, time.Now())
			if tc.refused != errors.Is(err, session.ErrIssuerChanged) {
				t.Fatalf("second start = %v, want refused = %t", err, tc.refused)
			}
		})
	}
}

// TestDirectoryChangedErrorHidesTheRecordedRow sends hostile recorded rows
// straight to the refusal, as if BindProviders had written them: a row with
// user information, and a row that is not an LDAP binding.
func TestDirectoryChangedErrorHidesTheRecordedRow(t *testing.T) {
	t.Parallel()

	const login, phrase = "alice", "hunter2-fake"

	withUser := fakecred.URL("ldaps", login, phrase, "old.example.org", "")

	for name, row := range map[string]string{
		"a row with user information":   "ldap:" + withUser,
		"a row with a path and a query": "ldap:ldaps://old.example.org/dc=x?secret-fake=1",
		"a row of an OpenID issuer":     "https://issuer.example.org/realm-fake",
		"a row with an unknown scheme":  "ldap:" + strings.Replace(withUser, "ldaps", "http", 1),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := &memoryProviders{err: nil, issuers: map[string]string{"corp": row}}
			p := session.Provider{
				ID: "corp", Issuer: "", UserinfoURL: "", SubjectClaim: "", DirectoryURL: "ldaps://new.example.org",
			}

			err := session.BindProviders(t.Context(), store, []session.Provider{p}, time.Now())
			if !errors.Is(err, session.ErrIssuerChanged) {
				t.Fatalf("BindProviders = %v, want ErrIssuerChanged", err)
			}

			for _, secret := range []string{login, phrase, "secret-fake", "realm-fake", "/dc=x"} {
				if strings.Contains(err.Error(), secret) {
					t.Errorf("error %q holds %q", err, secret)
				}
			}
		})
	}
}

// TestDirectoryChangedErrorShowsTheNormalizedRow proves that a valid
// recorded row shows in its normalized form.
func TestDirectoryChangedErrorShowsTheNormalizedRow(t *testing.T) {
	t.Parallel()

	store := &memoryProviders{err: nil, issuers: map[string]string{"corp": "ldap:LDAPS://Old.Example.org.:636"}}
	p := session.Provider{
		ID: "corp", Issuer: "", UserinfoURL: "", SubjectClaim: "", DirectoryURL: "ldaps://new.example.org",
	}

	err := session.BindProviders(t.Context(), store, []session.Provider{p}, time.Now())
	if err == nil || !strings.Contains(err.Error(), `"ldaps://old.example.org"`) {
		t.Fatalf("BindProviders = %v, want the normalized recorded URL", err)
	}
}

// TestBindProvidersRefusesAHostileIssuer sends an issuer with a query, a
// fragment, user information or another hostile form to BindProviders, as if
// the flag parser and NewServer did not check it. The store records nothing,
// and the error never holds the issuer.
func TestBindProvidersRefusesAHostileIssuer(t *testing.T) {
	t.Parallel()

	for name, issuer := range map[string]string{
		"query and fragment":     "https://idp.example?x=1#f",
		"query":                  "https://idp.example/?x=1",
		"fragment":               "https://idp.example#f",
		"empty query":            "https://idp.example?",
		"empty fragment":         "https://idp.example#",
		"user information":       "https://user:" + "pw" + "@idp.example",
		"space":                  "https://idp.example/a b",
		"control character":      "https://idp.example/\x00",
		"other scheme":           "ftp://idp.example",
		"http public host":       "http://idp.example",
		"no host":                "https://",
		"over 512 bytes":         "https://idp.example/" + strings.Repeat("a", 600),
		"upper-case scheme":      "HTTPS://idp.example",
		"non-ASCII host":         "https://b\u00fccher.example/",
		"trailing dot host":      "https://idp.example./",
		"port over 65535":        "https://idp.example:99999/",
		"dot-dot segment":        "https://idp.example/a/../b",
		"dot segment":            "https://idp.example/./x",
		"right-to-left override": "https://idp.example/a\u202eb",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			store := &memoryProviders{err: nil, issuers: map[string]string{}}
			p := session.Provider{ID: "corp", Issuer: issuer, UserinfoURL: "", SubjectClaim: "", DirectoryURL: ""}

			err := session.BindProviders(t.Context(), store, []session.Provider{p}, time.Now())
			if !errors.Is(err, session.ErrIssuer) {
				t.Fatalf("BindProviders = %v, want ErrIssuer", err)
			}

			if len(store.issuers) != 0 {
				t.Fatalf("the store recorded %v, want nothing", store.issuers)
			}

			if strings.Contains(err.Error(), "pw@") {
				t.Fatalf("the error holds the user information: %v", err)
			}
		})
	}
}
