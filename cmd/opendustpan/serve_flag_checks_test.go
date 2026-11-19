package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// parseCheckFlags parses args into serveFlags with the defaults of serve.
func parseCheckFlags(t *testing.T, args ...string) *serveFlags {
	t.Helper()

	var f serveFlags

	fs := newTestFlagSet(&f)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse flags: %v", err)
	}

	return &f
}

// TestServeCheckNamesSignInFlags proves that check refuses an incomplete or
// bad sign-in setting at the start, with the flag and the variable, and keeps
// a good one.
func TestServeCheckNamesSignInFlags(t *testing.T) {
	t.Parallel()

	oidc := []string{"--oidc-issuer", "https://id.example.com", "--oidc-provider", "corp", "--oidc-client-id", "app"}
	ldap := []string{"--ldap-url", "ldaps://ldap.example.com", "--ldap-provider", "corp-ldap"}

	bad := []struct {
		name string
		args []string
		want []string
	}{
		{
			"oidc without provider",
			[]string{"--oidc-issuer", "https://id.example.com", "--oidc-client-id", "a"},
			[]string{"--oidc-provider", "DUSTPAN_OIDC_PROVIDER"},
		},
		{
			"oidc provider not valid",
			[]string{"--oidc-issuer", "https://id.example.com", "--oidc-provider", "Corp!", "--oidc-client-id", "a"},
			[]string{"--oidc-provider"},
		},
		{
			"oidc without client id",
			[]string{"--oidc-issuer", "https://id.example.com", "--oidc-provider", "corp"},
			[]string{"--oidc-client-id", "DUSTPAN_OIDC_CLIENT_ID"},
		},
		{
			"oidc issuer http",
			[]string{"--oidc-issuer", "http://id.example.com", "--oidc-provider", "corp", "--oidc-client-id", "a"},
			[]string{"--oidc-issuer", "https://"},
		},
		{
			"oidc issuer no host",
			[]string{"--oidc-issuer", "https://", "--oidc-provider", "corp", "--oidc-client-id", "a"},
			[]string{"--oidc-issuer"},
		},
		{
			"ldap without provider",
			[]string{"--ldap-url", "ldaps://ldap.example.com"},
			[]string{"--ldap-provider", "DUSTPAN_LDAP_PROVIDER"},
		},
		{
			"ldap groups search without base", append(append([]string{}, ldap...), "--ldap-groups", "search"),
			[]string{"--ldap-group-base-dn", "--ldap-groups"},
		},
		{
			"origin null",
			[]string{"--allowed-origins", "null"},
			[]string{"--allowed-origins", "DUSTPAN_ALLOWED_ORIGINS"},
		},
		{
			"origin with path",
			[]string{"--allowed-origins", "https://a.example.com,https://b.example.com/x"},
			[]string{"--allowed-origins", "b.example.com/x"},
		},
	}

	for _, tt := range bad {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := parseCheckFlags(t, tt.args...).check()
			if err == nil {
				t.Fatal("check = nil, want an error")
			}

			for _, w := range tt.want {
				if !strings.Contains(err.Error(), w) {
					t.Fatalf("check error %q does not hold %q", err, w)
				}
			}
		})
	}

	good := [][]string{
		oidc,
		append(append([]string{}, ldap...), "--ldap-groups", "search", "--ldap-group-base-dn", "ou=g,dc=x"),
		{"--oidc-issuer", "http://127.0.0.1:9000", "--oidc-provider", "dev", "--oidc-client-id", "a"},
		{"--allowed-origins", "https://console.example.com, http://localhost:3000"},
		{"--components", "worker", "--oidc-issuer", "http://x.example.com"},
	}

	for _, args := range good {
		if err := parseCheckFlags(t, args...).check(); err != nil {
			t.Fatalf("check %v = %v, want no error", args, err)
		}
	}
}

// TestServeCheckTLSFilesNamesTheFlag proves that a TLS file that does not
// exist is refused with the flag, the variable and the whole path.
func TestServeCheckTLSFilesNamesTheFlag(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "nofile.pem")

	err := parseCheckFlags(t, "--tls-cert", missing, "--tls-key", missing).checkTLSFiles()
	if !errors.Is(err, errTLSFile) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checkTLSFiles = %v, want errTLSFile and not exist", err)
	}

	for _, w := range []string{"--tls-cert", "DUSTPAN_TLS_CERT", missing} {
		if !strings.Contains(err.Error(), w) {
			t.Fatalf("error %q does not hold %q", err, w)
		}
	}

	if err := parseCheckFlags(t).checkTLSFiles(); err != nil {
		t.Fatalf("checkTLSFiles with no TLS = %v, want nil", err)
	}
}

// TestLDAPRootsNamesTheWholePath proves that the error for a CA file names
// the whole path, with the leading separator, and not the last element only.
func TestLDAPRootsNamesTheWholePath(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "nonexistent.pem")

	_, err := ldapRoots(missing)
	if err == nil || !strings.Contains(err.Error(), "--ldap-ca-file "+missing+":") ||
		strings.Contains(err.Error(), "openat") {
		t.Fatalf("ldapRoots = %v, want the flag and the whole path and no system call", err)
	}
}
