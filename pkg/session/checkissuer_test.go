package session_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/session"
)

// TestCheckIssuerAllowlist proves that CheckIssuer accepts only the forms of
// an issuer that the allowlist names, and refuses every other form.
func TestCheckIssuerAllowlist(t *testing.T) {
	t.Parallel()

	for issuer, ok := range map[string]bool{
		"https://idp.example":                             true,
		"https://IdP.Example/tenant/":                     true,
		"https://idp.example:8443/a/b":                    true,
		"https://xn--bcher-kva.example/":                  true,
		"https://192.0.2.1/":                              true,
		"https://[2001:db8::1]:8443/":                     true,
		"https://idp.example/a%3Fb":                       true,
		"https://idp.example/a%23b":                       true,
		"https://idp.example/a:b@c":                       true,
		"http://localhost:8080":                           true,
		"http://127.0.0.1":                                true,
		"http://[::1]:9000/x":                             true,
		"HTTPS://idp.example":                             false,
		"HTTP://localhost":                                false,
		"https://b\u00fccher.example/":                    false,
		"https://idp.example./":                           false,
		"https://idp.example:99999/":                      false,
		"https://idp.example:0/":                          false,
		"https://idp.example:/":                           false,
		"https://idp.example:80a/":                        false,
		"https://idp.example:65535/":                      true,
		"https://idp.example:65536/":                      false,
		"https://idp.example:+443/":                       false,
		"https://idp.example:000443/":                     false,
		"https://idp.example:-1/":                         false,
		"https://idp.example/a/../b":                      false,
		"https://idp.example/./x":                         false,
		"https://idp.example/%2e%2e/x":                    false,
		"https://idp.example/a\u202eb":                    false,
		"https://idp.example/a%zz":                        false,
		"https://idp.example/a%2":                         false,
		"https://-idp.example":                            false,
		"https://idp_x.example":                           false,
		"https://999.1.1.1":                               false,
		"https://[::1%25eth0]/":                           false,
		"https://[::1":                                    false,
		"https://[idp]/":                                  false,
		"https://[::1]x/":                                 false,
		"http://idp.example":                              false,
		"http://localhost.example":                        false,
		"https://user@idp.example":                        false,
		"https://":                                        false,
		"https:///x":                                      false,
		"ftp://idp.example":                               false,
		"https://idp.example/" + strings.Repeat("a", 600): false,
		"": false,
	} {
		err := session.CheckIssuer(issuer)
		if (err == nil) != ok {
			t.Errorf("CheckIssuer(%.60q) = %v, want ok %v", issuer, err, ok)
		}

		if err != nil && !errors.Is(err, session.ErrIssuer) {
			t.Errorf("CheckIssuer(%.60q) = %v, want ErrIssuer", issuer, err)
		}

		// The error text holds no part of the issuer.
		if err != nil && (strings.Contains(err.Error(), "s3cr3t") || strings.Contains(err.Error(), "pass")) {
			t.Errorf("CheckIssuer(%.60q) error holds a part of the issuer: %v", issuer, err)
		}
	}
}
