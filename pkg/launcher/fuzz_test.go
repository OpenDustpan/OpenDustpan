package launcher_test

import (
	"errors"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/launcher"
	"opendustpan.org/opendustpan/pkg/runner"
)

// FuzzGitConfig checks the git configuration that GitConfig writes for any
// helper:
//   - a helper with a character that HelperRune refuses gives
//     ErrUnsafeHelper and no configuration;
//   - any other helper gives one [credential] section with the empty helper
//     line first and at most one more helper line, whose value holds only
//     the characters of HelperRune, so the value cannot add a line or a
//     section;
//   - each helper that the check of a pool takes gives a configuration.
func FuzzGitConfig(f *testing.F) {
	for _, s := range []string{
		"", "git-credential-store", "/usr/bin/helper", "!sh -c id", "git-credential-x\n[core]\n\tsshCommand = id",
		"a\rb", "a\x00b", "a;b", "a#b", `a"b`, "a\\b", "a b", "/a/../b", "git-credential-é",
	} {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, helper string) {
		unsafe := strings.ContainsFunc(helper, func(r rune) bool { return !runner.HelperRune(r) })

		config, err := launcher.GitConfig(helper)
		if err != nil {
			if !errors.Is(err, launcher.ErrUnsafeHelper) || config != nil || !unsafe {
				t.Fatalf(
					"GitConfig(%q) = %q, %v, want ErrUnsafeHelper only for a refused character",
					helper, config, err,
				)
			}

			return
		}

		if unsafe {
			t.Fatalf("GitConfig(%q) writes a configuration for a helper with a refused character", helper)
		}

		checkGitConfig(t, helper, string(config))

		pool := runner.GitCredentials{Mode: runner.GitCredentialsCustom, Helper: helper}
		if pool.Validate() == nil && !strings.Contains(string(config), "\thelper = ") {
			t.Fatalf("GitConfig(%q) = %q, want the helper line for a helper that a pool takes", helper, config)
		}
	})
}

// checkGitConfig fails t unless config is one [credential] section with the
// empty helper line and at most one helper line of HelperRune characters.
func checkGitConfig(t *testing.T, helper, config string) {
	t.Helper()

	lines := strings.Split(strings.TrimSuffix(config, "\n"), "\n")
	if !strings.HasSuffix(config, "\n") || len(lines) < 2 || len(lines) > 3 ||
		lines[0] != "[credential]" || lines[1] != "\thelper =" {
		t.Fatalf("GitConfig(%q) = %q, want one [credential] section that starts with the empty helper", helper, config)
	}

	if helper == "" && len(lines) != 2 {
		t.Fatalf("GitConfig(\"\") = %q, want only the empty helper", config)
	}

	if len(lines) == 3 {
		value, ok := strings.CutPrefix(lines[2], "\thelper = ")
		// An empty value is possible for the helper "git-credential-": git
		// reads it as one more reset of the helper list. The check of a pool
		// refuses that helper.
		if !ok || strings.ContainsFunc(value, func(r rune) bool { return !runner.HelperRune(r) }) {
			t.Fatalf("GitConfig(%q) has the line %q, want one helper value of allowed characters", helper, lines[2])
		}
	}
}
