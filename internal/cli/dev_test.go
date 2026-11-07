package cli_test

import (
	"slices"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

// TestDevFlagsReadNoVariable proves that dev reads its settings from flags
// only: no declaration of dev has a variable or a file key.
func TestDevFlagsReadNoVariable(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for _, s := range cli.DevFlags() {
		if s.Flag == "" {
			t.Errorf("a dev setting has no flag: %+v", s)

			continue
		}

		if s.Env != "" {
			t.Errorf("dev flag --%s has the variable %s", s.Flag, s.Env)
		}

		if s.File != "" {
			t.Errorf("dev flag --%s has the file key %s", s.Flag, s.File)
		}

		if seen[s.Flag] {
			t.Errorf("dev flag --%s is declared two times", s.Flag)
		}

		seen[s.Flag] = true
	}
}

// TestDevFlagSet proves the flags of dev: the ones the decision names, and
// no flag for the database, the blob store, the keys, the audit sinks, OIDC
// or the proxy.
func TestDevFlagSet(t *testing.T) {
	t.Parallel()

	got := make([]string, 0, len(cli.DevFlags()))
	for _, s := range cli.DevFlags() {
		got = append(got, s.Flag)
	}

	want := []string{
		"data-dir", "demo", "ephemeral", "listen", "tls", "hostname", "admin-token-file", "console-dir", "log-level",
		"log-format", "disable-pulumi-journaling", "max-checkpoint-bytes", "max-secret-batch-bytes",
		"max-state-bytes", "max-body-bytes-in-flight", "blob-cache-bytes",
	}

	if !slices.Equal(got, want) {
		t.Fatalf("dev flags = %v, want %v", got, want)
	}

	for _, refused := range []string{
		"config", "database", "installation", "blob-store", "blob-dir", "backup-target", "key-encryption-key-file",
		"generate-key-encryption-key", "audit-sink", "oidc-issuer", "trusted-proxies", "tls-cert", "tls-key",
		"public-url",
	} {
		if slices.Contains(got, refused) {
			t.Errorf("dev takes the flag --%s", refused)
		}
	}
}

// TestDevDefaults proves the defaults of dev that differ from serve.
func TestDevDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		flag        string
		wantDefault string
		serveHas    string
	}{
		{flag: "listen", wantDefault: "127.0.0.1:8080", serveHas: ":8080"},
		{flag: "log-format", wantDefault: "json", serveHas: "json"},
		{flag: "log-level", wantDefault: "info", serveHas: "info"},
		{flag: "data-dir", wantDefault: "", serveHas: ""},
	}

	for _, tt := range tests {
		t.Run(tt.flag, func(t *testing.T) {
			t.Parallel()

			if got := cli.DevByFlag(tt.flag).Default; got != tt.wantDefault {
				t.Fatalf("dev --%s default = %q, want %q", tt.flag, got, tt.wantDefault)
			}

			if tt.flag == "data-dir" {
				return
			}

			if got := cli.ByFlag(tt.flag).Default; got != tt.serveHas {
				t.Fatalf(
					"serve --%s default = %q, want %q: the dev copy must not change serve",
					tt.flag,
					got,
					tt.serveHas,
				)
			}
		})
	}
}

// TestDevSecretEnvs proves the variables that dev clears: the secret
// settings of serve and the file of the administrator token, and neither
// variable that dev reads.
func TestDevSecretEnvs(t *testing.T) {
	t.Parallel()

	got := cli.DevSecretEnvs()

	for _, want := range []string{
		"DUSTPAN_ADMIN_TOKEN", "DUSTPAN_ADMIN_TOKEN_FILE", "DUSTPAN_KEY_ENCRYPTION_KEY", "DUSTPAN_DATABASE",
		"DUSTPAN_OIDC_CLIENT_SECRET", "DUSTPAN_AUDIT_WEBHOOK_SECRET",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("dev does not clear %s", want)
		}
	}

	if slices.Contains(got, cli.DevDataDirEnv) {
		t.Errorf("dev clears %s, which it reads", cli.DevDataDirEnv)
	}
}

// TestDevByFlagPanicsOnUnknown proves that a flag with no declaration fails
// fast, as it does for serve.
func TestDevByFlagPanicsOnUnknown(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("DevByFlag of an unknown flag did not panic")
		}
	}()

	cli.DevByFlag("database")
}

// TestDemoIsOnlyAFlagOfDev proves that --demo is a flag of dev and of no other
// command: serve has no flag, variable or file key for the demo person.
func TestDemoIsOnlyAFlagOfDev(t *testing.T) {
	t.Parallel()

	if got := cli.DevByFlag("demo"); got.Env != "" || got.File != "" {
		t.Fatalf("dev --demo has the variable %q and the file key %q, want none", got.Env, got.File)
	}

	for _, s := range cli.Settings {
		for _, name := range []string{s.Flag, s.Env, s.File} {
			if strings.Contains(strings.ToLower(name), "demo") {
				t.Errorf("serve setting %q names the demo person: %q", s.Flag, name)
			}
		}
	}

	for _, s := range cli.ServeFlags() {
		if s.Flag == "demo" {
			t.Error("serve takes the flag --demo")
		}
	}
}
