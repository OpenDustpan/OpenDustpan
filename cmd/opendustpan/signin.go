package main

import (
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/pkg/dustpan"
)

// Variables of the client secret of the OpenID Connect provider. A flag
// shows in process listings, so the secret has no flag. They hold the names
// of the variables, not a value.
const (
	oidcSecretEnvVar     = "DUSTPAN_OIDC_CLIENT_SECRET"      //nolint:gosec // The name of a variable, not a credential.
	oidcSecretFileEnvVar = "DUSTPAN_OIDC_CLIENT_SECRET_FILE" //nolint:gosec // The name of a variable, not a credential.
)

// Errors of the console sign-in settings.
var (
	errNoOIDCSecret = errors.New("the console sign-in needs the OpenID Connect client secret")
	errNoOIDCIssuer = errors.New("an OpenID Connect setting needs --oidc-issuer")
)

// signInOptions returns the console sign-in option of the serve flags: one
// OpenID Connect provider when --oidc-issuer is set, else none. A process
// without the api component gets none. The client secret comes from lookup,
// which the start sets to the real environment: f.lookup is the snapshot of
// the reloads and holds only a fingerprint of a sensitive variable.
func (f *serveFlags) signInOptions(lookup func(string) (string, bool)) ([]dustpan.Option, error) {
	set, err := dustpan.ParseComponents(f.components)
	if err != nil {
		return nil, fmt.Errorf("--components: %w", err)
	}

	// Only the api component serves the console sign-in. A process without it
	// ignores the --oidc-* settings, also when they are not complete, and does
	// not read the client secret.
	if !set.Has(dustpan.ComponentAPI) {
		return nil, nil
	}

	provider, ok, err := f.oidcProviderOption(lookup)
	if err != nil || !ok {
		return nil, err
	}

	return []dustpan.Option{dustpan.WithOIDCProvider(provider)}, nil
}

// oidcProviderOption returns the OpenID Connect provider of the serve flags,
// and false when --oidc-issuer is not set.
func (f *serveFlags) oidcProviderOption(lookup func(string) (string, bool)) (dustpan.OIDCProvider, bool, error) {
	none := dustpan.OIDCProvider{
		HTTPClient:   nil,
		ID:           "",
		DisplayName:  "",
		Issuer:       "",
		ClientID:     "",
		ClientSecret: "",
		Scopes:       nil,
		AutoCreate:   false,
	}

	if f.oidcIssuer == "" {
		if f.oidcProvider != "" || f.oidcClientID != "" {
			return none, false,
				fmt.Errorf("%w: unset --oidc-provider and --oidc-client-id, or set the issuer", errNoOIDCIssuer)
		}

		return none, false, nil
	}

	secret, err := secretFrom(lookup, oidcSecretEnvVar, oidcSecretFileEnvVar, errNoOIDCSecret)
	if err != nil {
		return none, false, err
	}

	return dustpan.OIDCProvider{
		HTTPClient:   nil,
		ID:           f.oidcProvider,
		DisplayName:  f.oidcDisplayName,
		Issuer:       f.oidcIssuer,
		ClientID:     f.oidcClientID,
		ClientSecret: string(secret),
		Scopes:       splitList(f.oidcScopes),
		AutoCreate:   !f.oidcNoAutoCreate,
	}, true, nil
}
