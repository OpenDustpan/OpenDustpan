package main

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/session"
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

	missingCA := filepath.Join(t.TempDir(), "nofile-ca.pem")

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
			"ldap ca file missing", append(append([]string{}, ldap...), "--ldap-ca-file", missingCA),
			[]string{"--ldap-ca-file", missingCA},
		},
		{
			"oidc provider dev",
			[]string{
				"--oidc-issuer", "https://id.example.com", "--oidc-provider", session.DevProviderID,
				"--oidc-client-id", "a",
			},
			[]string{"--oidc-provider", "reserved"},
		},
		{
			"oidc provider local",
			[]string{
				"--oidc-issuer", "https://id.example.com", "--oidc-provider", session.LocalProviderID,
				"--oidc-client-id", "a",
			},
			[]string{"--oidc-provider", "reserved"},
		},
		{
			"ldap provider dev",
			[]string{"--ldap-url", "ldaps://ldap.example.com", "--ldap-provider", session.DevProviderID},
			[]string{"--ldap-provider", "reserved"},
		},
		{
			"ldap provider local",
			[]string{"--ldap-url", "ldaps://ldap.example.com", "--ldap-provider", session.LocalProviderID},
			[]string{"--ldap-provider", "reserved"},
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
		{"--oidc-issuer", "http://127.0.0.1:9000", "--oidc-provider", "corp", "--oidc-client-id", "a"},
		{"--oidc-issuer", "http://127.0.0.2:9000", "--oidc-provider", "corp", "--oidc-client-id", "a"},
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
	if err == nil {
		t.Fatal("checkTLSFiles = nil, want an error")
	}

	if !errors.Is(err, errTLSFile) || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("checkTLSFiles = %v, want errTLSFile and not exist", err)
	}

	for _, w := range []string{"--tls-cert", "DUSTPAN_TLS_CERT", missing} {
		if !strings.Contains(err.Error(), w) {
			t.Fatalf("error %q does not hold %q", err, w)
		}
	}

	keyErr := parseCheckFlags(t, "--tls-cert", certFile(t), "--tls-key", missing).checkTLSFiles()
	if keyErr == nil || !strings.Contains(keyErr.Error(), "set --tls-key (DUSTPAN_TLS_KEY)") ||
		strings.Contains(keyErr.Error(), "set --tls-cert") {
		t.Fatalf("checkTLSFiles with a missing key = %v, want the key flag only", keyErr)
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

// certFile writes an empty file that stands for a TLS file that exists.
func certFile(t *testing.T) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}

	return path
}

// TestServeCheckHostileValues sends hostile values straight to the checks of
// the provider ID, the issuer and the origin.
func TestServeCheckHostileValues(t *testing.T) {
	t.Parallel()

	issuer := func(v string) []string {
		return []string{"--oidc-issuer", v, "--oidc-provider", "corp", "--oidc-client-id", "a"}
	}

	cases := []struct {
		name string
		args []string
	}{
		{"oidc provider control", []string{"--oidc-issuer", "https://id.example.com", "--oidc-provider", "co\x01rp", "--oidc-client-id", "a"}},
		{"oidc provider CRLF", []string{"--oidc-issuer", "https://id.example.com", "--oidc-provider", "corp\r\nX: y", "--oidc-client-id", "a"}},
		{"ldap provider control", []string{"--ldap-url", "ldaps://l.example.com", "--ldap-provider", "co\x00rp"}},
		{"ldap provider CRLF", []string{"--ldap-url", "ldaps://l.example.com", "--ldap-provider", "corp\r\n"}},
		{"issuer userinfo", issuer("https://user:secret@id.example.com")},
		{"issuer userinfo name only", issuer("https://tokenvalue@id.example.com")},
		{"issuer query", issuer("https://id.example.com?x=1")},
		{"issuer fragment", issuer("https://id.example.com#x")},
		{"issuer CRLF", issuer("https://id.example.com/\r\nX: y")},
		{"origin upper case", []string{"--allowed-origins", "https://Console.example.com"}},
		{"origin default port", []string{"--allowed-origins", "https://a.example.com:443"}},
		{"origin query", []string{"--allowed-origins", "https://a.example.com?x=1"}},
		{"origin CRLF", []string{"--allowed-origins", "https://a.example.com\r\nX: y"}},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := parseCheckFlags(t, tt.args...).check()
			if err == nil {
				t.Fatalf("check %q = nil, want an error", tt.args)
			}

			for _, secret := range []string{"secret", "tokenvalue", "\r", "\n", "\x00", "\x01"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error %q holds %q", err, secret)
				}
			}
		})
	}
}

// TestServeCheckIssuerIsTheSessionRule proves that the serve check and the
// stored-provider rule agree on each issuer, so the two cannot drift.
func TestServeCheckIssuerIsTheSessionRule(t *testing.T) {
	t.Parallel()

	for _, issuer := range []string{
		"https://id.example.com", "http://127.0.0.1:9000", "http://127.0.0.2:9000", "http://[::1]:9000",
		"http://localhost", "http://id.example.com", "HTTPS://id.example.com", "https://u:p@id.example.com",
		"ftp://id.example.com", "https://",
	} {
		err := parseCheckFlags(t, "--oidc-issuer", issuer, "--oidc-provider", "corp", "--oidc-client-id", "a").check()
		if got, want := err == nil, session.ValidIssuer(issuer); got != want {
			t.Fatalf("issuer %q: check accepts = %v, session.ValidIssuer = %v", issuer, got, want)
		}
	}
}

// TestWarnUnusedS3Endpoint proves that the s3 endpoint warning shows only when
// neither the blob store nor the backup target is an s3:// address.
func TestWarnUnusedS3Endpoint(t *testing.T) {
	t.Parallel()

	const warning = "--s3-endpoint has no effect: --blob-store and --backup-target are not s3:// addresses"

	tests := []struct {
		name string
		args []string
		warn bool
	}{
		{"fs and s3 target", []string{"--s3-endpoint", "http://s3.example.com", "--backup-target", "s3://bucket/p"}, false},
		{"s3 store", []string{"--s3-endpoint", "http://s3.example.com", "--blob-store", "s3://bucket"}, false},
		{"fs and no target", []string{"--s3-endpoint", "http://s3.example.com"}, true},
		{"no endpoint", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			logger := slog.New(slog.NewTextHandler(&buf, nil))
			parseCheckFlags(t, tt.args...).warnUnusedS3Endpoint(t.Context(), logger)

			if got := strings.Contains(buf.String(), warning); got != tt.warn {
				t.Fatalf("log %q: warning = %v, want %v", buf.String(), got, tt.warn)
			}
		})
	}
}

// TestServeStopsOnMissingTLSFileBeforeTheDatabase proves that serve refuses a
// missing TLS file with the flag before it opens the database. The test sets
// no admin token and no key, so a start that went on would fail with another
// error.
func TestServeStopsOnMissingTLSFileBeforeTheDatabase(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	database := filepath.Join(dir, "dustpan.db")
	missing := filepath.Join(dir, "nofile.pem")

	err := serveWithIO(t.Context(), []string{
		"--database", "sqlite:" + database, "--tls-cert", missing, "--tls-key", missing,
	}, &bytes.Buffer{}, false)
	if err == nil || !errors.Is(err, errTLSFile) || !strings.Contains(err.Error(), "set --tls-cert (DUSTPAN_TLS_CERT)") ||
		!strings.Contains(err.Error(), missing) {
		t.Fatalf("serve = %v, want the TLS cert flag error with the path", err)
	}

	if _, statErr := os.Stat(database); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("stat database = %v, want not exist: serve opened the database", statErr)
	}
}

// TestCheckSignInFlagsRefusesBadComponents proves that the sign-in check names
// --components for a bad value by itself, as if check had not run before it.
func TestCheckSignInFlagsRefusesBadComponents(t *testing.T) {
	t.Parallel()

	err := parseCheckFlags(t, "--components", "nope").checkSignInFlags()
	if err == nil || !strings.Contains(err.Error(), "--components") {
		t.Fatalf("checkSignInFlags = %v, want an error that names --components", err)
	}
}
