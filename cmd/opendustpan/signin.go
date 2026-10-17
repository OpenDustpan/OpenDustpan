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
// OpenID Connect provider when --oidc-issuer is set, else none.
func (f *serveFlags) signInOptions() ([]dustpan.Option, error) {
	if f.oidcIssuer == "" {
		if f.oidcProvider != "" || f.oidcClientID != "" {
			return nil, fmt.Errorf("%w: unset --oidc-provider and --oidc-client-id, or set the issuer", errNoOIDCIssuer)
		}

		return nil, nil
	}

	secret, err := secretFrom(f.lookup, oidcSecretEnvVar, oidcSecretFileEnvVar, errNoOIDCSecret)
	if err != nil {
		return nil, err
	}

	return []dustpan.Option{dustpan.WithOIDCProvider(dustpan.OIDCProvider{
		HTTPClient:   nil,
		ID:           f.oidcProvider,
		DisplayName:  f.oidcDisplayName,
		Issuer:       f.oidcIssuer,
		ClientID:     f.oidcClientID,
		ClientSecret: string(secret),
		Scopes:       splitList(f.oidcScopes),
		AutoCreate:   !f.oidcNoAutoCreate,
	})}, nil
}
