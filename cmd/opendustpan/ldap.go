package main

import (
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/internal/cli"
	"opendustpan.org/opendustpan/pkg/dustpan"
)

// Variables of the password of the service account of the LDAP sign-in. A
// flag shows in process listings, so the password has no flag and no file
// key. They hold the names of the variables, not a value.
const (
	ldapPasswordEnvVar     = "DUSTPAN_LDAP_BIND_PASSWORD"      //nolint:gosec // The name of a variable, not a credential.
	ldapPasswordFileEnvVar = "DUSTPAN_LDAP_BIND_PASSWORD_FILE" //nolint:gosec // The name of a variable, not a credential.
)

// Errors of the LDAP sign-in settings.
var (
	errNoLDAPPassword = errors.New("the LDAP sign-in needs the password of the service account")
	errNoLDAPURL      = errors.New("an LDAP setting needs --ldap-url")
	errLDAPMask       = errors.New("--ldap-disabled-mask must be an unsigned decimal number")
)

// ldapFlags are the settings of the LDAP sign-in provider.
type ldapFlags struct {
	url                    string
	providerID             string
	displayName            string
	caFile                 string
	bindDN                 string
	baseDN                 string
	userFilter             string
	userNameAttribute      string
	emailAttribute         string
	nameAttribute          string
	subject                string
	disabledAttribute      string
	disabledMask           string
	disabledValues         string
	groups                 string
	groupBaseDN            string
	groupFilter            string
	groupMemberAttribute   string
	groupMemberOfAttribute string
	groupNameAttribute     string
	startTLS               bool
	noAutoCreate           bool
}

// register binds the LDAP flags of fs to l.
func (l *ldapFlags) register(fs *flag.FlagSet) {
	for _, s := range []struct {
		flag string
		into *string
	}{
		{"ldap-url", &l.url},
		{"ldap-provider", &l.providerID},
		{"ldap-display-name", &l.displayName},
		{"ldap-ca-file", &l.caFile},
		{"ldap-bind-dn", &l.bindDN},
		{"ldap-base-dn", &l.baseDN},
		{"ldap-user-filter", &l.userFilter},
		{"ldap-user-name-attribute", &l.userNameAttribute},
		{"ldap-email-attribute", &l.emailAttribute},
		{"ldap-name-attribute", &l.nameAttribute},
		{"ldap-subject", &l.subject},
		{"ldap-disabled-attribute", &l.disabledAttribute},
		{"ldap-disabled-mask", &l.disabledMask},
		{"ldap-disabled-values", &l.disabledValues},
		{"ldap-groups", &l.groups},
		{"ldap-group-base-dn", &l.groupBaseDN},
		{"ldap-group-filter", &l.groupFilter},
		{"ldap-group-member-attribute", &l.groupMemberAttribute},
		{"ldap-group-member-of-attribute", &l.groupMemberOfAttribute},
		{"ldap-group-name-attribute", &l.groupNameAttribute},
	} {
		cli.ByFlag(s.flag).String(fs, s.into)
	}

	cli.ByFlag("ldap-start-tls").Bool(fs, &l.startTLS)
	cli.ByFlag("ldap-no-auto-create").Bool(fs, &l.noAutoCreate)
}

// partial reports whether l sets a setting of a provider with no URL. The
// subject and the groups have defaults, so they do not count.
func (l *ldapFlags) partial() bool {
	for _, v := range []string{
		l.providerID, l.displayName, l.caFile, l.bindDN, l.baseDN, l.userFilter, l.userNameAttribute,
		l.emailAttribute, l.nameAttribute, l.disabledAttribute, l.disabledMask, l.disabledValues, l.groupBaseDN,
		l.groupFilter, l.groupMemberAttribute, l.groupMemberOfAttribute, l.groupNameAttribute,
	} {
		if strings.TrimSpace(v) != "" {
			return true
		}
	}

	return l.startTLS || l.noAutoCreate
}

// ldapOptions returns the LDAP sign-in option of the serve flags: one provider
// when --ldap-url is set, else none. A process without the api component gets
// none and does not read the password. The password comes only from
// DUSTPAN_LDAP_BIND_PASSWORD or the file of DUSTPAN_LDAP_BIND_PASSWORD_FILE.
// getenv reads the variables of the process and unsetenv clears the password
// variable after the read, so a child process does not inherit it. The server
// checks every other setting when it starts.
func (f *serveFlags) ldapOptions(
	getenv func(string) (string, bool), unsetenv func(string) error,
) ([]dustpan.Option, error) {
	set, err := dustpan.ParseComponents(f.components)
	if err != nil {
		return nil, fmt.Errorf("--components: %w", err)
	}

	if !set.Has(dustpan.ComponentAPI) {
		return nil, nil
	}

	l := &f.ldap

	if strings.TrimSpace(l.url) == "" {
		if l.partial() {
			return nil, fmt.Errorf("%w: unset the other --ldap-* settings, or set the URL", errNoLDAPURL)
		}

		return nil, nil
	}

	password, err := secretFrom(getenv, ldapPasswordEnvVar, ldapPasswordFileEnvVar, errNoLDAPPassword)
	if err != nil {
		return nil, err
	}

	if _, inVar := getenv(ldapPasswordEnvVar); inVar {
		if unsetErr := unsetenv(ldapPasswordEnvVar); unsetErr != nil {
			return nil, fmt.Errorf("%s: clear variable: %w", ldapPasswordEnvVar, unsetErr)
		}
	}

	p, err := l.provider(string(password))
	if err != nil {
		return nil, err
	}

	return []dustpan.Option{dustpan.WithLDAPProvider(p)}, nil
}

// provider returns the provider of l with the password of the service account.
func (l *ldapFlags) provider(password string) (dustpan.LDAPProvider, error) {
	var mask uint64

	if strings.TrimSpace(l.disabledMask) != "" {
		parsed, err := strconv.ParseUint(strings.TrimSpace(l.disabledMask), 10, 64)
		if err != nil || parsed == 0 {
			return dustpan.LDAPProvider{}, errLDAPMask //nolint:exhaustruct // An error answers no provider.
		}

		mask = parsed
	}

	roots, err := ldapRoots(l.caFile)
	if err != nil {
		return dustpan.LDAPProvider{}, err //nolint:exhaustruct // An error answers no provider.
	}

	return dustpan.LDAPProvider{
		RootCAs: roots, ID: l.providerID, DisplayName: l.displayName, URL: strings.TrimSpace(l.url),
		BindDN: l.bindDN, BindPassword: password, BaseDN: l.baseDN, UserFilter: l.userFilter,
		UserNameAttribute: l.userNameAttribute, EmailAttribute: l.emailAttribute, NameAttribute: l.nameAttribute,
		Subject: l.subject, DisabledAttribute: l.disabledAttribute, Groups: l.groups, GroupBaseDN: l.groupBaseDN,
		GroupFilter: l.groupFilter, GroupMemberAttribute: l.groupMemberAttribute,
		GroupMemberOfAttribute: l.groupMemberOfAttribute, GroupNameAttribute: l.groupNameAttribute,
		DisabledValues: splitList(l.disabledValues), DisabledMask: mask, StartTLS: l.startTLS,
		AutoCreate: !l.noAutoCreate,
	}, nil
}

// ldapRoots returns the certificate authorities of the file path, or nil for
// no path: the system roots. A file that cannot be read or that holds no PEM
// certificate fails the start.
func ldapRoots(path string) (*x509.CertPool, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil //nolint:nilnil // No file means the system roots.
	}

	pem, err := os.ReadFile(path) //nolint:gosec // The operator names the CA file in the server settings.
	if err != nil {
		return nil, fmt.Errorf("--ldap-ca-file: %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("--ldap-ca-file %s: %w", path, errNoCertificates)
	}

	return pool, nil
}
