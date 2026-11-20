package cli_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

// TestDatabaseAllowWideFileModeSetting proves the opt-out is off by default
// and that its effective value and its layer reach the resolved settings, which
// the start log and the start audit entry show.
func TestDatabaseAllowWideFileModeSetting(t *testing.T) {
	t.Parallel()

	const key = "database-allow-wide-file-mode"

	cases := []struct {
		name       string
		args       []string
		env        map[string]string
		wantValue  string
		wantSource cli.Source
	}{
		{"default is off", nil, nil, "false", cli.SourceDefault},
		{
			"variable turns it on", nil,
			map[string]string{"DUSTPAN_DATABASE_ALLOW_WIDE_FILE_MODE": "true"},
			"true", cli.SourceEnv,
		},
		{"flag turns it on", []string{"--" + key}, nil, "true", cli.SourceFlag},
		{
			"flag wins over the variable",
			[]string{"--" + key + "=false"},
			map[string]string{"DUSTPAN_DATABASE_ALLOW_WIDE_FILE_MODE": "true"},
			"false", cli.SourceFlag,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resolved, err := cli.Resolve(serveFlagSet(t, tc.args...), cli.ServeSettings(), mapLookup(tc.env), nil)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}

			got := resolvedFor(t, resolved, key)
			if got.Value != tc.wantValue || got.Source != tc.wantSource {
				t.Fatalf("%s = %q from %q, want %q from %q", key, got.Value, got.Source, tc.wantValue, tc.wantSource)
			}
		})
	}
}
