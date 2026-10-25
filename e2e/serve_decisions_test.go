//go:build e2e

package e2e_test

import (
	"encoding/base64"
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

	writeServeKey(t, bin, keyFile)

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

// TestKeyGenerate proves key generate through the real binary: it writes a
// key file with 32 random bytes in base64 and mode 0600, prints the path and
// the warning and never the key, and refuses a file that exists. Serve starts
// with the file in the other e2e steps.
func TestKeyGenerate(t *testing.T) {
	t.Parallel()

	bin := e2e.BuildBinaryWithVersion(t.Context(), t, t.TempDir(), "")
	path := filepath.Join(t.TempDir(), "dustpan.key")

	out, err := runBinary(t, bin, "key", "generate", "--out", path)
	if err != nil {
		t.Fatalf("key generate: %v\n%s", err, out)
	}

	for _, want := range []string{"key file: " + path, "loses every secret", "Nobody can recover them"} {
		if !strings.Contains(out, want) {
			t.Fatalf("key generate output lacks %q:\n%s", want, out)
		}
	}

	checkKeyFile(t, path, out)

	out, err = runBinary(t, bin, "key", "generate", "--out", path)
	if err == nil || !strings.Contains(out, "the key file exists: "+path+": set --out to a new path") {
		t.Fatalf("key generate over the file: err = %v, output:\n%s", err, out)
	}

	directory := t.TempDir()

	out, err = runBinary(t, bin, "key", "generate", "--out", directory+"/")
	if err == nil || !strings.Contains(out, "the key file path names a directory") {
		t.Fatalf("key generate over a directory: err = %v, output:\n%s", err, out)
	}

	if entries, readErr := os.ReadDir(directory); readErr != nil || len(entries) != 0 {
		t.Fatalf("key generate wrote into the directory (read err %v): %v", readErr, entries)
	}

	out, err = runBinary(t, bin, "key", "generate")
	if err == nil || !strings.Contains(out, "no key file path is set: set --out <path>") {
		t.Fatalf("key generate with no path: err = %v, output:\n%s", err, out)
	}
}

// readKeyFile reads the key file at path.
func readKeyFile(t *testing.T, path string) []byte {
	t.Helper()

	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatalf("open the directory of the key file: %v", err)
	}

	raw, readErr := root.ReadFile(filepath.Base(path))
	if closeErr := root.Close(); readErr != nil || closeErr != nil {
		t.Fatalf("read the key file: %v %v", readErr, closeErr)
	}

	return raw
}

// checkKeyFile proves that the key file at path has mode 0600 and 32 bytes in
// base64, and that the report of key generate does not hold the key.
func checkKeyFile(t *testing.T, path, report string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("key file: stat err = %v, mode = %v, want mode 0600", err, info)
	}

	raw := readKeyFile(t, path)

	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil || len(key) != 32 {
		t.Fatalf("key file holds %d bytes (decode err = %v), want 32 bytes in base64", len(key), err)
	}

	if strings.Contains(report, strings.TrimSpace(string(raw))) {
		t.Fatal("key generate printed the key")
	}
}

// doctorRow is one run of doctor that must print a line and exit with a code.
type doctorRow struct {
	name string
	args []string
	env  []string
	exit int
	want string
}

// TestDoctor proves doctor through the real binary: it prints one line for
// each check, exits with 0 when no check fails and with 1 when one fails, and
// with --server it asks a running server through the REST API. There is a
// failing row for the checks of the key (missing, loose mode, unreadable),
// the database, the admin token and the listen address, and a row for a dev
// server.
func TestDoctor(t *testing.T) {
	t.Parallel()

	bin := e2e.BuildBinaryWithVersion(t.Context(), t, t.TempDir(), "")
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "key")
	database := "sqlite:" + filepath.Join(dir, "serve.db")
	settings := []string{
		"--database", database,
		"--key-encryption-key-file", keyFile,
		"--backup-target", "dir:" + t.TempDir(),
		"--listen", "127.0.0.1:8080",
	}

	writeServeKey(t, bin, keyFile)

	looseKey := filepath.Join(dir, "loose.key")
	writeServeKey(t, bin, looseKey)
	chmodKey(t, looseKey, 0o660)

	unreadableKey := filepath.Join(dir, "unreadable.key")
	writeServeKey(t, bin, unreadableKey)
	chmodKey(t, unreadableKey, 0o000)

	t.Run("a good install", func(t *testing.T) {
		t.Parallel()

		out, err := runBinary(t, bin, append([]string{"doctor"}, settings...)...)
		checkGoodDoctor(t, out, err)
	})

	rows := []doctorRow{
		{
			name: "no key", args: []string{"doctor", "--database", database}, env: nil, exit: 1,
			want: "fail  key: no key-encryption key is set",
		},
		{
			name: "a loose key mode",
			args: []string{"doctor", "--database", database, "--key-encryption-key-file", looseKey},
			env:  nil,
			exit: 1,
			want: "fail  key: the key file " + looseKey + " has mode 0660",
		},
		{
			name: "a key file that serve cannot read",
			args: []string{"doctor", "--database", database, "--key-encryption-key-file", unreadableKey},
			env:  nil, exit: 1, want: "the owner cannot read it, so serve cannot read it",
		},
		{
			name: "no database", args: []string{"doctor", "--key-encryption-key-file", keyFile}, env: nil, exit: 1,
			want: "fail  database: no database is set",
		},
		{
			name: "a short admin token", args: append([]string{"doctor"}, settings...),
			env: []string{"DUSTPAN_ADMIN_TOKEN=short-token"}, exit: 1,
			want: "fail  admin token: an admin token has fewer than 32 bytes",
		},
		{
			name: "plain HTTP on a public address",
			args: []string{"doctor", "--database", database, "--key-encryption-key-file", keyFile, "--listen", ":8080"},
			env:  nil, exit: 0, want: "warn  listen address: :8080 is not a loopback address and uses plain HTTP",
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			checkDoctorRow(t, bin, row)
		})
	}

	t.Run("a running production server", func(t *testing.T) {
		t.Parallel()

		serverDir := t.TempDir()
		server := startProduction(t, bin, serverDir, t.TempDir())

		t.Cleanup(func() { server.stop(t) })

		out, err := runBinary(t, bin, append([]string{"doctor", "--server", server.url}, settings...)...)
		if err != nil ||
			!strings.Contains(out, "pass  server kind: the server reports the installation kind production") {
			t.Fatalf("doctor with --server: err = %v, output:\n%s", err, out)
		}
	})

	t.Run("a dev server", func(t *testing.T) {
		t.Parallel()

		if _, err := exec.LookPath("script"); err != nil {
			t.Skip("the dev server needs the script utility for a pseudo terminal")
		}

		dev := startPlainDev(t, bin)
		token := strings.TrimSpace(string(mustReadFile(t, filepath.Join(dev.dir, "dev.token"))))

		checkDoctorRow(t, bin, doctorRow{
			name: "a dev server", args: append([]string{"doctor", "--server", dev.url}, settings...),
			env: []string{"DUSTPAN_ADMIN_TOKEN=" + token}, exit: 1,
			want: "fail  server kind: the server reports the installation kind dev, not production",
		})
	})
}

// checkDoctorRow runs doctor for the row and checks the exit code and the line.
func checkDoctorRow(t *testing.T, bin string, row doctorRow) {
	t.Helper()

	out, err := runBinaryWithEnv(t, bin, row.env, row.args...)

	code := 0

	if err != nil {
		exit, isExit := errors.AsType[*exec.ExitError](err)
		if !isExit {
			t.Fatalf("doctor %s: %v", row.name, err)
		}

		code = exit.ExitCode()
	}

	if code != row.exit || !strings.Contains(out, row.want) {
		t.Fatalf("doctor %s: exit = %d, want %d; output lacks %q:\n%s", row.name, code, row.exit, row.want, out)
	}
}

// chmodKey sets the mode of the key file at path inside its directory.
func chmodKey(t *testing.T, path string, mode os.FileMode) {
	t.Helper()

	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatalf("open the directory of the key file: %v", err)
	}

	chmodErr := root.Chmod(filepath.Base(path), mode)
	if closeErr := root.Close(); chmodErr != nil || closeErr != nil {
		t.Fatalf("chmod the key file: %v %v", chmodErr, closeErr)
	}
}

// checkGoodDoctor proves that doctor passed a good install: exit code 0, a pass
// line for each local check, and no admin token in the output.
func checkGoodDoctor(t *testing.T, out string, err error) {
	t.Helper()

	if err != nil || strings.Contains(out, "fail ") {
		t.Fatalf("doctor on a good install: err = %v, output:\n%s", err, out)
	}

	for _, want := range []string{"pass  key: ", "pass  database: ", "pass  admin token: ", "pass  backup target: "} {
		if !strings.Contains(out, want) {
			t.Fatalf("doctor output lacks %q:\n%s", want, out)
		}
	}

	if strings.Contains(out, serveToken) {
		t.Fatalf("doctor printed the admin token:\n%s", out)
	}
}
