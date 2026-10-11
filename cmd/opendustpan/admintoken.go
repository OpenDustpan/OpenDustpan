package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"opendustpan.org/opendustpan/pkg/identity"
)

const (
	// adminTokenFileEnvVar names the file that holds the bootstrap admin
	// tokens, as an alternative to adminTokenEnvVar.
	adminTokenFileEnvVar = "DUSTPAN_ADMIN_TOKEN_FILE" //nolint:gosec // The value names the setting; it holds no credential.
)

var (
	// errAdminTokenMissing reports that neither DUSTPAN_ADMIN_TOKEN nor
	// DUSTPAN_ADMIN_TOKEN_FILE holds a token.
	errAdminTokenMissing = errors.New("no admin token is set")
	// errAdminTokenFilePermissions reports a token file that group or other
	// users can access.
	errAdminTokenFilePermissions = errors.New("admin token file allows group or other access")
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

	return loadAdminTokensFrom(os.LookupEnv)
}

// loadAdminTokensFrom reads the admin tokens with lookup instead of the
// process environment and changes nothing in it. The variable holds
// comma-separated tokens; the file holds tokens separated by commas or new
// lines, and blank lines are skipped. The order of the tokens gives the
// administrator names: admin, admin-2, and so on.
func loadAdminTokensFrom(lookup func(key string) (string, bool)) ([]string, error) {
	if raw, ok := lookup(adminTokenEnvVar); ok && strings.TrimSpace(raw) != "" {
		return strings.Split(raw, ","), nil
	}

	if path, ok := lookup(adminTokenFileEnvVar); ok && strings.TrimSpace(path) != "" {
		return readAdminTokenFile(strings.TrimSpace(path))
	}

	return nil, fmt.Errorf(
		"%w: set %s or %s",
		errAdminTokenMissing,
		adminTokenEnvVar,
		adminTokenFileEnvVar,
	)
}

// readAdminTokenFile reads the tokens in path. Like the key-encryption key
// file, it refuses a file that group or other users can access.
func readAdminTokenFile(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%s: stat file: %w", adminTokenFileEnvVar, err)
	}

	if perm := info.Mode().Perm(); perm&keyFilePrivateBits != 0 {
		return nil, fmt.Errorf(
			"%s: %w %q (mode %04o): run chmod 600 on the file",
			adminTokenFileEnvVar,
			errAdminTokenFilePermissions,
			path,
			perm,
		)
	}

	raw, err := os.ReadFile(path) //nolint:gosec // The operator names the token file through the environment.
	if err != nil {
		return nil, fmt.Errorf("%s: read file: %w", adminTokenFileEnvVar, err)
	}

	var tokens []string

	for line := range strings.SplitSeq(string(raw), "\n") {
		for entry := range strings.SplitSeq(line, ",") {
			if token := strings.TrimSpace(entry); token != "" {
				tokens = append(tokens, token)
			}
		}
	}

	if len(tokens) == 0 {
		return nil, fmt.Errorf("%s: %w: the file %q holds no token", adminTokenFileEnvVar, errAdminTokenMissing, path)
	}

	return tokens, nil
}
