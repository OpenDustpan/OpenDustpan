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
		{"default", "region", nil, nil, nil, "local", cli.SourceDefault, 0},
		{"file", "region", nil, nil, cli.ConfigFile{"region": "f"}, "f", cli.SourceFile, 1},
		{
			"env over file", "region", nil,
			map[string]string{"DUSTPAN_REGION": "e"},
			cli.ConfigFile{"region": "f"},
			"e", cli.SourceEnv, 2,
		},
		{
			"flag over env and file", "region",
			[]string{"--region=x"},
			map[string]string{"DUSTPAN_REGION": "e"},
			cli.ConfigFile{"region": "f"},
			"x", cli.SourceFlag, 3,
		},
		{
			"empty env still wins", "region", nil,
			map[string]string{"DUSTPAN_REGION": ""},
			cli.ConfigFile{"region": "f"},
			"", cli.SourceEnv, 2,
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

	fs := serveFlagSet(t, "--region=same", "--listen=:9090")
	env := map[string]string{"DUSTPAN_REGION": "same", "DUSTPAN_LOG_LEVEL": "debug"}
	file := cli.ConfigFile{"region": "same", "log-level": "warn", "max-state-bytes": "7"}

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
		"region":          {"same", "flag"},
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
		"region":    {"layer": "flag", "overridden": "env,file", "flag": "same", "env": "same", "file": "same"},
		"log-level": {"layer": "env", "overridden": "file", "env": "debug", "file": "warn"},
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
		value := canaryValue + "-" + s.Name()

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
				if strings.Contains(text, canaryValue) {
					t.Errorf("%s holds a sensitive value:\n%s", name, text)
				}
			}

			checkSensitiveLines(t, &jsonBuf, c.sensitive)
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

	file := cli.ConfigFile{"admin-token-file": "/from-file", "region": "r"}

	lookup := cli.LayeredLookup(mapLookup(map[string]string{"DUSTPAN_REGION": "env"}), file)

	if got, ok := lookup("DUSTPAN_ADMIN_TOKEN_FILE"); !ok || got != "/from-file" {
		t.Errorf("DUSTPAN_ADMIN_TOKEN_FILE = %q, %v", got, ok)
	}

	if got, ok := lookup("DUSTPAN_REGION"); !ok || got != "env" {
		t.Errorf("DUSTPAN_REGION = %q, %v", got, ok)
	}

	if got, ok := lookup("DUSTPAN_ADMIN_TOKEN"); ok {
		t.Errorf("DUSTPAN_ADMIN_TOKEN = %q, want unset", got)
	}

	if got, ok := lookup("AWS_SECRET_ACCESS_KEY"); ok {
		t.Errorf("AWS_SECRET_ACCESS_KEY = %q, want unset", got)
	}
}
