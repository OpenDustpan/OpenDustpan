package client

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

const (
	// AdminTokenFileEnvVar names the file that holds the bootstrap admin
	// tokens, as an alternative to AdminTokenEnvVar.
	AdminTokenFileEnvVar = "DUSTPAN_ADMIN_TOKEN_FILE" //nolint:gosec // The value names the setting; it holds no credential.
	// tokenFilePrivateBits are the permission bits a token file must not
	// set: group or other access of any kind, as for the key-encryption
	// key file.
	tokenFilePrivateBits = 0o077
)

var (
	// ErrAdminTokenMissing reports that neither DUSTPAN_ADMIN_TOKEN nor
	// DUSTPAN_ADMIN_TOKEN_FILE holds a token.
	ErrAdminTokenMissing = errors.New("no admin token is set")
	// ErrAdminTokenFilePermissions reports a token file that group or other
	// users can access.
	ErrAdminTokenFilePermissions = errors.New("admin token file allows group or other access")
)

// LoadAdminTokensFrom reads the admin tokens with lookup instead of the
// process environment and changes nothing in it. The variable holds
// comma-separated tokens; the file holds tokens separated by commas or new
// lines, and blank lines are skipped. The order of the tokens gives the
// administrator names: admin, admin-2, and so on.
func LoadAdminTokensFrom(lookup func(key string) (string, bool)) ([]string, error) {
	if raw, ok := lookup(AdminTokenEnvVar); ok && strings.TrimSpace(raw) != "" {
		return strings.Split(raw, ","), nil
	}

	if path, ok := lookup(AdminTokenFileEnvVar); ok && strings.TrimSpace(path) != "" {
		return ReadAdminTokenFile(strings.TrimSpace(path))
	}

	return nil, fmt.Errorf(
		"%w: set %s or %s",
		ErrAdminTokenMissing,
		AdminTokenEnvVar,
		AdminTokenFileEnvVar,
	)
}

// ReadAdminTokenFile reads the tokens in the file at path. Like the key-encryption key
// file, it refuses a file that group or other users can access.
func ReadAdminTokenFile(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%s: stat file: %w", AdminTokenFileEnvVar, err)
	}

	if perm := info.Mode().Perm(); perm&tokenFilePrivateBits != 0 {
		return nil, fmt.Errorf(
			"%s: %w %q (mode %04o): run chmod 600 on the file",
			AdminTokenFileEnvVar,
			ErrAdminTokenFilePermissions,
			path,
			perm,
		)
	}

	raw, err := os.ReadFile(path) //nolint:gosec // The operator names the token file through the environment.
	if err != nil {
		return nil, fmt.Errorf("%s: read file: %w", AdminTokenFileEnvVar, err)
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
		return nil, fmt.Errorf("%s: %w: the file %q holds no token", AdminTokenFileEnvVar, ErrAdminTokenMissing, path)
	}

	return tokens, nil
}
