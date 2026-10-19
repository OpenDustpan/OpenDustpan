package cli_test

import (
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

// resolvedFrom returns a resolved setting of the flag from source.
func resolvedFrom(flag string, source cli.Source) cli.Resolved {
	return cli.Resolved{Setting: cli.ByFlag(flag), Source: source, Value: "", Layers: nil}
}

// unreadCase is one case of TestUnreadFlags.
type unreadCase struct {
	name     string
	selected []string
	want     []string
}

// TestUnreadFlags proves a setting that a layer sets and that no selected
// component reads is reported, and a default value is not.
func TestUnreadFlags(t *testing.T) {
	t.Parallel()

	resolved := []cli.Resolved{
		resolvedFrom("console-dir", cli.SourceFlag),
		resolvedFrom("audit-sink", cli.SourceEnv),
		resolvedFrom("listen", cli.SourceFlag),
		resolvedFrom("max-state-bytes", cli.SourceDefault),
	}

	cases := []unreadCase{
		{name: "all read everything", selected: []string{cli.ComponentAPI, cli.ComponentWorker}, want: nil},
		{name: "worker ignores the api flags", selected: []string{cli.ComponentWorker}, want: []string{"console-dir"}},
		{name: "api ignores the worker flags", selected: []string{cli.ComponentAPI}, want: []string{"audit-sink"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := cli.UnreadFlags(resolved, tc.selected)
			if !slices.Equal(got, tc.want) {
				t.Fatalf("UnreadFlags = %v, want %v", got, tc.want)
			}
		})
	}
}
