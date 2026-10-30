package dustpan_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/profile"
)

// TestNewServerRefusesAnIssuerBaseURLThatIsNotAnIssuerHost checks the start of
// the server. The value becomes the iss of each namespace with no issuerURL,
// so a bad value must stop the start and not reach a token.
func TestNewServerRefusesAnIssuerBaseURLThatIsNotAnIssuerHost(t *testing.T) {
	t.Parallel()

	for name, raw := range map[string]string{
		"http":             "http://issuer.example.com",
		"a query":          "https://issuer.example.com?x=1",
		"a fragment":       "https://issuer.example.com#x",
		"a relative URL":   "issuer.example.com",
		"a leading space":  " https://issuer.example.com",
		"a trailing space": "https://issuer.example.com ",
		"user information": "https://user@issuer.example.com",
		"a trailing slash": "https://issuer.example.com/",
		"no host":          "https://",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			st, blobs, authn := requireServerPorts(t)
			opts := append(fullServerOptions(t, st, blobs, authn),
				dustpan.WithIssuerStore(st.Issuer()), dustpan.WithIssuerBaseURL(raw))

			_, err := dustpan.NewServer(opts...)
			if !errors.Is(err, profile.ErrInvalid) || !strings.Contains(err.Error(), "issuer base URL") {
				t.Errorf(
					"NewServer with the issuer base URL %q: err = %v, want ErrInvalid naming the setting",
					raw,
					err,
				)
			}
		})
	}

	st, blobs, authn := requireServerPorts(t)
	opts := append(fullServerOptions(t, st, blobs, authn),
		dustpan.WithIssuerStore(st.Issuer()), dustpan.WithIssuerBaseURL("https://issuer.example.com"))

	if _, err := dustpan.NewServer(opts...); err != nil {
		t.Errorf("NewServer with a valid issuer base URL: %v", err)
	}
}
