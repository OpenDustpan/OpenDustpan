package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
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

			f.lookup = envOf(tc.vars)
			f.oidcIssuer, f.oidcProvider, f.oidcClientID = tc.issuer, tc.provider, "dustpan"

			if tc.issuer == "" && tc.provider == "" {
				f.oidcClientID = ""
			}

			opts, err := f.signInOptions()
			if !errors.Is(err, tc.wantErr) || len(opts) != tc.want {
				t.Fatalf("signInOptions = %d options, %v; want %d, %v", len(opts), err, tc.want, tc.wantErr)
			}
		})
	}
}
