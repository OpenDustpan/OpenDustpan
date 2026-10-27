package cli_test

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

// TestParseConfigAccepts proves the values that the strict decoder takes and
// the text form it answers for each type.
func TestParseConfigAccepts(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		text string
		want cli.ConfigFile
	}{
		{"empty file", "", cli.ConfigFile{}},
		{"comment only", "# nothing\n", cli.ConfigFile{}},
		{"empty mapping", "{}", cli.ConfigFile{}},
		{"plain string", "installation: eu-west\n", cli.ConfigFile{"installation": "eu-west"}},
		{"no stays a string", "installation: no\n", cli.ConfigFile{"installation": "no"}},
		{"on stays a string", "installation: on\n", cli.ConfigFile{"installation": "on"}},
		{"yes stays a string", "installation: yes\n", cli.ConfigFile{"installation": "yes"}},
		{"quoted true is a string", "installation: \"true\"\n", cli.ConfigFile{"installation": "true"}},
		{"quoted number is a string", "listen: \"8080\"\n", cli.ConfigFile{"listen": "8080"}},
		{"empty string", "console-dir: \"\"\n", cli.ConfigFile{"console-dir": ""}},
		{"bool true", "disable-pulumi-journaling: true\n", cli.ConfigFile{"disable-pulumi-journaling": "true"}},
		{"bool false", "disable-pulumi-journaling: false\n", cli.ConfigFile{"disable-pulumi-journaling": "false"}},
		{"int", "max-state-bytes: 1048576\n", cli.ConfigFile{"max-state-bytes": "1048576"}},
		{"int zero", "max-state-bytes: 0\n", cli.ConfigFile{"max-state-bytes": "0"}},
		{"database without a password", "database: postgres://dustpan@db.example/dustpan\n", cli.ConfigFile{
			"database": "postgres://dustpan@db.example/dustpan",
		}},
		{"database SQLite path", "database: \"sqlite:/var/lib/dustpan.db\"\n", cli.ConfigFile{
			"database": "sqlite:/var/lib/dustpan.db",
		}},
		{
			"JSON shape with comments",
			"{\n  # the installation\n  \"installation\": \"eu-west\",\n  \"max-state-bytes\": 0,\n" +
				"  \"disable-pulumi-journaling\": true\n}\n",
			cli.ConfigFile{"installation": "eu-west", "max-state-bytes": "0", "disable-pulumi-journaling": "true"},
		},
		{"key for a variable", "admin-token-file: /run/secrets/tokens\n", cli.ConfigFile{
			"admin-token-file": "/run/secrets/tokens",
		}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := cli.ParseConfig([]byte(tc.text))
			if err != nil {
				t.Fatalf("ParseConfig: %v", err)
			}

			if len(got) != len(tc.want) {
				t.Fatalf("got %d keys, want %d: %v", len(got), len(tc.want), got)
			}

			for key, want := range tc.want {
				if got[key] != want {
					t.Errorf("%s = %q, want %q", key, got[key], want)
				}
			}
		})
	}
}

// canaryValue is a value that no error message and no log line may hold.
const canaryValue = "canary-3f9a7c"

// leak answers the form of value that text holds: the value, its last 8
// characters or the hex SHA-256 of the value. It answers "" when text holds
// none of them.
func leak(text, value string) string {
	sum := sha256.Sum256([]byte(value))

	forms := map[string]string{
		"the value":             value,
		"its last 8 characters": value[max(0, len(value)-8):],
		"its SHA-256":           hex.EncodeToString(sum[:]),
	}

	for name, form := range forms {
		if strings.Contains(text, form) {
			return name
		}
	}

	return ""
}

// TestParseConfigRefuses proves each rule of ADR 63 decision 3 and that no
// error holds the refused value.
func TestParseConfigRefuses(t *testing.T) {
	t.Parallel()

	aliasBomb := strings.Join([]string{
		`a: &a ["` + canaryValue + `","` + canaryValue + `","` + canaryValue + `"]`,
		`b: &b [*a,*a,*a,*a,*a,*a,*a,*a,*a]`,
		`c: &c [*b,*b,*b,*b,*b,*b,*b,*b,*b]`,
		`d: &d [*c,*c,*c,*c,*c,*c,*c,*c,*c]`,
		`e: &e [*d,*d,*d,*d,*d,*d,*d,*d,*d]`,
		`f: &f [*e,*e,*e,*e,*e,*e,*e,*e,*e]`,
		`g: &g [*f,*f,*f,*f,*f,*f,*f,*f,*f]`,
		`h: &h [*g,*g,*g,*g,*g,*g,*g,*g,*g]`,
		`i: &i [*h,*h,*h,*h,*h,*h,*h,*h,*h]`,
		"",
	}, "\n")

	cases := []struct {
		name string
		text string
		want error
	}{
		{"too large", "installation: " + strings.Repeat("x", cli.MaxConfigBytes) + "\n", cli.ErrConfigTooLarge},
		{"bad syntax", "installation: [" + canaryValue + "\n", cli.ErrConfigSyntax},
		{"two documents", "installation: a\n---\ninstallation: b\n", cli.ErrConfigShape},
		{"scalar root", canaryValue + "\n", cli.ErrConfigShape},
		{"sequence root", "- " + canaryValue + "\n", cli.ErrConfigShape},
		{"alias bomb", aliasBomb, cli.ErrConfigAlias},
		{"anchor on a value", "installation: &r " + canaryValue + "\n", cli.ErrConfigAlias},
		{"alias of a value", "installation: &r " + canaryValue + "\nlisten: *r\n", cli.ErrConfigAlias},
		{"merge key", "base: &b {installation: x}\n<<: *b\n", cli.ErrConfigAlias},
		{"duplicate key", "installation: " + canaryValue + "\ninstallation: " + canaryValue + "\n", cli.ErrConfigDuplicate},
		{"duplicate key with equal value", "installation: a\ninstallation: a\n", cli.ErrConfigDuplicate},
		{"unknown key", "regoin: " + canaryValue + "\n", cli.ErrConfigUnknown},
		{"flag without a file key", "server: " + canaryValue + "\n", cli.ErrConfigUnknown},
		{"secret without a file key", "DUSTPAN_ADMIN_TOKEN: " + canaryValue + "\n", cli.ErrConfigUnknown},
		{"nested key", "log:\n  level: " + canaryValue + "\n", cli.ErrConfigUnknown},
		{"int key", "1: " + canaryValue + "\n", cli.ErrConfigShape},
		{"bool yes", "disable-pulumi-journaling: yes\n", cli.ErrConfigType},
		{"bool no", "disable-pulumi-journaling: no\n", cli.ErrConfigType},
		{"bool on", "disable-pulumi-journaling: on\n", cli.ErrConfigType},
		{"bool True", "disable-pulumi-journaling: True\n", cli.ErrConfigType},
		{"bool 1", "disable-pulumi-journaling: 1\n", cli.ErrConfigType},
		{"bool quoted", "disable-pulumi-journaling: \"true\"\n", cli.ErrConfigType},
		{"bool null", "disable-pulumi-journaling:\n", cli.ErrConfigType},
		{"int as string", "max-state-bytes: \"10\"\n", cli.ErrConfigType},
		{"int negative", "max-state-bytes: -1\n", cli.ErrConfigType},
		{"int hex", "max-state-bytes: 0x10\n", cli.ErrConfigType},
		{"int octal", "max-state-bytes: 0o10\n", cli.ErrConfigType},
		{"int float", "max-state-bytes: 1.5\n", cli.ErrConfigType},
		{"int too large", "max-state-bytes: 99999999999999999999\n", cli.ErrConfigType},
		{"string as int", "installation: 12\n", cli.ErrConfigType},
		{"string as bool", "installation: true\n", cli.ErrConfigType},
		{"string null", "installation: null\n", cli.ErrConfigType},
		{"string sequence", "installation: [" + canaryValue + "]\n", cli.ErrConfigType},
		{"string mapping", "installation: {a: " + canaryValue + "}\n", cli.ErrConfigType},
		{"string binary tag", "installation: !!binary Y2FuYXJ5\n", cli.ErrConfigType},
		{"string custom tag", "installation: !secret " + canaryValue + "\n", cli.ErrConfigType},
		{"sensitive value of the wrong type", "database: [" + canaryValue + "]\n", cli.ErrConfigType},
		{"database with a password", "database: postgres://u:" + canaryValue + "@db/d\n", cli.ErrConfigPassword},
		{
			"database that is not a URL",
			"database: \"postgres://u:" + canaryValue + "@d b:x/d\"\n",
			cli.ErrConfigPassword,
		},
		{"int leading zero", "max-state-bytes: 07\n", cli.ErrConfigType},
		{"int two zeros", "max-state-bytes: 00\n", cli.ErrConfigType},
		{"int leading plus", "max-state-bytes: +7\n", cli.ErrConfigType},
		{"int minus zero", "max-state-bytes: -0\n", cli.ErrConfigType},
		{"int underscore", "max-state-bytes: 1_000\n", cli.ErrConfigType},
		{"unknown anchor", "installation: *" + canaryValue + "\n", cli.ErrConfigSyntax},
		{"syntax error after a value", "installation: \"" + canaryValue + "\" x: [\n", cli.ErrConfigSyntax},
		{"tab in indentation", "installation: x\n\t" + canaryValue + ": y\n", cli.ErrConfigSyntax},
		{"long unknown key", strings.Repeat("k", 32) + canaryValue + ": x\n", cli.ErrConfigUnknown},
		{"date for a string", "installation: 2026-10-14\n", cli.ErrConfigType},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := cli.ParseConfig([]byte(tc.text))
			if !errors.Is(err, tc.want) {
				t.Fatalf("ParseConfig = %v, %v; want %v", got, err, tc.want)
			}

			if form := leak(err.Error(), canaryValue); form != "" {
				t.Errorf("error holds %s of the value: %v", form, err)
			}
		})
	}
}

// TestParseConfigTypeErrorNamesKeyAndRule proves a type error names the key
// and the rule.
func TestParseConfigTypeErrorNamesKeyAndRule(t *testing.T) {
	t.Parallel()

	_, err := cli.ParseConfig([]byte("disable-pulumi-journaling: no\n"))
	if err == nil {
		t.Fatal("ParseConfig accepted no for a bool")
	}

	for _, want := range []string{`"disable-pulumi-journaling"`, "line 1", "want true or false"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not hold %q", err, want)
		}
	}
}

// TestDecoderErrorsNeverHoldTheValue is the canary for decoder errors: for
// each setting that the file can set, it puts a canary value in each form of
// refused file, and proves that no error holds the value, its last 8
// characters or its SHA-256.
func TestDecoderErrorsNeverHoldTheValue(t *testing.T) {
	t.Parallel()

	templates := []string{
		"%s: [%s]\n",            // a sequence for a scalar
		"%s: {v: %s}\n",         // a mapping for a scalar
		"%s: &a %s\n",           // an anchor
		"%s: *%s\n",             // an unknown alias
		"%s: !custom %s\n",      // a custom tag
		"%s: \"%s\" trailing\n", // a syntax error after the value
		"%s: \"%s\n",            // a string that does not end
		"%s: x\n%[1]s: %s\n",    // a duplicate key
	}

	for _, s := range cli.Settings {
		if s.File == "" {
			continue
		}

		for _, tmpl := range templates {
			value := "postgres://u:" + canaryValue + "@db/d"
			text := fmt.Sprintf(tmpl, s.File, value)

			_, err := cli.ParseConfig([]byte(text))
			if err == nil {
				// A string setting takes some of these texts as a valid
				// string. Then no error exists that can leak.
				continue
			}

			if form := leak(err.Error(), value); form != "" {
				t.Errorf("%s: error %q holds %s of the value", s.File, err, form)
			}
		}
	}
}

// TestParseConfigErrorShape proves a syntax error answers the line and a
// fixed rule, never the message of the YAML library, and that an error cuts
// a long key to 32 characters.
func TestParseConfigErrorShape(t *testing.T) {
	t.Parallel()

	_, err := cli.ParseConfig([]byte("installation: a\nlisten: *unknownAnchorName\n"))
	if err == nil || err.Error() != "config file is not valid YAML" && !strings.HasPrefix(err.Error(),
		"config file is not valid YAML: line ") {
		t.Fatalf("syntax error = %v, want the fixed rule text", err)
	}

	if strings.Contains(err.Error(), "unknownAnchorName") || strings.Contains(err.Error(), "anchor") {
		t.Fatalf("syntax error %q echoes the file", err)
	}

	_, err = cli.ParseConfig([]byte(strings.Repeat("a", 40) + ": x\n"))
	if err == nil || !strings.Contains(err.Error(), `"`+strings.Repeat("a", 32)+`" (cut)`) ||
		strings.Contains(err.Error(), strings.Repeat("a", 33)) {
		t.Fatalf("unknown key error = %v, want the key cut to 32 characters", err)
	}

	// The URL is built in parts, so no commit holds a contiguous URL with a
	// password: make trufflehog scans every branch of the history.
	_, err = cli.ParseConfig([]byte("database: postgres://u:" + canaryValue + "@db/d\n"))
	if err == nil || !strings.Contains(err.Error(), "DUSTPAN_DATABASE") ||
		!strings.Contains(err.Error(), `"database"`) {
		t.Fatalf("password error = %v, want the key and DUSTPAN_DATABASE", err)
	}
}

// TestReadConfigFile proves the read refuses a file larger than the cap
// and decodes a file at the cap.
func TestReadConfigFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	prefix := "installation: "
	atCap := prefix + strings.Repeat("x", cli.MaxConfigBytes-len(prefix))
	overCap := atCap + "x"

	atPath := filepath.Join(dir, "at.yaml")
	overPath := filepath.Join(dir, "over.yaml")

	for path, text := range map[string]string{atPath: atCap, overPath: overCap} {
		if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	got, err := cli.ReadConfigFile(atPath)
	if err != nil {
		t.Fatalf("ReadConfigFile at the cap: %v", err)
	}

	if len(got["installation"]) != cli.MaxConfigBytes-len(prefix) {
		t.Errorf("installation has %d bytes", len(got["installation"]))
	}

	if _, err := cli.ReadConfigFile(overPath); !errors.Is(err, cli.ErrConfigTooLarge) {
		t.Errorf("ReadConfigFile over the cap = %v, want %v", err, cli.ErrConfigTooLarge)
	}

	if _, err := cli.ReadConfigFile(filepath.Join(dir, "missing.yaml")); err == nil {
		t.Error("ReadConfigFile of a missing file answered no error")
	}
}

// TestConfigExampleDecodes proves the example of the reference is a file
// that the strict decoder takes.
func TestConfigExampleDecodes(t *testing.T) {
	t.Parallel()

	got, err := cli.ParseConfig([]byte(cli.ConfigExample))
	if err != nil {
		t.Fatalf("ParseConfig(ConfigExample): %v", err)
	}

	if got["installation"] != "eu-west" || got["disable-pulumi-journaling"] != "false" {
		t.Errorf("ParseConfig(ConfigExample) = %v", got)
	}
}

// TestParseConfigRefusesUnservedRetentionKeys proves a retention key stays
// refused until serve registers the setting.
func TestParseConfigRefusesUnservedRetentionKeys(t *testing.T) {
	t.Parallel()

	for _, key := range []string{"plan-retention", "job-log-retention"} {
		_, err := cli.ParseConfig([]byte(key + ": \"1h\"\n"))
		if !errors.Is(err, cli.ErrConfigUnknown) {
			t.Errorf("ParseConfig(%s) = %v, want ErrConfigUnknown", key, err)
		}
	}
}
