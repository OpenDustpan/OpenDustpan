package runner_test

import (
	"errors"
	"path"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/runner"
)

// FuzzGitCredentialsValidate checks that each git credential setting that
// Validate takes has a known mode, and that a custom helper is a bare
// git-credential- name or a clean absolute path of the characters that
// HelperRune allows. A refused helper wraps ErrInvalid, and its error does
// not hold the helper.
func FuzzGitCredentialsValidate(f *testing.F) {
	for _, s := range [][2]string{
		{"", ""},
		{"none", ""},
		{"forge", ""},
		{"custom", "git-credential-store"},
		{"custom", "/usr/bin/helper"},
		{"custom", "!sh -c id"},
		{"custom", "git-credential-x\n[core]\n\tsshCommand = id"},
		{"custom", "/a/../b"},
		{"custom", "/a/"},
		{"custom", "git-credential-"},
		{"custom", "git-credential-a/b"},
		{"custom", "/a\rb"},
		{"forge", "git-credential-store"},
		{"other", ""},
	} {
		f.Add(s[0], s[1])
	}

	f.Fuzz(func(t *testing.T, mode, helper string) {
		setting := runner.GitCredentials{Mode: runner.GitCredentialMode(mode), Helper: helper}

		err := setting.Validate()
		if err != nil {
			if !errors.Is(err, runner.ErrInvalid) {
				t.Fatalf("Validate(%q, %q) = %v, want an error that wraps ErrInvalid", mode, helper, err)
			}

			// The fixed text of ErrInvalidHelper holds "git-credential-", so
			// only a helper that is not a part of that text counts.
			if len(helper) >= 8 && strings.Contains(err.Error(), helper) &&
				!strings.Contains(runner.ErrInvalidHelper.Error(), helper) {
				t.Fatalf("Validate(%q, %q): the error holds the helper", mode, helper)
			}

			return
		}

		switch setting.Mode {
		case "", runner.GitCredentialsNone, runner.GitCredentialsForge:
			if helper != "" {
				t.Fatalf("Validate(%q, %q) takes a helper for a mode that has none", mode, helper)
			}
		case runner.GitCredentialsCustom:
			checkCustomHelper(t, helper)
		default:
			t.Fatalf("Validate(%q, %q) takes an unknown mode", mode, helper)
		}
	})
}

// checkCustomHelper fails t unless helper has only the characters of
// HelperRune and is a bare git-credential- name or a clean absolute path.
func checkCustomHelper(t *testing.T, helper string) {
	t.Helper()

	if strings.ContainsFunc(helper, func(r rune) bool { return !runner.HelperRune(r) }) {
		t.Fatalf("Validate takes the helper %q with a character that HelperRune refuses", helper)
	}

	name, bare := strings.CutPrefix(helper, "git-credential-")
	bare = bare && name != "" && !strings.Contains(helper, "/")
	absolute := path.IsAbs(helper) && path.Clean(helper) == helper && helper != "/"

	if !bare && !absolute {
		t.Fatalf("Validate takes the helper %q, which is not a bare name or a clean absolute path", helper)
	}
}
