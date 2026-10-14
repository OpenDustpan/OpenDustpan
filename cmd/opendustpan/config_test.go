package main

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

// writeConfig writes text to a configuration file in a new directory and
// answers its path.
func writeConfig(t *testing.T, text string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")

	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}

// TestServeConfigFile proves serve reads --config, applies flag > env >
// file, logs each setting with its layer and each override, and never logs
// a sensitive value (ADR 63 decisions 1, 6, 7 and 8). Serve reads the
// process environment, so the test sets it and does not run in parallel.
func TestServeConfigFile(t *testing.T) {
	setServeEnv(t, testKeyBase64())
	t.Setenv("DUSTPAN_REGION", "from-env")

	database := "sqlite:" + filepath.Join(t.TempDir(), "dustpan.db")

	config := writeConfig(t, `{
  # every value of this file is a test value
  "database": "`+database+`",
  "region": "from-file",
  "listen": "127.0.0.1:1",
  "log-format": "text",
  "generate-key-encryption-key": true
}
`)

	logs, err := startServerLogs(t, "--config", config, "--listen", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("serve with a config file: %v", err)
	}

	for _, want := range []string{
		"msg=setting key=config value=" + config + " layer=flag",
		"msg=setting key=database value=set layer=file",
		"msg=setting key=region value=from-env layer=env",
		"msg=setting key=listen value=127.0.0.1:0 layer=flag",
		"msg=setting key=log-format value=text layer=file",
		"msg=setting key=log-level value=info layer=default",
		"msg=setting key=DUSTPAN_ADMIN_TOKEN value=set layer=env",
		"msg=setting key=DUSTPAN_KEY_ENCRYPTION_KEY value=set layer=env",
		`level=WARN msg="setting has a value in more than one layer" key=region layer=env overridden=file`,
		`level=WARN msg="setting has a value in more than one layer" key=listen layer=flag overridden=file`,
	} {
		if !strings.Contains(logs, want) {
			t.Errorf("logs do not hold %q:\n%s", want, logs)
		}
	}

	for _, secret := range []string{database, adminTestToken, testKeyBase64()} {
		if strings.Contains(logs, secret) {
			t.Errorf("logs hold a sensitive value %q", secret)
		}
	}
}

// TestServeRefusesBadConfigFile proves a refused file stops the start, and
// the error names the key and the rule, never the value (ADR 63 decision 3).
//
//nolint:paralleltest // serve reads process environment, so this test sets it and runs its cases in order.
func TestServeRefusesBadConfigFile(t *testing.T) {
	setServeEnv(t, testKeyBase64())

	cases := []struct {
		name string
		text string
		want error
	}{
		{"unknown key", "regoin: x\n", cli.ErrConfigUnknown},
		{"bool no", "disable-pulumi-journaling: no\n", cli.ErrConfigType},
		{"secret in the file", "DUSTPAN_ADMIN_TOKEN: " + adminTestToken + "\n", cli.ErrConfigUnknown},
		{"wrong type of a sensitive value", "database: [" + adminTestToken + "]\n", cli.ErrConfigType},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := serve(t.Context(), []string{"--config", writeConfig(t, tc.text)}, os.Stderr)
			if !errors.Is(err, tc.want) {
				t.Fatalf("serve = %v, want %v", err, tc.want)
			}

			if strings.Contains(err.Error(), adminTestToken) {
				t.Errorf("error holds the value: %v", err)
			}
		})
	}

	t.Run("missing file", func(t *testing.T) {
		err := serve(t.Context(), []string{"--config", filepath.Join(t.TempDir(), "none.yaml")}, os.Stderr)
		if err == nil || !strings.Contains(err.Error(), "open config file") {
			t.Fatalf("serve = %v, want the open error", err)
		}
	})
}

// TestServeAdminTokenFileFromConfig proves the file can name the admin
// token file.
//
//nolint:paralleltest // serve reads process environment, so this test sets it.
func TestServeAdminTokenFileFromConfig(t *testing.T) {
	setServeEnv(t, testKeyBase64())

	if err := os.Unsetenv("DUSTPAN_ADMIN_TOKEN"); err != nil {
		t.Fatalf("unset: %v", err)
	}

	tokens := filepath.Join(t.TempDir(), "tokens")
	if err := os.WriteFile(tokens, []byte(adminTestToken+"\n"), 0o600); err != nil {
		t.Fatalf("write tokens: %v", err)
	}

	database := "sqlite:" + filepath.Join(t.TempDir(), "dustpan.db")
	config := writeConfig(t, `{"admin-token-file": "`+tokens+`", "generate-key-encryption-key": true}`)

	logs, err := startServerLogs(t, "--config", config, "--database", database, "--listen", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("serve with the token file from the config file: %v", err)
	}

	if !strings.Contains(logs, `"key":"admin-token-file","value":"`+tokens+`","layer":"file"`) {
		t.Errorf("logs do not name the token file from the file:\n%s", logs)
	}

	if strings.Contains(logs, adminTestToken) {
		t.Error("logs hold the admin token")
	}
}

// TestClientCommandsRefuseServerUserInfo proves each client command refuses
// a --server URL with user information before it sends anything, and the
// error never holds the value (ADR 63 decision 6).
func TestClientCommandsRefuseServerUserInfo(t *testing.T) {
	t.Parallel()

	const password = "pw-canary-7d21" //nolint:gosec // A test canary, not a credential.

	server := "https://admin:" + password + "@dustpan.example"
	backupID := "bkp-test00000000000001"

	for _, args := range [][]string{
		{"backup", "--server", server},
		{"backup", "check", "--server", server, "--backup", backupID},
		{"restore", "--server", server, "--backup", backupID},
		{"upgrade", "check", "--server", server},
		{"namespace", "create", "--server", server, "acme"},
	} {
		t.Run(strings.Join(args[:2], " "), func(t *testing.T) {
			t.Parallel()

			err := run(t.Context(), args, io.Discard, io.Discard)
			if !errors.Is(err, cli.ErrURLUserInfo) {
				t.Fatalf("run %v = %v, want %v", args[:2], err, cli.ErrURLUserInfo)
			}

			if strings.Contains(err.Error(), password) {
				t.Errorf("error holds the password: %v", err)
			}
		})
	}
}
