package cli_test

import (
	"errors"
	"flag"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
)

// TestRefuseFlagPassword proves that only a database URL with a password, set
// in a flag, is refused. A variable is not a flag, so it passes. The error
// names the flag and the variable, and never shows the value.
func TestRefuseFlagPassword(t *testing.T) {
	t.Parallel()

	// fakePassword is not a credential. See withPassword for why it is joined at run time.
	const fakePassword = "s3cret"

	// The fake password is joined at run time, so no line of the source holds a
	// contiguous credential URL that a secret scanner reports.
	withPassword := "postgres://dustpan:" + fakePassword + "@db.internal/dustpan"

	cases := []struct {
		name    string
		args    []string
		env     map[string]string
		wantErr bool
	}{
		{name: "flag with a password", args: []string{"--database", withPassword}, env: nil, wantErr: true},
		{
			name:    "flag without a password",
			args:    []string{"--database", "postgres://db.internal/dustpan"},
			env:     nil,
			wantErr: false,
		},
		{
			name:    "variable with a password",
			args:    nil,
			env:     map[string]string{"DUSTPAN_DATABASE": withPassword},
			wantErr: false,
		},
		{name: "no database", args: nil, env: nil, wantErr: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			fs := serveFlagSet(t, tc.args...)

			resolved, err := cli.Resolve(fs, cli.ServeSettings(), mapLookup(tc.env), nil)
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}

			if tc.wantErr {
				requireRefusal(t, fs, resolved, fakePassword)
			} else {
				requireNoRefusal(t, fs, resolved)
			}
		})
	}
}

// requireNoRefusal fails when the flag refusal reports an error.
func requireNoRefusal(t *testing.T, fs *flag.FlagSet, resolved []cli.Resolved) {
	t.Helper()

	if err := cli.RefuseFlagPassword(fs, resolved); err != nil {
		t.Fatalf("RefuseFlagPassword = %v, want nil", err)
	}
}

// requireRefusal fails unless the flag refusal is the flag password error
// that names the flag and the variable and never shows the password.
func requireRefusal(t *testing.T, fs *flag.FlagSet, resolved []cli.Resolved, password string) {
	t.Helper()

	err := cli.RefuseFlagPassword(fs, resolved)
	if err == nil {
		t.Fatal("RefuseFlagPassword = nil, want an error")
	}

	if !errors.Is(err, cli.ErrFlagPassword) {
		t.Fatalf("RefuseFlagPassword = %v, want ErrFlagPassword", err)
	}

	if !strings.Contains(err.Error(), "--database") || !strings.Contains(err.Error(), "DUSTPAN_DATABASE") {
		t.Fatalf("RefuseFlagPassword = %q, want --database and DUSTPAN_DATABASE", err.Error())
	}

	if strings.Contains(err.Error(), password) {
		t.Fatalf("RefuseFlagPassword shows the password: %q", err.Error())
	}
}
