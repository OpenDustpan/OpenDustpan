package cli_test

import (
	"errors"
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
		{"plain string", "region: eu-west\n", cli.ConfigFile{"region": "eu-west"}},
		{"no stays a string", "region: no\n", cli.ConfigFile{"region": "no"}},
		{"on stays a string", "region: on\n", cli.ConfigFile{"region": "on"}},
		{"yes stays a string", "region: yes\n", cli.ConfigFile{"region": "yes"}},
		{"quoted true is a string", "region: \"true\"\n", cli.ConfigFile{"region": "true"}},
		{"quoted number is a string", "listen: \"8080\"\n", cli.ConfigFile{"listen": "8080"}},
		{"empty string", "console-dir: \"\"\n", cli.ConfigFile{"console-dir": ""}},
		{"bool true", "disable-pulumi-journaling: true\n", cli.ConfigFile{"disable-pulumi-journaling": "true"}},
		{"bool false", "disable-pulumi-journaling: false\n", cli.ConfigFile{"disable-pulumi-journaling": "false"}},
		{"int", "max-state-bytes: 1048576\n", cli.ConfigFile{"max-state-bytes": "1048576"}},
		{"int zero", "max-state-bytes: 0\n", cli.ConfigFile{"max-state-bytes": "0"}},
		{
			"JSON shape with comments",
			"{\n  # the region\n  \"region\": \"eu-west\",\n  \"max-state-bytes\": 0,\n" +
				"  \"disable-pulumi-journaling\": true\n}\n",
			cli.ConfigFile{"region": "eu-west", "max-state-bytes": "0", "disable-pulumi-journaling": "true"},
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
		{"too large", "region: " + strings.Repeat("x", cli.MaxConfigBytes) + "\n", cli.ErrConfigTooLarge},
		{"bad syntax", "region: [" + canaryValue + "\n", cli.ErrConfigSyntax},
		{"two documents", "region: a\n---\nregion: b\n", cli.ErrConfigShape},
		{"scalar root", canaryValue + "\n", cli.ErrConfigShape},
		{"sequence root", "- " + canaryValue + "\n", cli.ErrConfigShape},
		{"alias bomb", aliasBomb, cli.ErrConfigAlias},
		{"anchor on a value", "region: &r " + canaryValue + "\n", cli.ErrConfigAlias},
		{"alias of a value", "region: &r " + canaryValue + "\nlisten: *r\n", cli.ErrConfigAlias},
		{"merge key", "base: &b {region: x}\n<<: *b\n", cli.ErrConfigAlias},
		{"duplicate key", "region: " + canaryValue + "\nregion: " + canaryValue + "\n", cli.ErrConfigDuplicate},
		{"duplicate key with equal value", "region: a\nregion: a\n", cli.ErrConfigDuplicate},
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
		{"string as int", "region: 12\n", cli.ErrConfigType},
		{"string as bool", "region: true\n", cli.ErrConfigType},
		{"string null", "region: null\n", cli.ErrConfigType},
		{"string sequence", "region: [" + canaryValue + "]\n", cli.ErrConfigType},
		{"string mapping", "region: {a: " + canaryValue + "}\n", cli.ErrConfigType},
		{"string binary tag", "region: !!binary Y2FuYXJ5\n", cli.ErrConfigType},
		{"string custom tag", "region: !secret " + canaryValue + "\n", cli.ErrConfigType},
		{"sensitive value of the wrong type", "database: [" + canaryValue + "]\n", cli.ErrConfigType},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := cli.ParseConfig([]byte(tc.text))
			if !errors.Is(err, tc.want) {
				t.Fatalf("ParseConfig = %v, %v; want %v", got, err, tc.want)
			}

			if strings.Contains(err.Error(), canaryValue) {
				t.Errorf("error holds the value: %v", err)
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

// TestReadConfigFile proves the read refuses a file larger than the cap
// and decodes a file at the cap.
func TestReadConfigFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	prefix := "region: "
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

	if len(got["region"]) != cli.MaxConfigBytes-len(prefix) {
		t.Errorf("region has %d bytes", len(got["region"]))
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

	if got["region"] != "eu-west" || got["disable-pulumi-journaling"] != "false" {
		t.Errorf("ParseConfig(ConfigExample) = %v", got)
	}
}
