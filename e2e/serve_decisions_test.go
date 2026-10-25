//go:build e2e

package e2e_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

// devKeyFirstLine is the first line of a key file that opendustpan dev makes.
const devKeyFirstLine = "opendustpan dev key: this key is for a trial, and opendustpan serve refuses it"

// TestServeDecisions proves through the real binary that serve stops, before it
// opens the database, when the operator did not decide the key or the database,
// when the shell holds DUSTPAN_DEV, and when the key file is a key of dev. Each
// refusal exits with 1 and names one command or setting that helps.
func TestServeDecisions(t *testing.T) {
	t.Parallel()

	bin := e2e.BuildBinaryWithVersion(t.Context(), t, t.TempDir(), "")

	dir := t.TempDir()
	keyFile := filepath.Join(dir, "serve.key")
	devKeyFile := filepath.Join(dir, "dev.key")

	writeServeKey(t, keyFile)

	devKey := devKeyFirstLine + "\nAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n"

	if err := os.WriteFile(devKeyFile, []byte(devKey), 0o600); err != nil {
		t.Fatalf("write the key file of dev: %v", err)
	}

	cases := []struct {
		name string
		args func(database string) []string
		env  []string
		want string
	}{
		{
			name: "no key",
			args: func(database string) []string { return []string{"serve", "--database", database} },
			env:  nil,
			want: "opendustpan: no key-encryption key is set: set --key-encryption-key-file " +
				"(DUSTPAN_KEY_ENCRYPTION_KEY_FILE) or DUSTPAN_KEY_ENCRYPTION_KEY. " +
				"To make a key file, run: opendustpan key generate --out <path>",
		},
		{
			name: "no database",
			args: func(string) []string { return []string{"serve", "--key-encryption-key-file", keyFile} },
			env:  nil,
			want: "opendustpan: no database is set: set --database (DUSTPAN_DATABASE), " +
				"for example sqlite:/var/lib/opendustpan/dustpan.db",
		},
		{
			name: "DUSTPAN_DEV in the shell",
			args: func(database string) []string {
				return []string{"serve", "--database", database, "--key-encryption-key-file", keyFile}
			},
			env: []string{"DUSTPAN_DEV=1"},
			want: "opendustpan: DUSTPAN_DEV is set. The dev.env file of opendustpan dev sets it. " +
				"Start serve in a new shell.",
		},
		{
			name: "key file of dev",
			args: func(database string) []string {
				return []string{"serve", "--database", database, "--key-encryption-key-file", devKeyFile}
			},
			env:  nil,
			want: "the key file was made by opendustpan dev: serve refuses it",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Each case has its own database path. The check that serve did not
			// open it runs inside the case, after the binary ended.
			databaseFile := filepath.Join(t.TempDir(), "serve.db")

			out, err := runBinaryWithEnv(t, bin, tc.env, tc.args("sqlite:"+databaseFile)...)

			exit, isExit := errors.AsType[*exec.ExitError](err)
			if !isExit || exit.ExitCode() != 1 {
				t.Fatalf("serve: err = %v, want exit code 1; output:\n%s", err, out)
			}

			if !strings.Contains(out, tc.want) {
				t.Fatalf("serve output lacks %q:\n%s", tc.want, out)
			}

			if _, statErr := os.Stat(databaseFile); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("a refused serve opened the database (stat err = %v)", statErr)
			}
		})
	}
}
