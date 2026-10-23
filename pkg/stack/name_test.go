package stack_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/stack"
)

// TestParseNamesStateTheRule proves that a refused project or stack name gives
// ErrInvalidName with the rule of a name and an echo of at most 40 characters,
// and that an empty name gives the same text as an invalid one.
func TestParseNamesStateTheRule(t *testing.T) {
	t.Parallel()

	const rule = "use 1 to 100 ASCII letters, digits, -, _ and ., not starting with - or ."

	long := strings.Repeat("a", 300)
	cut := `"` + strings.Repeat("a", 40) + `"... (cut, 300 characters)`

	for _, tc := range []struct {
		parse func(string) error
		in    string
		want  string
	}{
		{parse: parseProject, in: "", want: `invalid project or stack name: project "": ` + rule},
		{parse: parseProject, in: "a b", want: `invalid project or stack name: project "a b": ` + rule},
		{parse: parseStack, in: "ok name", want: `invalid project or stack name: stack "ok name": ` + rule},
		{parse: parseStack, in: long, want: "invalid project or stack name: stack " + cut + ": " + rule},
	} {
		err := tc.parse(tc.in)
		if !errors.Is(err, stack.ErrInvalidName) || err.Error() != tc.want {
			t.Errorf("parse %.50q = %v, want %s", tc.in, err, tc.want)
		}
	}
}

func parseProject(s string) error {
	_, err := stack.ParseProjectName(s)

	return err
}

func parseStack(s string) error {
	_, err := stack.ParseName(s)

	return err
}
