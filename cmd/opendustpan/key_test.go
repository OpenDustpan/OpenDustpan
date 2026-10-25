package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

// clearKeyEnv clears every key-encryption key setting and sets the admin
// token, so a bootstrap test starts with no key and no key file.
func clearKeyEnv(t *testing.T) {
	t.Helper()

	t.Setenv("DUSTPAN_ADMIN_TOKEN", adminTestToken)
	t.Setenv(keyEnvVar, "")
	t.Setenv(keyFileEnvVar, "")
	t.Setenv("DUSTPAN_DATABASE", "")
}

// The texts of the refusals of serve, word for word as the decision records
// them. The tests compare the text of the error with these strings, so a change
// of a word fails a test.
const (
	wantNoKeyText = "no key-encryption key is set: set --key-encryption-key-file " +
		"(DUSTPAN_KEY_ENCRYPTION_KEY_FILE) or DUSTPAN_KEY_ENCRYPTION_KEY. " +
		"To make a key file, run: opendustpan key generate --out <path>"
	wantNoKeyTrialText = "no key-encryption key is set. For a trial, run: opendustpan dev"
	wantNoDatabaseText = "no database is set: set --database (DUSTPAN_DATABASE), " +
		"for example sqlite:/var/lib/opendustpan/dustpan.db"
	wantNoDatabaseTrialText = "no database is set. For a trial, run: opendustpan dev"
	wantDevEnvironmentText  = "DUSTPAN_DEV is set. The dev.env file of opendustpan dev sets it. " +
		"Start serve in a new shell."
)

// refusalCase is one start of serve that must stop for a missing key or
// database. config is the text of a configuration file, empty for none. The
// file goes in --config, or in DUSTPAN_CONFIG when configInEnv is true.
type refusalCase struct {
	name        string
	terminal    bool
	args        []string
	env         map[string]string
	config      string
	configInEnv bool
	want        string
}

// TestServeRefusesMissingDecisions proves that serve stops before it opens the
// database when the key or the database is missing, checks the key first,
// reports only the first, and gives the trial hint only to a person at a
// terminal who set no setting of serve. A flag, a variable, --config,
// DUSTPAN_CONFIG and a key of a configuration file each turn the hint off.
// The text of each error is the text of the decision. Serve reads process
// environment, so the cases run one after the other.
func TestServeRefusesMissingDecisions(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", adminTestToken)

	const fileWithKey = `{"region": "other"}`

	cases := []refusalCase{
		{
			name: "no key, no terminal", terminal: false, args: nil, env: nil,
			config: "", configInEnv: false, want: wantNoKeyText,
		},
		{
			name: "no key, terminal, no setting", terminal: true, args: nil, env: nil,
			config: "", configInEnv: false, want: wantNoKeyTrialText,
		},
		{
			name: "no key, terminal, a token is no setting", terminal: true, args: nil,
			env: map[string]string{"DUSTPAN_ADMIN_TOKEN": adminTestToken}, config: "", configInEnv: false,
			want: wantNoKeyTrialText,
		},
		{
			name: "no key, terminal, a flag is a setting", terminal: true,
			args: []string{"--listen", "127.0.0.1:0"}, env: nil, config: "", configInEnv: false,
			want: wantNoKeyText,
		},
		{
			name: "no key, terminal, a variable is a setting", terminal: true, args: nil,
			env: map[string]string{"DUSTPAN_REGION": "other"}, config: "", configInEnv: false, want: wantNoKeyText,
		},
		{
			name: "no key, terminal, --config with a key in the file", terminal: true, args: nil, env: nil,
			config: fileWithKey, configInEnv: false, want: wantNoKeyText,
		},
		{
			name: "no key, terminal, DUSTPAN_CONFIG with a key in the file", terminal: true, args: nil, env: nil,
			config: fileWithKey, configInEnv: true, want: wantNoKeyText,
		},
		{
			name: "no key, terminal, --config with an empty file", terminal: true, args: nil, env: nil,
			config: "{}", configInEnv: false, want: wantNoKeyText,
		},
		{
			name: "no key and no database report the key", terminal: false, args: nil, env: nil,
			config: "", configInEnv: false, want: wantNoKeyText,
		},
		{
			name: "no database, no terminal", terminal: false, args: nil,
			env: map[string]string{keyEnvVar: testKeyBase64()}, config: "", configInEnv: false,
			want: wantNoDatabaseText,
		},
		{
			name: "no database, terminal, a key is a setting", terminal: true, args: nil,
			env: map[string]string{keyEnvVar: testKeyBase64()}, config: "", configInEnv: false,
			want: wantNoDatabaseText,
		},
		{
			name: "no database, a key file", terminal: false,
			args: []string{"--key-encryption-key-file", "missing.key"}, env: nil, config: "", configInEnv: false,
			want: wantNoDatabaseText,
		},
		{
			name: "no database, terminal, a key file", terminal: true,
			args: []string{"--key-encryption-key-file", "missing.key"}, env: nil, config: "", configInEnv: false,
			want: wantNoDatabaseText,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(keyEnvVar, "")
			checkRefusal(t, tc)
		})
	}
}

// checkRefusal runs one case of TestServeRefusesMissingDecisions.
func checkRefusal(t *testing.T, tc refusalCase) {
	t.Helper()

	clearKeyEnv(t)

	for name, value := range tc.env {
		t.Setenv(name, value)
	}

	args := tc.args

	if tc.config != "" {
		path := writeConfig(t, tc.config)

		if tc.configInEnv {
			t.Setenv("DUSTPAN_CONFIG", path)
		} else {
			args = append([]string{"--config", path}, args...)
		}
	}

	var logs lockedBuffer

	err := serveWithIO(t.Context(), args, &logs, tc.terminal)
	if err == nil || err.Error() != tc.want {
		t.Fatalf("serve error = %v, want exactly %q", err, tc.want)
	}
}

// TestRefusalTextsOfTheDecision pins the texts of the four errors and of the
// refusal of DUSTPAN_DEV word for word. The trial hint for a missing
// database is only reachable when the key is set, and a key is a setting
// that turns the hint off, so a start cannot show it today: this test keeps
// its text as the decision records it.
func TestRefusalTextsOfTheDecision(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"no key", errNoKey, wantNoKeyText},
		{"no key, trial", errNoKeyTrial, wantNoKeyTrialText},
		{"no database", errNoDatabase, wantNoDatabaseText},
		{"no database, trial", errNoDatabaseTrial, wantNoDatabaseTrialText},
		{"DUSTPAN_DEV", errDevEnvironment, wantDevEnvironmentText},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.err.Error(); got != tc.want {
				t.Fatalf("error text = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestNoSettings proves which layers turn the trial hint off: a flag, a
// variable and a key of a configuration file each do. A value of the default
// does not. The two variables that a shell often exports for other tools do
// not. A key in the variable does. It sets the layers directly, because a
// configuration file needs --config or DUSTPAN_CONFIG, which are settings
// too, so a start cannot show the layer of the file alone.
func TestNoSettings(t *testing.T) {
	t.Parallel()

	setting := func(env string, source cli.Source) cli.Resolved {
		return cli.Resolved{Setting: cli.ByEnv(env), Source: source, Value: "", Layers: nil}
	}

	cases := []struct {
		name     string
		resolved []cli.Resolved
		env      map[string]string
		want     bool
	}{
		{"only defaults", []cli.Resolved{setting("DUSTPAN_REGION", cli.SourceDefault)}, nil, true},
		{"a flag", []cli.Resolved{setting("DUSTPAN_REGION", cli.SourceFlag)}, nil, false},
		{"a variable", []cli.Resolved{setting("DUSTPAN_REGION", cli.SourceEnv)}, nil, false},
		{"a key of a file", []cli.Resolved{setting("DUSTPAN_REGION", cli.SourceFile)}, nil, false},
		{"an admin token variable", []cli.Resolved{setting("DUSTPAN_ADMIN_TOKEN", cli.SourceEnv)}, nil, true},
		{"an admin token file variable", []cli.Resolved{setting("DUSTPAN_ADMIN_TOKEN_FILE", cli.SourceEnv)}, nil, true},
		{
			"a key in the variable",
			[]cli.Resolved{setting("DUSTPAN_REGION", cli.SourceDefault)},
			map[string]string{keyEnvVar: testKeyBase64()},
			false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var f serveFlags

			f.resolved = tc.resolved

			lookup := func(name string) (string, bool) {
				value, ok := tc.env[name]

				return value, ok
			}

			if got := f.noSettings(lookup); got != tc.want {
				t.Fatalf("noSettings = %t, want %t", got, tc.want)
			}
		})
	}
}

// TestServeRefusesTheRemovedKeyFlag proves that the flag that made a key file
// is gone: serve does not know it, and it makes no file.
func TestServeRefusesTheRemovedKeyFlag(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", adminTestToken)
	clearKeyEnv(t)

	var logs lockedBuffer

	err := serveWithIO(t.Context(), []string{"--generate-key-encryption-key"}, &logs, false)
	if err == nil || !strings.Contains(logs.String(), "flag provided but not defined: -generate-key-encryption-key") {
		t.Fatalf("serve with the removed flag: err = %v, output:\n%s", err, logs.String())
	}
}

// TestServeRefusesDevEnvironment proves that serve stops when the shell holds
// DUSTPAN_DEV, also with a key and a database.
func TestServeRefusesDevEnvironment(t *testing.T) {
	clearKeyEnv(t)
	t.Setenv(keyEnvVar, testKeyBase64())
	t.Setenv(devEnvVar, "1")

	dbPath := filepath.Join(t.TempDir(), "dustpan.db")

	var logs lockedBuffer

	err := serveWithIO(t.Context(), []string{"--database", "sqlite:" + dbPath}, &logs, false)
	if !errors.Is(err, errDevEnvironment) {
		t.Fatalf("serve error = %v, want errDevEnvironment", err)
	}

	if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
		t.Fatal("serve opened the database, want it to stop before the database opens")
	}
}

// TestServeRefusesDevKeyFile proves that serve stops on a key file that
// opendustpan dev made, before it opens the database.
func TestServeRefusesDevKeyFile(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", adminTestToken)
	clearKeyEnv(t)

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "dev.key")
	dbPath := filepath.Join(dir, "dustpan.db")

	if _, err := makeDevKey(keyPath); err != nil {
		t.Fatalf("make the key of dev: %v", err)
	}

	var logs lockedBuffer

	err := serveWithIO(
		t.Context(), []string{"--database", "sqlite:" + dbPath, "--key-encryption-key-file", keyPath}, &logs, false,
	)
	if !errors.Is(err, errDevKeyRefused) {
		t.Fatalf("serve error = %v, want errDevKeyRefused", err)
	}

	if _, statErr := os.Stat(dbPath); !os.IsNotExist(statErr) {
		t.Fatal("serve opened the database, want it to stop before the database opens")
	}
}

// TestServeStartsWithKeyFile proves that serve starts with a key file that the
// operator made, keeps the file as it is, logs its path and never the key.
func TestServeStartsWithKeyFile(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", adminTestToken)
	clearKeyEnv(t)

	dir := t.TempDir()
	keyPath := filepath.Join(t.TempDir(), "dustpan.key")
	if err := os.WriteFile(keyPath, []byte(testKeyBase64()+"\n"), keyFileMode); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	args := []string{
		"--database", "sqlite:" + filepath.Join(dir, "dustpan.db"),
		"--key-encryption-key-file", keyPath,
		"--listen", "127.0.0.1:0",
	}

	logs, err := startServerLogs(t, args...)
	if err != nil {
		t.Fatalf("serve with a key file: %v", err)
	}

	if !bytes.Equal(readMadeKey(t, keyPath), []byte(testKeyString)) {
		t.Fatal("key file changed, want it unchanged")
	}

	out := logs

	if !strings.Contains(out, keyPath) {
		t.Fatalf("serve output lacks the key file path %q:\n%s", keyPath, out)
	}

	if strings.Contains(out, testKeyBase64()) {
		t.Fatal("serve output holds the key")
	}

	if strings.Contains(out, "next to the data") {
		t.Fatalf("serve warned about a key file in another directory:\n%s", out)
	}
}

// TestServeWarnsKeyNextToData proves that serve starts and logs a warning at
// each start when the key file is in the directory of the SQLite file.
func TestServeWarnsKeyNextToData(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", adminTestToken)
	clearKeyEnv(t)

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "dustpan.key")

	if err := os.WriteFile(keyPath, []byte(testKeyBase64()+"\n"), keyFileMode); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	args := []string{
		"--database", "sqlite:" + filepath.Join(dir, "dustpan.db"),
		"--key-encryption-key-file", keyPath,
		"--listen", "127.0.0.1:0",
	}

	logs, err := startServerLogs(t, args...)
	if err != nil {
		t.Fatalf("serve with a key file next to the data: %v", err)
	}

	if !strings.Contains(logs, "next to the data") {
		t.Fatalf("serve output lacks the warning:\n%s", logs)
	}
}

// TestKeyNextToData proves the check of the place of the key file: the same
// directory as the SQLite file, the blob directory and a directory under it
// warn, a link to a file in the data directory warns too, and another
// directory does not.
func TestKeyNextToData(t *testing.T) {
	t.Parallel()

	data := t.TempDir()
	elsewhere := t.TempDir()

	inData := filepath.Join(data, "dustpan.key")
	inElsewhere := filepath.Join(elsewhere, "dustpan.key")
	blobs := filepath.Join(data, "blobs")
	underBlobs := filepath.Join(blobs, "keys")
	linkToData := filepath.Join(elsewhere, "link.key")

	for _, dir := range []string{blobs, underBlobs} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatalf("make directory: %v", err)
		}
	}

	for _, path := range []string{inData, inElsewhere, filepath.Join(underBlobs, "a.key")} {
		if err := os.WriteFile(path, []byte("x"), keyFileMode); err != nil {
			t.Fatalf("write file: %v", err)
		}
	}

	if err := os.Symlink(inData, linkToData); err != nil {
		t.Skipf("symbolic links are not available: %v", err)
	}

	database := "sqlite:" + filepath.Join(data, "dustpan.db")

	cases := []struct {
		name, key, database, blobDir string
		want                         bool
	}{
		{"same directory as the SQLite file", inData, database, "", true},
		{"another directory", inElsewhere, database, "", false},
		{"link to a file in the data directory", linkToData, database, "", true},
		{"under the blob directory", filepath.Join(underBlobs, "a.key"), "postgres://h/db", blobs, true},
		{"PostgreSQL, another directory", inElsewhere, "postgres://h/db", blobs, false},
		{"file that does not exist", filepath.Join(data, "missing", "x.key"), database, "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := keyNextToData(tc.key, tc.database, tc.blobDir); got != tc.want {
				t.Fatalf("keyNextToData(%q, %q, %q) = %t, want %t", tc.key, tc.database, tc.blobDir, got, tc.want)
			}
		})
	}
}

// readMadeKey reads the base64 key at path and answers its raw bytes.
func readMadeKey(t *testing.T, path string) []byte {
	t.Helper()

	raw, err := os.ReadFile(path) //nolint:gosec // The test reads the key file it made.
	if err != nil {
		t.Fatalf("read made key file: %v", err)
	}

	key, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatalf("decode made key: %v", err)
	}

	if len(key) != 32 {
		t.Fatalf("made key holds %d bytes, want 32", len(key))
	}

	return key
}

func TestServeKeyRestartOpensWrappedKeys(t *testing.T) {
	clearKeyEnv(t)

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "dustpan.db")
	keyPath := filepath.Join(t.TempDir(), "dustpan.key")

	if err := os.WriteFile(keyPath, []byte(testKeyBase64()+"\n"), keyFileMode); err != nil {
		t.Fatalf("write key file: %v", err)
	}

	args := []string{
		"--database", "sqlite:" + dbPath, "--key-encryption-key-file", keyPath, "--listen", "127.0.0.1:0",
	}

	if _, err := startServerLogs(t, args...); err != nil {
		t.Fatalf("first serve with the key file: %v", err)
	}

	key := readMadeKey(t, keyPath)
	seedMainKey(t, dbPath, key)

	// A start clears the admin token variable after it reads it.
	t.Setenv("DUSTPAN_ADMIN_TOKEN", adminTestToken)

	second, err := startServerLogs(t, args...)
	if err != nil {
		t.Fatalf("restart with the key file: %v", err)
	}

	if !strings.Contains(second, keyPath) {
		t.Fatalf("restart output lacks the key file path %q:\n%s", keyPath, second)
	}

	if strings.Contains(second, base64.StdEncoding.EncodeToString(key)) {
		t.Fatal("restart output holds the key")
	}
}

func TestServeKeyFlagsHelp(t *testing.T) {
	t.Setenv("DUSTPAN_ADMIN_TOKEN", adminTestToken)
	clearKeyEnv(t)

	var stderr bytes.Buffer

	err := serveWithIO(t.Context(), []string{"--help"}, &stderr, false)
	if err == nil {
		t.Fatal("serve --help succeeded, want the help request")
	}

	out := stderr.String()

	for _, want := range []string{"key-encryption-key-file", "loses", "opendustpan key generate"} {
		if !strings.Contains(out, want) {
			t.Fatalf("serve --help lacks %q:\n%s", want, out)
		}
	}

	if strings.Contains(out, "generate-key-encryption-key") {
		t.Fatalf("serve --help names the removed flag:\n%s", out)
	}
}
