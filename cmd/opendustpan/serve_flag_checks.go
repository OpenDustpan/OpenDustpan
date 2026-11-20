package main

import (
	"errors"
	"fmt"
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
	errOIDCIssuer     = errors.New("invalid --oidc-issuer (DUSTPAN_OIDC_ISSUER)")
	errOIDCReservedID = errors.New(
		"invalid --oidc-provider (DUSTPAN_OIDC_PROVIDER): the ID is reserved for the dev and local sign-in: " +
			"use another ID",
	)
	errLDAPReservedID = errors.New(
		"invalid --ldap-provider (DUSTPAN_LDAP_PROVIDER): the ID is reserved for the dev and local sign-in: " +
			"use another ID",
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
	errTLSFile = errors.New("the TLS file cannot be found or read")
)

// checkSignInFlags refuses a sign-in setting that the server refuses later,
// before the database opens and before the secrets are read, so the error
// names the flag. A process without the api component ignores the settings,
// so it skips the check. The server keeps its own checks.
func (f *serveFlags) checkSignInFlags() error {
	// Defense in depth: check parses --components first; this parse stays so
	// that a caller of this function alone cannot skip the refusal.
	set, err := dustpan.ParseComponents(f.components)
	if err != nil {
		return fmt.Errorf("--components: %w", err)
	}

	if !set.Has(dustpan.ComponentAPI) {
		return nil
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

	if reservedProviderID(f.ldap.providerID) {
		return fmt.Errorf("%w: %q", errLDAPReservedID, f.ldap.providerID)
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

	if reservedProviderID(f.oidcProvider) {
		return fmt.Errorf("%w: %q", errOIDCReservedID, f.oidcProvider)
	}

	if f.oidcClientID == "" {
		return errOIDCClientID
	}

	// The issuer is never printed, not even in part: it may hold a user name,
	// a password, a query or a fragment with a secret. The text of
	// session.ErrIssuer names the rule and holds no issuer.
	if issuerErr := session.CheckIssuer(f.oidcIssuer); issuerErr != nil {
		return fmt.Errorf("%w: %w", errOIDCIssuer, issuerErr)
	}

	return nil
}

// reservedProviderID reports whether id is an ID that the server keeps for the
// dev sign-in or for the local users.
func reservedProviderID(id string) bool {
	return id == session.DevProviderID || id == session.LocalProviderID
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

// checkTLSFiles refuses a --tls-cert or --tls-key file that cannot be found or
// read, before the database opens.
func (f *serveFlags) checkTLSFiles() error {
	for _, item := range []struct{ flag, env, path string }{
		{"--tls-cert", "DUSTPAN_TLS_CERT", f.tlsCert},
		{"--tls-key", "DUSTPAN_TLS_KEY", f.tlsKey},
	} {
		if item.path == "" {
			continue
		}

		if err := openAndClose(item.path); err != nil {
			return fmt.Errorf("%w: %q: set %s (%s) to a readable PEM file: %w",
				errTLSFile, item.path, item.flag, item.env, unwrapPathError(err))
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

// openAndClose opens the file at path for reading and closes it, so that the
// check proves that the server can read the file, not only that it exists.
func openAndClose(path string) error {
	f, err := os.Open(path) //nolint:gosec // The operator names the TLS file with a flag.
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close: %w", err)
	}

	return nil
}
