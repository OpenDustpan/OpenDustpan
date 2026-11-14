//go:build e2e

package e2e

import (
	"context"
	"testing"

	"opendustpan.org/opendustpan/internal/ldaptest"
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/session"
)

// The LDAP providers of an env from NewEnvWithLDAP. Both read the same test
// directory.
const (
	// LDAPTLSProvider connects to the directory over TLS.
	LDAPTLSProvider = "tls-ldap"
	// LDAPPlainProvider connects to the directory with no TLS, at the host
	// name localhost. The server refuses it until the setting
	// egress.allowPlainConnections is on.
	LDAPPlainProvider = "plain-ldap"
)

// NewEnvWithLDAP starts a server as NewEnvWithRealTimeSignIn does, with the
// LDAP providers LDAPTLSProvider and LDAPPlainProvider of the test directory
// dir. Only the in-process server takes the directory: the server binary
// refuses a directory on the loopback, as a server in production does.
func NewEnvWithLDAP(ctx context.Context, tb testing.TB, cli Install, store Store, dir *ldaptest.Server) *Env {
	tb.Helper()

	if UseBinaryServer() {
		tb.Fatal("the server binary cannot reach a test LDAP directory on the loopback: run the step in process")
	}

	return startSignInEnvWith(ctx, tb, cli, store,
		signInEnvOptions{issuer: nil, ldap: dir, realTime: true, ipAllowlists: false, secretInVariable: false})
}

// ldapOptions returns the options of the LDAP providers of the sign-in, or
// none when the sign-in has no test directory.
func (s *signInSetup) ldapOptions() []dustpan.Option {
	if s.ldap == nil {
		return nil
	}

	provider := func(id, url string) dustpan.LDAPProvider {
		return dustpan.LDAPProvider{
			RootCAs:                s.ldap.CertPool(),
			AllowedRanges:          nil,
			ID:                     id,
			DisplayName:            "",
			URL:                    url,
			BindDN:                 ldaptest.ServiceDN,
			BindPassword:           session.NewPassword(s.ldap.ServicePassword()),
			BaseDN:                 ldaptest.BaseDN,
			UserFilter:             "(&(objectClass=person)(uid={user}))",
			UserNameAttribute:      "uid",
			EmailAttribute:         "",
			NameAttribute:          "",
			Subject:                "entryUUID",
			DisabledAttribute:      "",
			Groups:                 "off",
			GroupBaseDN:            "",
			GroupFilter:            "",
			GroupMemberAttribute:   "",
			GroupMemberOfAttribute: "",
			GroupNameAttribute:     "",
			DisabledValues:         nil,
			DisabledMask:           0,
			StartTLS:               false,
			AutoCreate:             true,
		}
	}

	return []dustpan.Option{
		dustpan.WithLDAPLoopbackForTests(),
		dustpan.WithLDAPProvider(provider(LDAPTLSProvider, s.ldap.LDAPSURL())),
		dustpan.WithLDAPProvider(provider(LDAPPlainProvider, s.ldap.PlainNameURL())),
	}
}
