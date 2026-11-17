package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The tests in this file send hostile input to the settings layer of the
// LDAP sign-in, as if the flag parser, the variables and the file keys were
// the only layer between the input and the directory.

// subjectLimitFile writes a configuration file that sets the lockout limit to
// value, and returns its path.
func subjectLimitFile(t *testing.T, value string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "dustpan.yaml")
	if err := os.WriteFile(path, []byte("ldap.subjectFailureLimit: "+value+"\n"), 0o600); err != nil {
		t.Fatalf("write the configuration file: %v", err)
	}

	return path
}

// subjectLimitArgs returns the flags of a valid provider.
func subjectLimitArgs() []string {
	return []string{
		"--ldap-url", "ldaps://ldap.example.org",
		"--ldap-provider", "corp-ldap",
		"--ldap-bind-dn", "cn=dustpan,dc=example,dc=org",
		"--ldap-base-dn", "dc=example,dc=org",
		"--ldap-user-filter", "(uid={user})",
	}
}

// subjectLimitLayer returns the flags and the variables that set the lockout
// limit through layer, and writes the file of the file layer. The layer
// "none" sets no limit.
func subjectLimitLayer(t *testing.T, f *serveFlags, args []string, layer, value string) ([]string, map[string]string) {
	t.Helper()

	vars := map[string]string{ldapBindEnvVar: ldapTestPassword}

	switch layer {
	case "none":
	case "flag":
		args = append(args, "--"+ldapSubjectFailureLimitFlag+"="+value)
	case "variable":
		vars["DUSTPAN_LDAP_SUBJECT_FAILURE_LIMIT"] = value
	case "file":
		f.config = subjectLimitFile(t, value)
	default:
		t.Fatalf("unknown layer %q", layer)
	}

	return args, vars
}

// startSubjectLimit starts the settings with args and the limit of layer, and
// returns the error of the start, or nil.
func startSubjectLimit(t *testing.T, args []string, layer, value string) error {
	t.Helper()

	var f serveFlags

	fs := newTestFlagSet(&f)
	args, vars := subjectLimitLayer(t, &f, args, layer, value)
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse the flags: %w", err)
	}

	env := &fakeEnv{vars: vars, cleared: nil, reads: 0}
	if err := f.resolveLayers(fs, env.get); err != nil {
		return err
	}

	opts, err := f.ldapOptions(env.get, env.unset)
	if err == nil && len(opts) != 1 {
		t.Fatalf("ldapOptions = %d options, want 1", len(opts))
	}

	return err
}

// TestLDAPSubjectFailureLimitRefusesAHostileValueFromEachLayer proves that
// no layer puts a limit outside the range into a running server: the flag,
// the variable and the file key each stop the start, and the error names the
// setting.
func TestLDAPSubjectFailureLimitRefusesAHostileValueFromEachLayer(t *testing.T) {
	t.Parallel()

	for _, layer := range []string{"flag", "variable", "file"} {
		for _, value := range []string{"0", "-1", "101", "two"} {
			t.Run(layer+" "+value, func(t *testing.T) {
				t.Parallel()

				err := startSubjectLimit(t, subjectLimitArgs(), layer, value)

				want := ldapSubjectFailureLimitKey
				if layer == "flag" && value == "two" {
					// The flag parser refuses a value that is not a number
					// before the settings guard runs. It names the flag.
					want = ldapSubjectFailureLimitFlag
				}

				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("start with %s %q = %v, want an error that names %s", layer, value, err, want)
				}
			})
		}
	}
}

// providerLimit returns the limit and the source that the provider of the
// settings carries for layer.
func providerLimit(t *testing.T, layer, value string) (int, string) {
	t.Helper()

	var f serveFlags

	fs := newTestFlagSet(&f)
	args, vars := subjectLimitLayer(t, &f, subjectLimitArgs(), layer, value)
	if err := fs.Parse(args); err != nil {
		t.Fatalf("parse the flags: %v", err)
	}

	env := &fakeEnv{vars: vars, cleared: nil, reads: 0}
	if err := f.resolveLayers(fs, env.get); err != nil {
		t.Fatalf("resolve the settings: %v", err)
	}

	opts, err := f.ldapOptions(env.get, env.unset)
	if err != nil || len(opts) != 1 {
		t.Fatalf("ldapOptions = %d options, %v; want 1, nil", len(opts), err)
	}

	source, _ := ldapSubjectFailureLimitSource(f.resolved)

	p, err := f.ldap.provider(ldapTestPassword, source)
	if err != nil {
		t.Fatalf("provider: %v", err)
	}

	return p.SubjectFailureLimit, p.SubjectFailureLimitSource
}

// TestLDAPSubjectFailureLimitReachesTheProvider proves that the bounds pass,
// that an empty layer uses the default, and that the provider carries the
// effective limit and the layer that sets it, as the list of the sign-in
// providers shows them.
func TestLDAPSubjectFailureLimitReachesTheProvider(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		layer  string
		value  string
		want   int
		source string
	}{
		{name: "default", layer: "none", value: "", want: 5, source: "default"},
		{name: "empty variable", layer: "variable", value: " ", want: 5, source: "default"},
		{name: "flag minimum", layer: "flag", value: "1", want: 1, source: "flag"},
		{name: "flag maximum", layer: "flag", value: "100", want: 100, source: "flag"},
		{name: "variable", layer: "variable", value: "7", want: 7, source: "env"},
		{name: "file", layer: "file", value: "9", want: 9, source: "file"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			limit, source := providerLimit(t, tc.layer, tc.value)
			if limit != tc.want || source != tc.source {
				t.Fatalf("provider limit = %d from %s, want %d from %s", limit, source, tc.want, tc.source)
			}
		})
	}
}

// TestLDAPSubjectFailureLimitWithoutAURLFailsFromEachLayer proves that a
// limit from the flag, the variable or the file with no URL stops the start
// and names the URL setting.
func TestLDAPSubjectFailureLimitWithoutAURLFailsFromEachLayer(t *testing.T) {
	t.Parallel()

	for _, layer := range []string{"flag", "variable", "file"} {
		t.Run(layer, func(t *testing.T) {
			t.Parallel()

			if err := startSubjectLimit(t, nil, layer, "7"); !errors.Is(err, errNoLDAPURL) {
				t.Fatalf("start with %s and no URL = %v, want errNoLDAPURL", layer, err)
			}
		})
	}
}
