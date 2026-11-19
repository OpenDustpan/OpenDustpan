package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

// TestUnknownCommandNamesTheProblem proves that a command line that matches
// no command gives a short error that names the word and the one command that
// helps, and not the whole usage. The error still matches the usage error.
func TestUnknownCommandNamesTheProblem(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"unknown word", []string{"bogus"}, `unknown command "bogus"`},
		{"incomplete command", []string{"key"}, `"key" is not complete or is not known`},
		{"unknown subcommand", []string{"collection", "no-such"}, `"collection" is not complete or is not known`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer

			err := run(t.Context(), tt.args, &stdout, &stderr)
			if !errors.Is(err, errUsage) {
				t.Fatalf("run(%v) = %v, want it to match the usage error", tt.args, err)
			}

			if !strings.Contains(err.Error(), tt.want) || !strings.Contains(err.Error(), "opendustpan --help") {
				t.Fatalf("run(%v) = %q, want %q and the help command", tt.args, err, tt.want)
			}

			if strings.Contains(err.Error(), "Try out:") || strings.Contains(err.Error(), "\n") {
				t.Fatalf("run(%v) = %q, want a short error with no usage text", tt.args, err)
			}
		})
	}
}
