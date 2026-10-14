package cli_test

import (
	"flag"
	"io"
	"slices"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

func TestListFlag(t *testing.T) {
	t.Setenv("DUSTPAN_AUDIT_SINK", "file:/a  webhook:https://h/x?a=1,2\nblob:audit")

	parse := func(args ...string) []string {
		t.Helper()

		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(io.Discard)

		var items []string

		cli.ByFlag("audit-sink").List(fs, &items)

		if err := fs.Parse(args); err != nil {
			t.Fatalf("parse %v: %v", args, err)
		}

		return items
	}

	if got, want := parse(), []string{"file:/a", "webhook:https://h/x?a=1,2", "blob:audit"}; !slices.Equal(got, want) {
		t.Fatalf("items of the variable: got %q, want %q", got, want)
	}

	got := parse("--audit-sink", "webhook:https://h/y?a=1,2", "--audit-sink", "file:/b")
	if want := []string{"webhook:https://h/y?a=1,2", "file:/b"}; !slices.Equal(got, want) {
		t.Fatalf("items of the flags: got %q, want %q", got, want)
	}
}

// TestListFlagThroughResolve proves that Resolve gives a List flag each item
// of its variable, never the whole value as one item, that a flag on the
// command line wins, and that the log value of the audit sinks is only set:
// a webhook URL can hold a token.
func TestListFlagThroughResolve(t *testing.T) {
	t.Parallel()

	env := map[string]string{"DUSTPAN_AUDIT_SINK": "file:/a  blob:audit"}

	cases := []struct {
		name string
		args []string
		want []string
	}{
		{"env", nil, []string{"file:/a", "blob:audit"}},
		{"flag", []string{"--audit-sink", "file:/b"}, []string{"file:/b"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			setting := cli.ByFlag("audit-sink")
			fs := flag.NewFlagSet("t", flag.ContinueOnError)
			fs.SetOutput(io.Discard)

			var items []string

			setting.List(fs, &items)

			if err := fs.Parse(tc.args); err != nil {
				t.Fatalf("parse %v: %v", tc.args, err)
			}

			lookup := func(key string) (string, bool) {
				value, ok := env[key]

				return value, ok
			}

			resolved, err := cli.Resolve(fs, []*cli.Setting{setting}, lookup, nil)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}

			if !slices.Equal(items, tc.want) {
				t.Errorf("items: got %q, want %q", items, tc.want)
			}

			if got := resolved[0].Value; got != "set" {
				t.Errorf("log value: got %q, want set", got)
			}
		})
	}
}
