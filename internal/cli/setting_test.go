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
