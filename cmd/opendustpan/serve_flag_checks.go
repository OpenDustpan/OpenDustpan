package main

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"

	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/session"
)

// Errors of the start-up checks of the serve flags. Each names the flag and
// the environment variable that fix it.
var (
	errOIDCProviderID = errors.New(
		"invalid --oidc-provider (DUSTPAN_OIDC_PROVIDER): use 1 to 32 lower-case letters, digits and '-', " +
			"and start with a letter, because --oidc-issuer is set",
	)
	errOIDCClientID = errors.New(
		"--oidc-client-id (DUSTPAN_OIDC_CLIENT_ID) is empty: set the client ID of the application, " +
			"because --oidc-issuer is set",
	)
	errOIDCIssuer = errors.New(
		"invalid --oidc-issuer (DUSTPAN_OIDC_ISSUER): use an https:// address, or http:// to a loopback host",
	)
	errLDAPProviderID = errors.New(
		"invalid --ldap-provider (DUSTPAN_LDAP_PROVIDER): use 1 to 32 lower-case letters, digits and '-', " +
			"and start with a letter, because --ldap-url is set",
	)
	errLDAPGroupBaseDN = errors.New(
		"--ldap-group-base-dn (DUSTPAN_LDAP_GROUP_BASE_DN) is empty: set it, because --ldap-groups is search",
	)
	errAllowedOrigin = errors.New(
		"invalid --allowed-origins (DUSTPAN_ALLOWED_ORIGINS): use http:// or https:// with a host and an " +
			"optional port, in lower case, with no path, query or fragment, for example https://console.example.com",
	)
	errTLSFile = errors.New("the TLS file cannot be read")
)

// checkSignInFlags refuses a sign-in setting that the server refuses later,
// before the database opens and before the secrets are read, so the error
// names the flag. A process without the api component ignores the settings,
// so it skips the check. The server keeps its own checks.
func (f *serveFlags) checkSignInFlags() error {
	set, err := dustpan.ParseComponents(f.components)
	if err != nil || !set.Has(dustpan.ComponentAPI) {
		return nil //nolint:nilerr // check reports a bad --components value.
	}

	if f.oidcIssuer != "" {
		if err := f.checkOIDCFlags(); err != nil {
			return err
		}
	}

	if strings.TrimSpace(f.ldap.url) != "" {
		return f.checkLDAPFlags()
	}

	return nil
}

// checkLDAPFlags refuses an LDAP setting that is not complete or not valid.
func (f *serveFlags) checkLDAPFlags() error {
	if !session.ValidProviderID(f.ldap.providerID) {
		return fmt.Errorf("%w: %q", errLDAPProviderID, f.ldap.providerID)
	}

	if f.ldap.groups == "search" && strings.TrimSpace(f.ldap.groupBaseDN) == "" {
		return errLDAPGroupBaseDN
	}

	// Read the CA file here, so a bad path is reported before the missing
	// password. The provider reads it again at the start.
	if strings.TrimSpace(f.ldap.caFile) != "" {
		if _, err := ldapRoots(f.ldap.caFile); err != nil {
			return err
		}
	}

	return nil
}

// checkOIDCFlags refuses an OpenID Connect setting that is not complete.
func (f *serveFlags) checkOIDCFlags() error {
	if !session.ValidProviderID(f.oidcProvider) {
		return fmt.Errorf("%w: %q", errOIDCProviderID, f.oidcProvider)
	}

	if f.oidcClientID == "" {
		return errOIDCClientID
	}

	u, err := url.Parse(f.oidcIssuer)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%w: %q", errOIDCIssuer, f.oidcIssuer)
	}

	loopback := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "https" && (u.Scheme != "http" || !loopback) {
		return fmt.Errorf("%w: %q", errOIDCIssuer, f.oidcIssuer)
	}

	return nil
}

// checkOriginFlags refuses an --allowed-origins item that the server refuses.
func (f *serveFlags) checkOriginFlags() error {
	for _, o := range splitList(f.origins) {
		if !dustpan.ValidAllowedOrigin(o) {
			return fmt.Errorf("%w: %q", errAllowedOrigin, o)
		}
	}

	return nil
}

// checkTLSFiles refuses a --tls-cert or --tls-key file that does not exist,
// before the database opens.
func (f *serveFlags) checkTLSFiles() error {
	for _, item := range []struct{ flag, env, path string }{
		{"--tls-cert", "DUSTPAN_TLS_CERT", f.tlsCert},
		{"--tls-key", "DUSTPAN_TLS_KEY", f.tlsKey},
	} {
		if item.path == "" {
			continue
		}

		if _, err := os.Stat(item.path); err != nil {
			return fmt.Errorf("%w: %s (%s) %s: %w", errTLSFile, item.flag, item.env, item.path, unwrapPathError(err))
		}
	}

	return nil
}

// checkFlagValues runs the checks of the sign-in settings, the allowed origins
// and the trusted proxies.
func (f *serveFlags) checkFlagValues() error {
	if err := f.checkSignInFlags(); err != nil {
		return err
	}

	if err := f.checkOriginFlags(); err != nil {
		return err
	}

	return checkProxyFlags(f.trustedProxies, f.trustedProxyHeader)
}
