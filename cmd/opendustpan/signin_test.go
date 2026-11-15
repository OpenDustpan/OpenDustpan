package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

func TestSignInOptions(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	private := filepath.Join(dir, "private")
	shared := filepath.Join(dir, "shared")

	for _, path := range []string{private, shared} {
		if err := os.WriteFile(path, []byte("client-secret-from-file\n"), 0o600); err != nil {
			t.Fatalf("write secret: %v", err)
		}
	}

	// The test makes a file that others can read on purpose.
	chmodErr := os.Chmod(shared, 0o644) //nolint:gosec // See the comment above.
	if chmodErr != nil {
		t.Fatalf("chmod shared secret: %v", chmodErr)
	}

	for _, tc := range []struct {
		name     string
		issuer   string
		provider string
		vars     map[string]string
		want     int
		wantErr  error
	}{
		{name: "no sign-in", issuer: "", provider: "", vars: nil, want: 0, wantErr: nil},
		{name: "provider with no issuer", issuer: "", provider: "corp", vars: nil, want: 0, wantErr: errNoOIDCIssuer},
		{
			name: "secret in the variable", issuer: "https://idp.example", provider: "corp",
			vars: map[string]string{oidcSecretEnvVar: "client-secret"}, want: 1, wantErr: nil,
		},
		{
			name: "secret in a private file", issuer: "https://idp.example", provider: "corp",
			vars: map[string]string{oidcSecretFileEnvVar: private}, want: 1, wantErr: nil,
		},
		{
			name: "secret in a shared file", issuer: "https://idp.example", provider: "corp",
			vars: map[string]string{oidcSecretFileEnvVar: shared}, want: 0, wantErr: errSecretFilePermissions,
		},
		{name: "no secret", issuer: "https://idp.example", provider: "corp", vars: nil, want: 0, wantErr: errNoOIDCSecret},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var f serveFlags

			live := envOf(tc.vars)
			f.lookup = cli.SnapshotEnv(live, cli.DeclaredEnvs())
			f.oidcIssuer, f.oidcProvider, f.oidcClientID = tc.issuer, tc.provider, "dustpan"

			if tc.issuer == "" && tc.provider == "" {
				f.oidcClientID = ""
			}

			opts, err := f.signInOptions(live)
			if !errors.Is(err, tc.wantErr) || len(opts) != tc.want {
				t.Fatalf("signInOptions = %d options, %v; want %d, %v", len(opts), err, tc.want, tc.wantErr)
			}

			checkSignInErrorHoldsNoSecret(t, err)
		})
	}
}

// TestOIDCProviderReadsTheSecretFromTheStartEnvironment proves that the
// provider takes the client secret from the lookup of the start, not from
// the snapshot that keeps only a fingerprint.
func TestOIDCProviderReadsTheSecretFromTheStartEnvironment(t *testing.T) {
	t.Parallel()

	const secret = "client-secret-value"

	dir := t.TempDir()
	path := filepath.Join(dir, "secret")

	if err := os.WriteFile(path, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatalf("write secret: %v", err)
	}

	for _, tc := range []struct {
		name string
		vars map[string]string
	}{
		{name: "variable", vars: map[string]string{oidcSecretEnvVar: secret}},
		{name: "file", vars: map[string]string{oidcSecretFileEnvVar: path}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			live := envOf(tc.vars)

			var f serveFlags
			f.lookup = cli.SnapshotEnv(live, cli.DeclaredEnvs())
			f.oidcIssuer, f.oidcProvider, f.oidcClientID = "https://idp.example", "corp", "dustpan"

			provider, ok, err := f.oidcProviderOption(live)
			if err != nil || !ok {
				t.Fatalf("oidcProviderOption = %v, %v; want a provider", ok, err)
			}

			if provider.ClientSecret != secret {
				t.Fatalf("ClientSecret = %q, want %q", provider.ClientSecret, secret)
			}
		})
	}
}

// checkSignInErrorHoldsNoSecret proves that an error of signInOptions never
// holds the client secret.
func checkSignInErrorHoldsNoSecret(t *testing.T, err error) {
	t.Helper()

	if err != nil && strings.Contains(err.Error(), "client-secret") {
		t.Fatalf("the error holds the secret: %v", err)
	}
}
