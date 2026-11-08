package cli_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

// serveFlagSet registers every serve setting that has a flag on a new flag
// set and parses args.
func serveFlagSet(t *testing.T, args ...string) *flag.FlagSet {
	t.Helper()

	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	for _, s := range cli.ServeFlags() {
		switch s.Kind {
		case cli.KindBool:
			s.Bool(fs, new(bool))
		case cli.KindInt:
			s.Int64(fs, new(int64))
		case cli.KindString:
			s.String(fs, new(string))
		default:
			t.Fatalf("setting %s has an unknown kind", s.Name())
		}
	}

	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse %v: %v", args, err)
	}

	return fs
}

// mapLookup answers a lookup over env.
func mapLookup(env map[string]string) cli.LookupFunc {
	return func(key string) (string, bool) {
		value, ok := env[key]

		return value, ok
	}
}

// resolvedFor answers the resolved setting with the name key.
func resolvedFor(t *testing.T, resolved []cli.Resolved, key string) cli.Resolved {
	t.Helper()

	for _, r := range resolved {
		if r.Setting.Name() == key {
			return r
		}
	}

	t.Fatalf("no resolved setting %s", key)

	return cli.Resolved{Setting: nil, Source: "", Value: "", Layers: nil}
}

// TestResolvePrecedence proves flag > env > file > default for each kind,
// and that the flag set holds the effective value.
func TestResolvePrecedence(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		key        string
		args       []string
		env        map[string]string
		file       cli.ConfigFile
		wantValue  string
		wantSource cli.Source
		wantLayers int
	}{
		{"default", "installation", nil, nil, nil, "local", cli.SourceDefault, 0},
		{"file", "installation", nil, nil, cli.ConfigFile{"installation": "f"}, "f", cli.SourceFile, 1},
		{
			"env over file", "installation", nil,
			map[string]string{"DUSTPAN_INSTALLATION": "e"},
			cli.ConfigFile{"installation": "f"},
			"e", cli.SourceEnv, 2,
		},
		{
			"flag over env and file", "installation",
			[]string{"--installation=x"},
			map[string]string{"DUSTPAN_INSTALLATION": "e"},
			cli.ConfigFile{"installation": "f"},
			"x", cli.SourceFlag, 3,
		},
		{
			"empty env is not set", "installation", nil,
			map[string]string{"DUSTPAN_INSTALLATION": ""},
			cli.ConfigFile{"installation": "f"},
			"f", cli.SourceFile, 1,
		},
		{
			"blank env is not set", "installation", nil,
			map[string]string{"DUSTPAN_INSTALLATION": " \t "},
			nil,
			"local", cli.SourceDefault, 0,
		},
		{
			"empty int env is not set", "max-state-bytes", nil,
			map[string]string{"DUSTPAN_MAX_STATE_BYTES": ""},
			cli.ConfigFile{"max-state-bytes": "7"},
			"7", cli.SourceFile, 1,
		},
		{
			"blank bool env is not set", "disable-pulumi-journaling", nil,
			map[string]string{"DUSTPAN_DISABLE_PULUMI_JOURNALING": "  "},
			cli.ConfigFile{"disable-pulumi-journaling": "true"},
			"true", cli.SourceFile, 1,
		},
		{
			"blank secret is not set", "DUSTPAN_ADMIN_TOKEN", nil,
			map[string]string{"DUSTPAN_ADMIN_TOKEN": "  "},
			nil,
			"not set", cli.SourceDefault, 0,
		},
		{
			"empty variable of a file key is not set", "admin-token-file", nil,
			map[string]string{"DUSTPAN_ADMIN_TOKEN_FILE": ""},
			cli.ConfigFile{"admin-token-file": "/t"},
			"/t", cli.SourceFile, 1,
		},
		{
			"bool file", "disable-pulumi-journaling", nil, nil,
			cli.ConfigFile{"disable-pulumi-journaling": "true"},
			"true", cli.SourceFile, 1,
		},
		{
			"bool env 1", "disable-pulumi-journaling", nil,
			map[string]string{"DUSTPAN_DISABLE_PULUMI_JOURNALING": "1"},
			cli.ConfigFile{"disable-pulumi-journaling": "false"},
			"true", cli.SourceEnv, 2,
		},
		{
			"bool flag", "disable-pulumi-journaling",
			[]string{"--disable-pulumi-journaling=false"},
			nil,
			cli.ConfigFile{"disable-pulumi-journaling": "true"},
			"false", cli.SourceFlag, 2,
		},
		{"int file", "max-state-bytes", nil, nil, cli.ConfigFile{"max-state-bytes": "42"}, "42", cli.SourceFile, 1},
		{
			"int bad env", "max-state-bytes", nil,
			map[string]string{"DUSTPAN_MAX_STATE_BYTES": "x"},
			cli.ConfigFile{"max-state-bytes": "42"},
			"-1", cli.SourceEnv, 2,
		},
		{"int default", "checkpoint-delta-cutoff", nil, nil, nil, "1048576", cli.SourceDefault, 0},
		{
			"variable from the file", "admin-token-file", nil, nil,
			cli.ConfigFile{"admin-token-file": "/t"},
			"/t", cli.SourceFile, 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fs := serveFlagSet(t, tc.args...)

			resolved, err := cli.Resolve(fs, cli.ServeSettings(), mapLookup(tc.env), tc.file)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}

			got := resolvedFor(t, resolved, tc.key)

			if got.Value != tc.wantValue || got.Source != tc.wantSource || len(got.Layers) != tc.wantLayers {
				t.Errorf("got %q from %s with %d layers, want %q from %s with %d layers",
					got.Value, got.Source, len(got.Layers), tc.wantValue, tc.wantSource, tc.wantLayers)
			}

			if f := fs.Lookup(tc.key); f != nil && f.Value.String() != tc.wantValue {
				t.Errorf("flag --%s holds %q, want %q", tc.key, f.Value.String(), tc.wantValue)
			}
		})
	}
}

// logLine is one decoded JSON log line.
type logLine map[string]any

// logLines decodes the JSON lines in buf.
func logLines(t *testing.T, buf *bytes.Buffer) []logLine {
	t.Helper()

	var lines []logLine

	for raw := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		if raw == "" {
			continue
		}

		var line logLine
		if err := json.Unmarshal([]byte(raw), &line); err != nil {
			t.Fatalf("decode log line %q: %v", raw, err)
		}

		lines = append(lines, line)
	}

	return lines
}

// linesByLevel answers the decoded log lines of level by their key.
func linesByLevel(t *testing.T, buf *bytes.Buffer, level string) map[string]logLine {
	t.Helper()

	out := map[string]logLine{}

	for _, line := range logLines(t, buf) {
		if key, ok := line["key"].(string); ok && line["level"] == level {
			out[key] = line
		}
	}

	return out
}

// TestLogResolved proves one INFO line for each setting and one WARN line
// for each setting with more than one layer, also when the values are equal.
func TestLogResolved(t *testing.T) {
	t.Parallel()

	fs := serveFlagSet(t, "--installation=same", "--listen=:9090")
	env := map[string]string{"DUSTPAN_INSTALLATION": "same", "DUSTPAN_LOG_LEVEL": "debug"}
	file := cli.ConfigFile{"installation": "same", "log-level": "warn", "max-state-bytes": "7"}

	resolved, err := cli.Resolve(fs, cli.ServeSettings(), mapLookup(env), file)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}

	var buf bytes.Buffer

	cli.LogResolved(t.Context(), slog.New(slog.NewJSONHandler(&buf, nil)), resolved)

	infos := linesByLevel(t, bytes.NewBuffer(buf.Bytes()), "INFO")
	if len(infos) != len(cli.ServeSettings()) {
		t.Errorf("got %d INFO lines, want %d", len(infos), len(cli.ServeSettings()))
	}

	wantInfo := map[string][2]string{
		"installation":    {"same", "flag"},
		"listen":          {":9090", "flag"},
		"log-level":       {"debug", "env"},
		"max-state-bytes": {"7", "file"},
		"log-format":      {"json", "default"},
	}

	for key, want := range wantInfo {
		if infos[key]["value"] != want[0] || infos[key]["layer"] != want[1] {
			t.Errorf("INFO %s = %v, want value %s from %s", key, infos[key], want[0], want[1])
		}
	}

	wantWarn := map[string]logLine{
		"installation": {"layer": "flag", "overridden": "env,file", "flag": "same", "env": "same", "file": "same"},
		"log-level":    {"layer": "env", "overridden": "file", "env": "debug", "file": "warn"},
	}

	warns := linesByLevel(t, &buf, "WARN")
	if len(warns) != len(wantWarn) {
		t.Errorf("got WARN lines for %d keys, want %d: %v", len(warns), len(wantWarn), warns)
	}

	for key, want := range wantWarn {
		for attr, value := range want {
			if warns[key][attr] != value {
				t.Errorf("WARN %s %s = %v, want %v", key, attr, warns[key][attr], value)
			}
		}
	}
}

// canary holds the sensitive serve settings, and the flag arguments, the
// variables and the file that set each of them to a canary value in each
// layer that can hold it.
type canary struct {
	sensitive []*cli.Setting
	args      []string
	env       map[string]string
	file      cli.ConfigFile
}

// canaryLayers answers the canary layers of every sensitive serve setting.
func canaryLayers(t *testing.T) canary {
	t.Helper()

	var (
		sensitive []*cli.Setting
		args      []string
		env       = map[string]string{}
		file      = cli.ConfigFile{}
	)

	for _, s := range cli.ServeSettings() {
		if !s.Sensitive {
			continue
		}

		sensitive = append(sensitive, s)
		value := s.Name() + "-" + canaryValue

		if s.Flag != "" {
			args = append(args, "--"+s.Flag+"="+value)
		}

		if s.Env != "" {
			env[s.Env] = value
		}

		if s.File != "" {
			file[s.File] = value
		}
	}

	if len(sensitive) == 0 {
		t.Fatal("no sensitive serve setting")
	}

	return canary{sensitive: sensitive, args: args, env: env, file: file}
}

// checkSensitiveLines proves each INFO line of a sensitive setting shows
// only set or not set: set when a layer or the default gives a value.
func checkSensitiveLines(t *testing.T, buf *bytes.Buffer, sensitive []*cli.Setting) {
	t.Helper()

	infos := linesByLevel(t, buf, "INFO")

	for _, s := range sensitive {
		line := infos[s.Name()]

		want := "not set"
		if line["layer"] != "default" || s.Default != "" {
			want = "set"
		}

		if line["value"] != want {
			t.Errorf("INFO %s = %v, want value %s", s.Name(), line, want)
		}
	}
}

// checkNoLeak proves text holds no form of the canary value of each
// sensitive setting: not the value, not its last 8 characters, not its
// SHA-256.
func checkNoLeak(t *testing.T, name, text string, sensitive []*cli.Setting) {
	t.Helper()

	for _, s := range sensitive {
		if form := leak(text, s.Name()+"-"+canaryValue); form != "" {
			t.Errorf("%s holds %s of the value of %s:\n%s", name, form, s.Name(), text)
		}
	}
}

// TestSensitiveValuesNeverLogged is the canary test of ADR 63: it sets each
// sensitive setting to a known value in each layer that can hold it, alone
// and all at once, and proves no log line and no resolved value holds it.
func TestSensitiveValuesNeverLogged(t *testing.T) {
	t.Parallel()

	c := canaryLayers(t)

	cases := []struct {
		name string
		args []string
		env  map[string]string
		file cli.ConfigFile
	}{
		{"flag", c.args, nil, nil},
		{"env", nil, c.env, nil},
		{"file", nil, nil, c.file},
		{"every layer", c.args, c.env, c.file},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resolved, err := cli.Resolve(serveFlagSet(t, tc.args...), cli.ServeSettings(), mapLookup(tc.env), tc.file)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}

			var jsonBuf, textBuf bytes.Buffer

			opts := &slog.HandlerOptions{Level: slog.LevelDebug}
			cli.LogResolved(t.Context(), slog.New(slog.NewJSONHandler(&jsonBuf, opts)), resolved)
			cli.LogResolved(t.Context(), slog.New(slog.NewTextHandler(&textBuf, opts)), resolved)

			for name, text := range map[string]string{
				"JSON log": jsonBuf.String(), "text log": textBuf.String(),
				"resolved values": fmt.Sprintf("%+v", resolved),
			} {
				checkNoLeak(t, name, text, c.sensitive)
			}

			checkSensitiveLines(t, &jsonBuf, c.sensitive)
		})
	}
}

// TestOIDCSecretNeverLogged proves that the client secret of the sign-in
// never shows in the start log, also when the settings have more than one
// layer.
func TestOIDCSecretNeverLogged(t *testing.T) {
	t.Parallel()

	secret := "oidc-" + canaryValue

	cases := []struct {
		name string
		env  map[string]string
		file cli.ConfigFile
	}{
		{
			name: "environment",
			env:  map[string]string{"DUSTPAN_OIDC_CLIENT_SECRET": secret},
			file: nil,
		},
		{
			name: "environment over file",
			env: map[string]string{
				"DUSTPAN_OIDC_CLIENT_SECRET": secret,
				"DUSTPAN_DATABASE":           "sqlite:" + secret + ".db",
			},
			file: cli.ConfigFile{"database": "sqlite:" + secret + "-file.db"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resolved, err := cli.Resolve(serveFlagSet(t), cli.ServeSettings(), mapLookup(tc.env), tc.file)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}

			var buf bytes.Buffer

			logger := slog.New(slog.NewJSONHandler(&buf, nil))
			cli.LogSettings(t.Context(), logger, resolved)
			cli.LogOverrides(t.Context(), logger, resolved)

			if form := leak(buf.String(), secret); form != "" {
				t.Fatalf("the start log holds %s of the client secret:\n%s", form, buf.String())
			}

			if tc.file != nil {
				if _, ok := linesByLevel(t, &buf, "WARN")["database"]; !ok {
					t.Fatalf("no WARN line for the database setting:\n%s", buf.String())
				}
			}
		})
	}
}

// TestResolveRefusesUserInfo proves a URL setting with user information
// stops the resolve in each layer, and the error never holds the value.
func TestResolveRefusesUserInfo(t *testing.T) {
	t.Parallel()

	value := "https://user:" + canaryValue + "@dustpan.example"

	cases := []struct {
		name string
		args []string
		env  map[string]string
		file cli.ConfigFile
	}{
		{"flag", []string{"--public-url=" + value}, nil, nil},
		{"env", nil, map[string]string{"DUSTPAN_S3_ENDPOINT": value}, nil},
		{"file", nil, nil, cli.ConfigFile{"backup-target": value}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := cli.Resolve(serveFlagSet(t, tc.args...), cli.ServeSettings(), mapLookup(tc.env), tc.file)
			if !errors.Is(err, cli.ErrURLUserInfo) {
				t.Fatalf("Resolve = %v, want %v", err, cli.ErrURLUserInfo)
			}

			if strings.Contains(err.Error(), canaryValue) {
				t.Errorf("error holds the value: %v", err)
			}
		})
	}
}

// TestLayeredLookup proves the lookup reads the environment first, then the
// file key of the declaration, and never the file for a variable with no
// file key.
func TestLayeredLookup(t *testing.T) {
	t.Parallel()

	file := cli.ConfigFile{"admin-token-file": "/from-file", "installation": "r"}

	lookup := cli.LayeredLookup(mapLookup(map[string]string{"DUSTPAN_INSTALLATION": "env"}), file)

	if got, ok := lookup("DUSTPAN_ADMIN_TOKEN_FILE"); !ok || got != "/from-file" {
		t.Errorf("DUSTPAN_ADMIN_TOKEN_FILE = %q, %v", got, ok)
	}

	if got, ok := lookup("DUSTPAN_INSTALLATION"); !ok || got != "env" {
		t.Errorf("DUSTPAN_INSTALLATION = %q, %v", got, ok)
	}

	if got, ok := lookup("DUSTPAN_ADMIN_TOKEN"); ok {
		t.Errorf("DUSTPAN_ADMIN_TOKEN = %q, want unset", got)
	}

	if got, ok := lookup("AWS_SECRET_ACCESS_KEY"); ok {
		t.Errorf("AWS_SECRET_ACCESS_KEY = %q, want unset", got)
	}
}

// TestChanges proves Changes answers each setting whose value changed, with
// the old and the new value, and that a changed sensitive value shows only
// set, with no text of either value.
func TestChanges(t *testing.T) {
	t.Parallel()

	resolve := func(file cli.ConfigFile) []cli.Resolved {
		t.Helper()

		resolved, err := cli.Resolve(serveFlagSet(t), cli.ServeSettings(), mapLookup(nil), file)
		if err != nil {
			t.Fatalf("Resolve: %v", err)
		}

		return resolved
	}

	old := resolve(
		cli.ConfigFile{"installation": "eu-west", "database": "sqlite:" + canaryValue + "-a", "listen": ":1"},
	)
	current := resolve(
		cli.ConfigFile{"installation": "us-east", "database": "sqlite:" + canaryValue + "-b", "listen": ":1"},
	)

	changes := cli.Changes(old, current)

	got := make([]string, 0, len(changes))
	for _, c := range changes {
		got = append(got, c.Key+":"+*c.Old+">"+c.New+"@"+c.Layer+"/"+c.TakesEffect)
	}

	want := []string{"database:set>set@file/next start", "installation:eu-west>us-east@file/next start"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("Changes = %v, want %v", got, want)
	}

	if strings.Contains(fmt.Sprintf("%+v", changes), canaryValue) {
		t.Fatal("a change holds a sensitive value")
	}

	same := resolve(
		cli.ConfigFile{"installation": "us-east", "database": "sqlite:" + canaryValue + "-b", "listen": ":1"},
	)

	if again := cli.Changes(current, same); len(again) != 0 {
		t.Fatalf("Changes of equal values = %+v, want none", again)
	}
}

// TestSnapshotEnvKeepsNoSecret proves the snapshot keeps the value of a
// setting that is not sensitive, keeps no form of a sensitive value, and
// that a resolve against the snapshot sees no change against a resolve
// against the real variables (the review of ADR 63 part 2, item 5).
func TestSnapshotEnvKeepsNoSecret(t *testing.T) {
	t.Parallel()

	secretDB := "sqlite:/var/lib/" + canaryValue + ".db"
	vars := map[string]string{
		"DUSTPAN_DATABASE":           secretDB,
		"DUSTPAN_ADMIN_TOKEN":        "token-" + canaryValue,
		"DUSTPAN_KEY_ENCRYPTION_KEY": "kek-" + canaryValue,
		"DUSTPAN_INSTALLATION":       "eu-west",
		"DUSTPAN_ADMIN_TOKEN_FILE":   " ",
	}

	snapshot := cli.SnapshotEnv(mapLookup(vars), cli.DeclaredEnvs())

	if got, ok := snapshot("DUSTPAN_INSTALLATION"); !ok || got != "eu-west" {
		t.Errorf("DUSTPAN_INSTALLATION = %q, %v; want eu-west", got, ok)
	}

	for _, key := range []string{"DUSTPAN_DATABASE", "DUSTPAN_ADMIN_TOKEN", "DUSTPAN_KEY_ENCRYPTION_KEY"} {
		got, ok := snapshot(key)
		if !ok || got == "" {
			t.Errorf("%s is not set in the snapshot", key)
		}

		if form := leak(got, vars[key]); form != "" {
			t.Errorf("the snapshot of %s holds %s of the value", key, form)
		}
	}

	start, err := cli.Resolve(serveFlagSet(t), cli.ServeSettings(), mapLookup(vars), nil)
	if err != nil {
		t.Fatalf("Resolve against the variables: %v", err)
	}

	reload, err := cli.Resolve(serveFlagSet(t), cli.ServeSettings(), snapshot, nil)
	if err != nil {
		t.Fatalf("Resolve against the snapshot: %v", err)
	}

	if changes := cli.Changes(start, reload); len(changes) != 0 {
		t.Fatalf("Changes(start, reload) = %+v, want none", changes)
	}

	other := cli.SnapshotEnv(mapLookup(map[string]string{"DUSTPAN_DATABASE": "sqlite:/other.db"}), cli.DeclaredEnvs())

	moved, err := cli.Resolve(serveFlagSet(t), cli.ServeSettings(), other, nil)
	if err != nil {
		t.Fatalf("Resolve against another snapshot: %v", err)
	}

	if changes := cli.Changes(start, moved); len(changes) == 0 {
		t.Fatal("a changed sensitive variable gave no change")
	}
}
