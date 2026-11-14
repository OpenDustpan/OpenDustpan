package main

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/pkg/egress"
)

// ldapTestPassword is the password of the service account in the tests of the
// LDAP settings. It is no credential of any system.
const ldapTestPassword = "bind-password-of-the-test"

// defaultLDAP returns the LDAP flags with no setting: only the defaults of
// the subject and the groups.
func defaultLDAP() ldapFlags {
	return ldapFlags{
		url: "", providerID: "", displayName: "", caFile: "", allowedRanges: "", bindDN: "", baseDN: "", userFilter: "",
		userNameAttribute: "", emailAttribute: "", nameAttribute: "", subject: "entryUUID",
		disabledAttribute: "", disabledMask: "", disabledValues: "", groups: "off", groupBaseDN: "",
		groupFilter: "", groupMemberAttribute: "", groupMemberOfAttribute: "", groupNameAttribute: "",
		startTLS: false, noAutoCreate: false,
	}
}

// completeLDAP returns the LDAP flags of a provider with each required setting.
func completeLDAP() ldapFlags {
	l := defaultLDAP()
	l.url = "ldaps://ldap.example.org"
	l.providerID = "corp-ldap"
	l.bindDN = "cn=dustpan,dc=example,dc=org"
	l.baseDN = "dc=example,dc=org"
	l.userFilter = "(uid={user})"

	return l
}

// checkLDAPOptions runs ldapOptions on change(completeLDAP()) with the
// variables vars, and fails t when the result is not want options and wantErr.
func checkLDAPOptions(t *testing.T, change func(l *ldapFlags), vars map[string]string, want int, wantErr error) {
	t.Helper()

	var f serveFlags

	f.ldap = completeLDAP()
	change(&f.ldap)

	env := &fakeEnv{vars: map[string]string{}, cleared: nil, reads: 0}
	maps.Copy(env.vars, vars)

	opts, err := f.ldapOptions(env.get, env.unset)
	if !errors.Is(err, wantErr) || len(opts) != want {
		t.Fatalf("ldapOptions = %d options, %v; want %d, %v", len(opts), err, want, wantErr)
	}

	if err != nil && strings.Contains(err.Error(), ldapTestPassword) {
		t.Fatalf("the error holds the password: %v", err)
	}
}

// fakeEnv is a process environment that records the cleared variables.
type fakeEnv struct {
	vars    map[string]string
	cleared []string
	reads   int
}

func (e *fakeEnv) get(name string) (string, bool) {
	e.reads++
	v, ok := e.vars[name]

	return v, ok
}

func (e *fakeEnv) unset(name string) error {
	e.cleared = append(e.cleared, name)
	delete(e.vars, name)

	return nil
}

// TestLDAPOptionsRefuseBadSettings gives the LDAP settings each value that the
// start must refuse, and the values that it must take.
func TestLDAPOptionsRefuseBadSettings(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	// The test makes a file that others can read on purpose.
	shared := writeTokenFile(t, ldapTestPassword+"\n", 0o644)
	private := writeTokenFile(t, ldapTestPassword+"\n", 0o600)
	noCert := writeTokenFile(t, ldapTestPassword+"\n", 0o600)

	withPassword := map[string]string{ldapBindEnvVar: ldapTestPassword}

	cases := []struct {
		name    string
		change  func(l *ldapFlags)
		vars    map[string]string
		want    int
		wantErr error
	}{
		{"no LDAP setting", func(l *ldapFlags) { *l = defaultLDAP() }, nil, 0, nil},
		{"a setting with no URL", func(l *ldapFlags) { l.url = "" }, withPassword, 0, errNoLDAPURL},
		{"start TLS with no URL", func(l *ldapFlags) {
			*l = defaultLDAP()
			l.startTLS = true
		}, withPassword, 0, errNoLDAPURL},
		{"no password", func(*ldapFlags) {}, nil, 0, errNoLDAPPassword},
		{
			"a password file that others can read", func(*ldapFlags) {},
			map[string]string{ldapBindFileEnvVar: shared},
			0, errSecretFilePermissions,
		},
		{"a mask that is not a number", func(l *ldapFlags) { l.disabledMask = "two" }, withPassword, 0, errLDAPMask},
		{"a mask of zero", func(l *ldapFlags) { l.disabledMask = "0" }, withPassword, 0, errLDAPMask},
		{
			"a CA file with no certificate", func(l *ldapFlags) { l.caFile = noCert }, withPassword, 0,
			errNoCertificates,
		},
		{
			"a CA file that does not exist", func(l *ldapFlags) { l.caFile = filepath.Join(dir, "missing") },
			withPassword, 0, os.ErrNotExist,
		},
		{"ranges with no URL", func(l *ldapFlags) {
			*l = defaultLDAP()
			l.allowedRanges = "10.20.0.0/16"
		}, withPassword, 0, errNoLDAPURL},
		{
			"a range that is too wide", func(l *ldapFlags) { l.allowedRanges = "10.0.0.0/4" }, withPassword, 0,
			egress.ErrRangeTooWide,
		},
		{
			"a loopback range", func(l *ldapFlags) { l.allowedRanges = "127.0.0.0/8" }, withPassword, 0,
			egress.ErrRangeTooWide,
		},
		{
			"a range that is not a CIDR", func(l *ldapFlags) { l.allowedRanges = "not-a-cidr" }, withPassword, 0,
			egress.ErrRange,
		},
		{
			"a prefix of more than 32 bits", func(l *ldapFlags) { l.allowedRanges = "10.0.0.0/33" }, withPassword, 0,
			egress.ErrRange,
		},
		{
			"every IPv6 address", func(l *ldapFlags) { l.allowedRanges = "::/0" }, withPassword, 0,
			egress.ErrRangeTooWide,
		},
		{
			"a link-local range", func(l *ldapFlags) { l.allowedRanges = "169.254.0.0/16" }, withPassword, 0,
			egress.ErrRangeTooWide,
		},
		{
			"the cloud metadata address",
			func(l *ldapFlags) { l.allowedRanges = "169.254.169.254/32" },
			withPassword,
			0,
			egress.ErrRangeTooWide,
		},
		{
			"an IPv6 link-local range", func(l *ldapFlags) { l.allowedRanges = "fe80::/64" }, withPassword, 0,
			egress.ErrRangeTooWide,
		},
		{
			"an IPv4-mapped loopback range",
			func(l *ldapFlags) { l.allowedRanges = "::ffff:127.0.0.0/104" },
			withPassword,
			0,
			egress.ErrRangeTooWide,
		},
		{
			"a private range", func(l *ldapFlags) { l.allowedRanges = "10.20.0.0/16, fd00:20::/48" }, withPassword, 1,
			nil,
		},
		{"the password in the variable", func(*ldapFlags) {}, withPassword, 1, nil},
		{
			"the password in a private file", func(*ldapFlags) {},
			map[string]string{ldapBindFileEnvVar: private},
			1, nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			checkLDAPOptions(t, tc.change, tc.vars, tc.want, tc.wantErr)
		})
	}
}

// TestLDAPPasswordVariableIsCleared proves that the start clears the password
// variable after the read, so a child process does not inherit it.
func TestLDAPPasswordVariableIsCleared(t *testing.T) {
	t.Parallel()

	var f serveFlags

	f.ldap = completeLDAP()
	env := &fakeEnv{vars: map[string]string{ldapBindEnvVar: ldapTestPassword}, cleared: nil, reads: 0}

	if _, err := f.ldapOptions(env.get, env.unset); err != nil {
		t.Fatalf("ldapOptions = %v", err)
	}

	if len(env.cleared) != 1 || env.cleared[0] != ldapBindEnvVar {
		t.Fatalf("cleared variables = %v, want [%s]", env.cleared, ldapBindEnvVar)
	}
}

// TestLDAPPasswordWithoutTheAPIComponentIsNotRead proves that a process
// without the api component reads no LDAP setting and no password.
func TestLDAPPasswordWithoutTheAPIComponentIsNotRead(t *testing.T) {
	t.Parallel()

	var f serveFlags

	f.components = "worker"
	f.ldap = completeLDAP()
	env := &fakeEnv{vars: map[string]string{ldapBindEnvVar: ldapTestPassword}, cleared: nil, reads: 0}

	opts, err := f.ldapOptions(env.get, env.unset)
	if err != nil || len(opts) != 0 || env.reads != 0 || len(env.cleared) != 0 {
		t.Fatalf("ldapOptions = %d options, %v, %d reads, cleared %v; want none and no read",
			len(opts), err, env.reads, env.cleared)
	}
}

// TestLDAPPasswordHasNoFlagAndNoFileKey proves that only the variables set
// the password: no flag takes it, and a configuration file with it is refused.
func TestLDAPPasswordHasNoFlagAndNoFileKey(t *testing.T) {
	t.Parallel()

	for _, s := range cli.Settings {
		if strings.Contains(s.Flag, "password") && strings.HasPrefix(s.Flag, "ldap-") {
			t.Errorf("the flag --%s exists: a flag shows in process listings", s.Flag)
		}
	}

	for _, key := range []string{"ldap.bindPassword", "ldap.password"} {
		_, err := cli.ParseConfig([]byte(key + ": " + ldapTestPassword + "\n"))
		if !errors.Is(err, cli.ErrConfigUnknown) {
			t.Errorf("a configuration file with %s = %v, want ErrConfigUnknown", key, err)
		}
	}

	if s := cli.ByEnv(ldapBindEnvVar); s == nil || !s.Sensitive || s.Flag != "" || s.File != "" {
		t.Errorf("%s is not a sensitive variable with no flag and no file key", ldapBindEnvVar)
	}
}
