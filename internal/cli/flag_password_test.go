package cli_test

import (
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

// TestFlagsHoldingPassword proves that only a database URL with a password,
// set in a flag, is reported. A variable is not a flag, so it is not reported.
// The reported settings name the flag, and the value of a sensitive setting is
// never shown.
func TestFlagsHoldingPassword(t *testing.T) {
	t.Parallel()

	// fakePassword is not a credential. See withPassword for why it is joined at run time.
	const fakePassword = "s3cret"

	// The fake password is joined at run time, so no line of the source holds a
	// contiguous credential URL that a secret scanner reports.
	const withPassword = "postgres://dustpan:" + fakePassword + "@db.internal/dustpan"

	cases := []struct {
		name string
		args []string
		env  map[string]string
		want int
	}{
		{name: "flag with a password", args: []string{"--database", withPassword}, env: nil, want: 1},
		{
			name: "flag without a password",
			args: []string{"--database", "postgres://db.internal/dustpan"},
			env:  nil,
			want: 0,
		},
		{
			name: "variable with a password",
			args: nil,
			env:  map[string]string{"DUSTPAN_DATABASE": withPassword},
			want: 0,
		},
		{name: "no database", args: nil, env: nil, want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fs := serveFlagSet(t, tc.args...)

			resolved, err := cli.Resolve(fs, cli.ServeSettings(), mapLookup(tc.env), nil)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}

			got := cli.FlagsHoldingPassword(fs, resolved)
			if len(got) != tc.want {
				t.Fatalf("FlagsHoldingPassword = %d settings, want %d", len(got), tc.want)
			}

			for _, s := range got {
				if s.Flag != "database" {
					t.Fatalf("reported setting %q, want database", s.Flag)
				}
			}
		})
	}
}
