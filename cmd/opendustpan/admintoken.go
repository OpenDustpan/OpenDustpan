package main

import (
	"fmt"
	"os"
	"strings"

	"opendustpan.org/opendustpan/internal/cli/client"
	"opendustpan.org/opendustpan/pkg/identity"
)

// adminAuthenticator builds the bootstrap authenticator of serve from the
// admin tokens that loadAdminTokens reads.
func adminAuthenticator() (*identity.StaticTokens, error) {
	tokens, err := loadAdminTokens()
	if err != nil {
		return nil, err
	}

	authn, err := identity.NewStaticTokens(tokens...)
	if err != nil {
		return nil, fmt.Errorf("admin token: %w", err)
	}

	return authn, nil
}

// loadAdminTokens returns the bootstrap admin tokens for serve. It reads
// DUSTPAN_ADMIN_TOKEN (comma-separated) or, when that variable is empty, the
// file in DUSTPAN_ADMIN_TOKEN_FILE. The variable wins when both are set, as
// for the key-encryption key. It clears DUSTPAN_ADMIN_TOKEN from the process
// environment after the read, so child processes do not inherit the tokens.
func loadAdminTokens() ([]string, error) {
	if raw, ok := os.LookupEnv(adminTokenEnvVar); ok && strings.TrimSpace(raw) != "" {
		if err := os.Unsetenv(adminTokenEnvVar); err != nil {
			return nil, fmt.Errorf("%s: clear variable: %w", adminTokenEnvVar, err)
		}

		return strings.Split(raw, ","), nil
	}

	return client.LoadAdminTokensFrom(os.LookupEnv)
}
