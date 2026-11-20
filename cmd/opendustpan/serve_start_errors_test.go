package main

import (
	"strings"
	"testing"
)

// TestServeStartErrorsNameFlagAndFix proves that three start errors of serve
// name the flag, the variable and the one fix, before the database opens.
func TestServeStartErrorsNameFlagAndFix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T) error
		want []string
	}{
		{
			"public URL with a scheme that is not http or https",
			func(t *testing.T) error {
				t.Helper()

				return parseCheckFlags(t, "--public-url", "ftp://x").check()
			},
			[]string{"--public-url", "DUSTPAN_PUBLIC_URL", "use an absolute http or https URL"},
		},
		{
			"public URL with a query",
			func(t *testing.T) error {
				t.Helper()

				return parseCheckFlags(t, "--public-url", "https://x.example.com/?a=b").check()
			},
			[]string{"--public-url", "DUSTPAN_PUBLIC_URL", "use an absolute http or https URL"},
		},
		{
			"listen port above 65535",
			func(t *testing.T) error {
				t.Helper()

				return parseCheckFlags(t, "--listen", "127.0.0.1:99999").check()
			},
			[]string{"--listen", "DUSTPAN_LISTEN", "the port must be a number from 0 to 65535"},
		},
		{
			"listen port with too many digits",
			func(t *testing.T) error {
				t.Helper()

				return parseCheckFlags(t, "--listen", ":99999999999999999999").check()
			},
			[]string{"--listen", "DUSTPAN_LISTEN", "the port must be a number from 0 to 65535"},
		},
		{
			"empty sqlite path",
			func(t *testing.T) error {
				t.Helper()

				return checkDatabaseFlag("sqlite:")
			},
			[]string{"--database", "DUSTPAN_DATABASE", "is empty", "write the path after sqlite:"},
		},
		{
			"blank sqlite path",
			func(t *testing.T) error {
				t.Helper()

				return checkDatabaseFlag("sqlite:  ")
			},
			[]string{"--database", "DUSTPAN_DATABASE", "is empty", "write the path after sqlite:"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.run(t)
			if err == nil {
				t.Fatal("want an error, got nil")
			}

			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

// TestServeStartErrorsKeepGoodValues proves that the three checks keep a valid
// value, so the guard is not a refusal of everything.
func TestServeStartErrorsKeepGoodValues(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"--listen", "127.0.0.1:0"},
		{"--listen", "127.0.0.1:65535"},
		{"--public-url", "https://dustpan.example.com"},
		{"--public-url", ""},
	} {
		if err := parseCheckFlags(t, args...).checkNames(); err != nil {
			t.Errorf("checkNames(%q): %v", args, err)
		}
	}

	if err := checkDatabaseFlag("sqlite:x.db"); err != nil {
		t.Errorf("checkDatabaseFlag: %v", err)
	}
}
