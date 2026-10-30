//go:build e2e

package e2e_test

import (
	"testing"

	"opendustpan.org/opendustpan/e2e"
)

func TestConsoleLinkChecksTheHostAndLetsThePathVary(t *testing.T) {
	t.Parallel()

	const host = "http://app.dustpan.test"

	cases := []struct {
		name   string
		stdout string
		want   bool
	}{
		{name: "host alone", stdout: "Logged in to x as admin (http://app.dustpan.test)\n", want: true},
		{name: "host and subject", stdout: "Logged in to x as admin (http://app.dustpan.test/admin)\n", want: true},
		{name: "other path", stdout: "as admin (http://app.dustpan.test/other)\n", want: false},
		{name: "other host", stdout: "as admin (http://app.dustpan.test.evil.test)\n", want: false},
		{name: "other scheme", stdout: "as admin (https://app.dustpan.test)\n", want: false},
		{name: "no link", stdout: "as admin ()\n", want: false},
		{name: "empty", stdout: "", want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			result := e2e.Result{Command: "", Stdout: tc.stdout, Stderr: "", Duration: 0, ExitCode: 0}

			if got := consoleLink(host)(result) == nil; got != tc.want {
				t.Fatalf("consoleLink(%q) on %q accepted = %t, want %t", host, tc.stdout, got, tc.want)
			}
		})
	}
}
