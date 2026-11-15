package pulumi_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/api/pulumi"
)

// TestAcceptedGitHead checks the allowlist of the commit that the CLI reports:
// only exactly 40 or exactly 64 lowercase hexadecimal characters pass, and
// every other value becomes the empty string.
func TestAcceptedGitHead(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "40 lowercase hex",
			input: "0123456789abcdef0123456789abcdef01234567",
			want:  "0123456789abcdef0123456789abcdef01234567",
		},
		{
			name:  "64 lowercase hex",
			input: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
			want:  "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		},
		{name: "uppercase hex", input: strings.Repeat("A", 40), want: ""},
		{name: "39 characters", input: strings.Repeat("a", 39), want: ""},
		{name: "41 characters", input: strings.Repeat("a", 41), want: ""},
		{name: "carriage return inside", input: strings.Repeat("a", 39) + "\r", want: ""},
		{name: "line feed inside", input: strings.Repeat("a", 39) + "\n", want: ""},
		{name: "space", input: strings.Repeat("a", 39) + " ", want: ""},
		{name: "branch name", input: "refs/heads/main", want: ""},
		{name: "1 MiB", input: strings.Repeat("a", 1<<20), want: ""},
		{name: "non-ASCII hex lookalike digit", input: strings.Repeat("a", 37) + "\uff10", want: ""},
		{name: "empty", input: "", want: ""},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()

			if got := pulumi.CheckGitHead(row.input); got != row.want {
				t.Fatalf("got %q, want %q", got, row.want)
			}
		})
	}
}
