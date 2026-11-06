package issuersvc_test

import (
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/issuersvc"
	"opendustpan.org/opendustpan/pkg/profile"
)

// newConfig returns a config over the store of a harness, with the given
// issuer base URL and public URL.
func newConfig(h *harness, base, public string) issuersvc.Config {
	return issuersvc.Config{
		Store: h.store.Issuer(), Namespaces: h.store, Sealer: scopedSealer{}, Notifier: nil, Fetcher: nil,
		Authorizer:    grants{alice: allActions()},
		IssuerBaseURL: base, PublicURL: public, Now: h.clock.Now, Log: nil,
	}
}

func TestNewServiceRefusesAnIssuerBaseURLThatIsNotAnIssuerHost(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	for name, base := range map[string]string{
		"http":                 "http://issuer.example.com",
		"a query":              "https://issuer.example.com?x=1",
		"a fragment":           "https://issuer.example.com#x",
		"a relative URL":       "issuer.example.com",
		"a path only":          "/oidc",
		"a leading space":      " https://issuer.example.com",
		"a trailing space":     "https://issuer.example.com ",
		"a space in the host":  "https://issuer example.com",
		"a newline":            "https://issuer.example.com\n",
		"user information":     "https://user@issuer.example.com",
		"a trailing slash":     "https://issuer.example.com/",
		"another scheme":       "ftp://issuer.example.com",
		"no host":              "https://",
		"a URL above the size": "https://" + strings.Repeat("a", profile.MaxIssuerURLBytes),
	} {
		_, err := issuersvc.NewService(newConfig(h, base, "https://api.example.com"))
		if err == nil {
			t.Errorf("%s: NewService accepts the issuer base URL %.40q", name, base)

			continue
		}

		wantErr(t, name, err, profile.ErrInvalid)
	}

	for _, base := range []string{"", "https://issuer.example.com", "https://issuer.example.com/p"} {
		if _, err := issuersvc.NewService(newConfig(h, base, "https://api.example.com")); err != nil {
			t.Errorf("NewService refuses the issuer base URL %q: %v", base, err)
		}
	}
}

func TestNewServiceChecksThePublicURLThatGivesTheHost(t *testing.T) {
	t.Parallel()

	h := newHarness(t)

	for name, public := range map[string]string{
		"a query":          "https://api.example.com?x=1",
		"a fragment":       "https://api.example.com#x",
		"a relative URL":   "api.example.com",
		"user information": "https://u@api.example.com",
		"another scheme":   "ftp://api.example.com",
		"no host":          "https://",
	} {
		_, err := issuersvc.NewService(newConfig(h, "", public))
		if err == nil {
			t.Errorf("%s: NewService accepts the public URL %q", name, public)

			continue
		}

		wantErr(t, name, err, profile.ErrInvalid)
	}

	// The server trims the public URL for every other use. The issuer does the
	// same, so the iss never holds a space or a slash from the setting.
	svc, err := issuersvc.NewService(newConfig(h, "", "  https://api.example.com/ \n"))
	if err != nil {
		t.Fatalf("NewService with a padded public URL: %v", err)
	}

	d, err := svc.Discovery(t.Context(), h.ns.ID)
	if err != nil || d.Issuer != "https://api.example.com/oidc/"+h.ns.ID.String() {
		t.Errorf("discovery = %+v, %v; want the trimmed public URL as host", d, err)
	}
}

// TestTheAudienceOfTheIssuerHostIsRefused covers a server with only an issuer
// base URL: the public URL is empty, and the audience still cannot be an
// address that the issuer serves.
func TestTheAudienceOfTheIssuerHostIsRefused(t *testing.T) {
	t.Parallel()

	for name, public := range map[string]string{"no public URL": "", "another public URL": "https://api.example.com"} {
		h := newHarness(t, func(c *issuersvc.Config, _ *harness) { c.PublicURL = public })

		for _, aud := range []string{
			issuerHost, issuerHost + "/oidc/" + h.ns.ID.String(), strings.ToUpper(issuerHost),
			issuerHost + ":443/x", strings.TrimPrefix(issuerHost, "https://") + ".",
		} {
			_, err := h.login(aud, 5*time.Minute)
			if err == nil {
				t.Errorf("%s: Login signs a token for the audience %q", name, aud)

				continue
			}

			wantErr(t, name+" "+aud, err, profile.ErrInvalid)
		}

		if _, err := h.login("sts.amazonaws.com", 5*time.Minute); err != nil {
			t.Errorf("%s: Login for an outside audience: %v", name, err)
		}
	}
}
