package main

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

const (
	tokenA = "token-a-0123456789abcdef0123456789"
	tokenB = "token-b-0123456789abcdef0123456789"
)

// writeTokenFile writes content to a new file with mode and returns its path.
func writeTokenFile(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "admin-token")

	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write token file: %v", err)
	}

	// WriteFile applies the umask; set the mode the case names.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatalf("chmod token file: %v", err)
	}

	return path
}

func TestLoadAdminTokensFrom(t *testing.T) {
	t.Parallel()

	private := writeTokenFile(t, tokenA+"\n\n"+tokenB+",\n", 0o600)
	shared := writeTokenFile(t, tokenA+"\n", 0o644)
	empty := writeTokenFile(t, "\n \n", 0o600)
	missing := filepath.Join(t.TempDir(), "no-such-file")

	tests := []struct {
		name    string
		env     map[string]string
		want    []string
		wantErr error
	}{
		{
			name:    "the variable holds comma-separated tokens",
			env:     map[string]string{adminTokenEnvVar: tokenA + "," + tokenB},
			want:    []string{tokenA, tokenB},
			wantErr: nil,
		},
		{
			name:    "the variable wins over the file",
			env:     map[string]string{adminTokenEnvVar: tokenB, adminTokenFileEnvVar: private},
			want:    []string{tokenB},
			wantErr: nil,
		},
		{
			name:    "the file holds tokens on lines and after commas, blank lines skipped",
			env:     map[string]string{adminTokenFileEnvVar: private},
			want:    []string{tokenA, tokenB},
			wantErr: nil,
		},
		{
			name:    "a blank variable falls back to the file",
			env:     map[string]string{adminTokenEnvVar: " ", adminTokenFileEnvVar: private},
			want:    []string{tokenA, tokenB},
			wantErr: nil,
		},
		{
			name:    "a file that group or other users can read is refused",
			env:     map[string]string{adminTokenFileEnvVar: shared},
			want:    nil,
			wantErr: errAdminTokenFilePermissions,
		},
		{
			name:    "a file with no token is refused",
			env:     map[string]string{adminTokenFileEnvVar: empty},
			want:    nil,
			wantErr: errAdminTokenMissing,
		},
		{
			name:    "a missing file is refused",
			env:     map[string]string{adminTokenFileEnvVar: missing},
			want:    nil,
			wantErr: os.ErrNotExist,
		},
		{
			name:    "no setting is refused",
			env:     map[string]string{},
			want:    nil,
			wantErr: errAdminTokenMissing,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			lookup := func(key string) (string, bool) {
				value, ok := tc.env[key]

				return value, ok
			}

			got, err := loadAdminTokensFrom(lookup)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("load admin tokens: err = %v, want %v", err, tc.wantErr)
			}

			if !slices.Equal(got, tc.want) {
				t.Fatalf("load admin tokens = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLoadAdminTokensClearsVariable(t *testing.T) {
	t.Setenv(adminTokenEnvVar, tokenA)

	got, err := loadAdminTokens(os.LookupEnv)
	if err != nil {
		t.Fatalf("load admin tokens: %v", err)
	}

	if !slices.Equal(got, []string{tokenA}) {
		t.Fatalf("load admin tokens = %q, want %q", got, []string{tokenA})
	}

	if value, ok := os.LookupEnv(adminTokenEnvVar); ok {
		t.Fatalf("%s = %q after the read, want it cleared", adminTokenEnvVar, value)
	}
}

func TestLoadAdminTokensKeepsFileVariable(t *testing.T) {
	path := writeTokenFile(t, tokenA, 0o600)

	if err := os.Unsetenv(adminTokenEnvVar); err != nil {
		t.Fatalf("unset %s: %v", adminTokenEnvVar, err)
	}

	t.Setenv(adminTokenFileEnvVar, path)

	got, err := loadAdminTokens(os.LookupEnv)
	if err != nil {
		t.Fatalf("load admin tokens: %v", err)
	}

	if !slices.Equal(got, []string{tokenA}) {
		t.Fatalf("load admin tokens = %q, want %q", got, []string{tokenA})
	}

	if value, _ := os.LookupEnv(adminTokenFileEnvVar); value != path {
		t.Fatalf("%s = %q after the read, want the path kept: it names no secret", adminTokenFileEnvVar, value)
	}
}

func TestServeAdminTokenFile(t *testing.T) {
	setServeEnv(t, testKeyBase64())

	if err := os.Unsetenv(adminTokenEnvVar); err != nil {
		t.Fatalf("unset %s: %v", adminTokenEnvVar, err)
	}

	dbPath := filepath.Join(t.TempDir(), "dustpan.db")
	args := []string{"--database", "sqlite:" + dbPath, "--listen", "127.0.0.1:0"}

	t.Setenv(adminTokenFileEnvVar, writeTokenFile(t, adminTestToken+"\n", 0o600))

	if err := startServer(t, args...); err != nil {
		t.Fatalf("serve with a token file: %v", err)
	}

	t.Setenv(adminTokenFileEnvVar, writeTokenFile(t, adminTestToken+"\n", 0o640))

	if err := serve(t.Context(), args, new(lockedBuffer)); !errors.Is(err, errAdminTokenFilePermissions) {
		t.Fatalf("serve with a group-readable token file: err = %v, want %v", err, errAdminTokenFilePermissions)
	}

	t.Setenv(adminTokenFileEnvVar, "")

	if err := serve(t.Context(), args, new(lockedBuffer)); !errors.Is(err, errAdminTokenMissing) {
		t.Fatalf("serve with no token: err = %v, want %v", err, errAdminTokenMissing)
	}
}

func TestBackupTokenFromFile(t *testing.T) {
	t.Setenv(adminTokenEnvVar, "")
	t.Setenv(adminTokenFileEnvVar, writeTokenFile(t, tokenB+"\n"+tokenA+"\n", 0o600))

	got, err := backupToken()
	if err != nil {
		t.Fatalf("backup token: %v", err)
	}

	if got != tokenB {
		t.Fatalf("backup token = %q, want the first token %q", got, tokenB)
	}

	t.Setenv(adminTokenFileEnvVar, "")

	if _, err := backupToken(); !errors.Is(err, errBackupNoToken) {
		t.Fatalf("backup token with no setting: err = %v, want %v", err, errBackupNoToken)
	}
}
