package dustpan_test

import (
	"strings"
	"testing"

	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/session"
)

// ldapSetupCase is one LDAP provider setup and the text of the start error
// that it must give, or "" for a setup that starts.
type ldapSetupCase struct {
	change    func(p *dustpan.LDAPProvider)
	name      string
	publicURL string
	want      string
	withOIDC  bool
}

// ldapSetupCases are the hostile LDAP provider setups that NewServer must
// refuse, and one valid setup.
func ldapSetupCases() []ldapSetupCase {
	const public = "https://dustpan.example"

	return []ldapSetupCase{
		{name: "valid", publicURL: public, change: func(*dustpan.LDAPProvider) {}, want: "", withOIDC: false},
		{
			name:      "no public URL",
			publicURL: "",
			change:    func(*dustpan.LDAPProvider) {},
			want:      "--public-url",
			withOIDC:  false,
		},
		{
			name: "http public URL", publicURL: "http://dustpan.example", change: func(*dustpan.LDAPProvider) {},
			want: "must use https", withOIDC: false,
		},
		{name: "http URL", publicURL: public, change: func(p *dustpan.LDAPProvider) {
			p.URL = "http://ldap.example.org"
		}, want: "ldap sign-in", withOIDC: false},
		{name: "URL with a path", publicURL: public, change: func(p *dustpan.LDAPProvider) {
			p.URL = "ldaps://ldap.example.org/dc=example"
		}, want: "ldap sign-in", withOIDC: false},
		{name: "URL with user information", publicURL: public, change: func(p *dustpan.LDAPProvider) {
			p.URL = "ldaps://admin@ldap.example.org"
		}, want: "ldap sign-in", withOIDC: false},
		{name: "StartTLS on ldaps", publicURL: public, change: func(p *dustpan.LDAPProvider) {
			p.StartTLS = true
		}, want: "ldap sign-in", withOIDC: false},
		{name: "filter with no user placeholder", publicURL: public, change: func(p *dustpan.LDAPProvider) {
			p.UserFilter = "(objectClass=person)"
		}, want: "ldap sign-in", withOIDC: false},
		{name: "no bind password", publicURL: public, change: func(p *dustpan.LDAPProvider) {
			p.BindPassword = ""
		}, want: "ldap sign-in", withOIDC: false},
		{name: "unknown groups source", publicURL: public, change: func(p *dustpan.LDAPProvider) {
			p.Groups = "all"
		}, want: "ldap sign-in", withOIDC: false},
		{name: "reserved local ID", publicURL: public, change: func(p *dustpan.LDAPProvider) {
			p.ID = session.LocalProviderID
		}, want: "provider ID", withOIDC: false},
		{name: "reserved dev ID", publicURL: public, change: func(p *dustpan.LDAPProvider) {
			p.ID = session.DevProviderID
		}, want: "provider ID", withOIDC: false},
		{name: "upper-case ID", publicURL: public, change: func(p *dustpan.LDAPProvider) {
			p.ID = "Corp"
		}, want: "provider ID", withOIDC: false},
		{name: "ID of an OIDC provider", publicURL: public, change: func(p *dustpan.LDAPProvider) {
			p.ID = "corp"
		}, want: "two", withOIDC: true},
	}
}

// TestNewServerLDAPSetup proves that NewServer checks each LDAP provider:
// the public URL, the URL of the directory, the filter, the bind password and
// the provider ID. A valid setup starts with no connection to the directory.
func TestNewServerLDAPSetup(t *testing.T) {
	t.Parallel()

	for _, tc := range ldapSetupCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			p := ldapProvider()
			tc.change(&p)

			opts := append(auditSinkServerOptions(t, openAuditStore(t)),
				dustpan.WithPublicURL(tc.publicURL), dustpan.WithLDAPProvider(p))
			if tc.withOIDC {
				opts = append(opts, dustpan.WithOIDCProvider(oidcProvider("corp")))
			}

			_, err := dustpan.NewServer(opts...)

			switch {
			case tc.want == "" && err != nil:
				t.Fatalf("NewServer: %v", err)
			case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
				t.Fatalf("NewServer error = %v, want one that holds %q", err, tc.want)
			case err != nil && strings.Contains(err.Error(), p.BindPassword) && p.BindPassword != "":
				t.Fatalf("the start error holds the bind password: %v", err)
			default:
			}
		})
	}
}

// ldapProvider returns a valid LDAP provider with the ID corp-ldap.
func ldapProvider() dustpan.LDAPProvider {
	return dustpan.LDAPProvider{
		RootCAs: nil, ID: "corp-ldap", DisplayName: "Corp", URL: "ldaps://ldap.example.org",
		BindDN: "cn=dustpan,ou=services,dc=example,dc=org", BindPassword: "bind-password-of-the-test",
		BaseDN: "ou=people,dc=example,dc=org", UserFilter: "(&(objectClass=person)(uid={user}))",
		UserNameAttribute: "uid", EmailAttribute: "mail", NameAttribute: "cn", Subject: "entryUUID",
		DisabledAttribute: "", Groups: "off", GroupBaseDN: "", GroupFilter: "", GroupMemberAttribute: "",
		GroupMemberOfAttribute: "", GroupNameAttribute: "", DisabledValues: nil, DisabledMask: 0, StartTLS: false,
		AutoCreate: true,
	}
}
