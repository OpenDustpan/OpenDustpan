package issuersvc_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/issuersvc"
	"opendustpan.org/opendustpan/pkg/profile"
)

// failingURLs is an issuer store whose list of issuer URLs fails.
type failingURLs struct {
	profile.IssuerStore
}

var errURLs = errors.New("the list failed")

func (failingURLs) IssuerURLs(context.Context) ([]string, error) { return nil, errURLs }

func TestOwnsIssuerCoversTheHostsAndTheStoredIssuerURLs(t *testing.T) {
	t.Parallel()

	h, hs := newURLHarness(t)

	// The harness sets the issuer base URL https://dustpan.example.com and the
	// public URL https://api.example.com. The namespace sets an issuerURL.
	cases := []struct {
		name   string
		issuer string
		owns   bool
	}{
		{"the public URL", "https://api.example.com", true},
		{"the public URL with a path", "https://API.example.com:443/oidc/ns-1/", true},
		{"the public URL with a trailing dot", "https://api.example.com./oidc/x", true},
		{"the issuer base URL", "https://dustpan.example.com", true},
		{"the issuer base URL with the oidc path", "https://dustpan.example.com/oidc/" + h.ns.ID.String(), true},
		{"the issuer base URL in capitals", "HTTPS://DUSTPAN.EXAMPLE.COM/oidc/x", true},
		{"the issuer base URL with user information", "https://u@dustpan.example.com/oidc/x", true},
		{"the stored issuerURL", hs.url(), true},
		{"the stored issuerURL with a slash", hs.url() + "/", true},
		{"the stored issuerURL with a capital scheme", "HTTPS://" + strings.TrimPrefix(hs.url(), "https://"), true},
		{"the stored issuerURL with a path in capitals", strings.ToUpper(hs.url()), false},
		{"another path on the file host", hs.url() + "/other", false},
		{"a host that only starts the same", "https://api.example.com.evil.net", false},
		{"an outside issuer", "https://token.actions.example.com", false},
		{"not a URL", "ci-cluster-7", false},
		{"empty", "", false},
	}

	for _, tc := range cases {
		got, err := h.svc.OwnsIssuer(t.Context(), tc.issuer)
		if err != nil || got != tc.owns {
			t.Errorf("%s: OwnsIssuer(%q) = %v, %v; want %v", tc.name, tc.issuer, got, err, tc.owns)
		}
	}
}

// TestOwnsIssuerSeesAnIssuerURLThatIsSetLater proves the reason for the second
// call at the exchange: a rule can exist before a namespace sets the issuerURL.
func TestOwnsIssuerSeesAnIssuerURLThatIsSetLater(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	const issuer = "https://files.example.com/acme"

	if owns, err := h.svc.OwnsIssuer(t.Context(), issuer); err != nil || owns {
		t.Fatalf("before the PATCH: %v, %v; want false", owns, err)
	}

	h.setIssuerURL(issuer)

	if owns, err := h.svc.OwnsIssuer(t.Context(), issuer); err != nil || !owns {
		t.Errorf("after the PATCH: %v, %v; want true", owns, err)
	}

	set := h.settings()

	if _, err := h.patch(h.admin, patchURL(""), set.Version, okFresh); err != nil {
		t.Fatalf("clear: %v", err)
	}

	if owns, err := h.svc.OwnsIssuer(t.Context(), issuer); err != nil || owns {
		t.Errorf("after the URL was cleared: %v, %v; want false", owns, err)
	}
}

// TestOwnsIssuerFailsClosed proves that a store that cannot list the issuer
// URLs gives an error and not an answer. A host of the server needs no store
// and still answers.
func TestOwnsIssuerFailsClosed(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	cfg := newConfig(h, "https://dustpan.example.com", "")
	cfg.Store = failingURLs{IssuerStore: h.store.Issuer()}

	svc, err := issuersvc.NewService(cfg)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	owns, err := svc.OwnsIssuer(t.Context(), "https://token.example.com")
	if !errors.Is(err, errURLs) || owns {
		t.Errorf("OwnsIssuer = %v, %v; want the error of the store and false", owns, err)
	}

	if owns, err = svc.OwnsIssuer(t.Context(), "https://dustpan.example.com/oidc/x"); err != nil || !owns {
		t.Errorf("OwnsIssuer of a host of the server = %v, %v; want true", owns, err)
	}
}
