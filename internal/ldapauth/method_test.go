package ldapauth_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/ldapauth"
	"opendustpan.org/opendustpan/internal/ldaptest"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/session/sessiontest"
)

// providerID names the provider of each test method.
const providerID = "corp-ldap"

// setup is a test directory and the switch of its provider.
type setup struct {
	dir *ldaptest.Server
	off *atomic.Bool
}

// newSetup starts a test directory.
func newSetup(t *testing.T) setup {
	t.Helper()

	return setup{dir: ldaptest.Start(t), off: &atomic.Bool{}}
}

// baseConfig returns the Config of a method over the directory with the
// settings s.
func (s setup) baseConfig(ds sessiontest.DirectorySettings) ldapauth.Config {
	url := map[sessiontest.DirectoryTransport]string{
		sessiontest.TransportTLS:           s.dir.LDAPSURL(),
		sessiontest.TransportPlainLoopback: s.dir.PlainURL(),
		sessiontest.TransportPlainName:     s.dir.PlainNameURL(),
	}[ds.Transport]

	subject := map[sessiontest.DirectorySubject]ldapauth.SubjectKind{
		sessiontest.SubjectEntryUUID:  ldapauth.SubjectEntryUUID,
		sessiontest.SubjectObjectGUID: ldapauth.SubjectObjectGUID,
	}[ds.Subject]

	groups := map[sessiontest.DirectoryGroups]ldapauth.Groups{
		sessiontest.DirectoryGroupsOff: {
			Source: ldapauth.GroupsOff, BaseDN: "", Filter: "", MemberAttribute: "", MemberOfAttribute: "",
			NameAttribute: "",
		},
		sessiontest.DirectoryGroupsSearch: {
			Source: ldapauth.GroupsSearch, BaseDN: ldaptest.GroupsDN, Filter: "(objectClass=groupOfNames)",
			MemberAttribute: "member", MemberOfAttribute: "", NameAttribute: "cn",
		},
		sessiontest.DirectoryGroupsMemberOf: {
			Source: ldapauth.GroupsMemberOf, BaseDN: ldaptest.GroupsDN, Filter: "", MemberAttribute: "",
			MemberOfAttribute: "memberOf", NameAttribute: "cn",
		},
	}[ds.Groups]

	allowPlain := ds.AllowPlain
	off := s.off

	return ldapauth.Config{
		Enabled:    func(context.Context) (bool, error) { return !off.Load(), nil },
		AllowPlain: func(context.Context) (bool, error) { return allowPlain, nil },
		Now:        time.Now, Logger: nil, RootCAs: s.dir.CertPool(), ProviderID: providerID, URL: url,
		BindDN: ldaptest.ServiceDN, BaseDN: ldaptest.BaseDN, UserFilter: "(&(objectClass=person)(uid={user}))",
		UserNameAttribute: "uid", EmailAttribute: "mail", NameAttribute: "cn",
		BindPassword:   session.NewPassword(ldaptest.ServicePassword),
		Subject:        ldapauth.Subject{Kind: subject, Attribute: ""},
		Disabled:       ldapauth.Disabled{Attribute: "userAccountControl", Values: nil, Mask: 2},
		Groups:         groups,
		Egress:         egress.Rules{Allow: nil, LoopbackForTests: !ds.EgressRefused},
		ConnectTimeout: 0, OperationTimeout: ds.OperationTimeout, StartTLS: false,
	}
}

// newMethod returns the method of cfg.
func newMethod(t *testing.T, cfg ldapauth.Config) *ldapauth.Method {
	t.Helper()

	m, err := ldapauth.NewMethod(cfg)
	if err != nil {
		t.Fatalf("NewMethod() = %v", err)
	}

	return m
}

// randomBytes returns n random bytes.
func randomBytes(t *testing.T, n int) []byte {
	t.Helper()

	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("random: %v", err)
	}

	return b
}

// uuidText returns the 16 bytes b as UUID text in upper case, as some
// directories store it.
func uuidText(b []byte) string {
	h := fmt.Sprintf("%X", b)

	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
}

// subjects are the subjects of one user in the text form of each kind.
type subjects struct {
	uuid string
	guid string
}

// addUser adds the person p to the directory with the raw subject value
// raw, or with random subjects when raw is nil. It returns the subjects.
func (s setup) addUser(t *testing.T, p sessiontest.TestPerson, password string, raw []byte) subjects {
	t.Helper()

	guid := randomBytes(t, 16)
	uuid := uuidText(randomBytes(t, 16))
	attrs := map[string][]string{"userAccountControl": {"512"}}

	if raw != nil {
		guid, uuid = raw, string(raw)
	}

	if p.Disabled {
		attrs["userAccountControl"] = []string{"514"}
	}

	attrs["entryUUID"] = []string{uuid}
	attrs["objectGUID"] = []string{string(guid)}

	if p.Email != "" {
		attrs["mail"] = []string{p.Email}
	}

	if p.Name != "" {
		attrs["cn"] = []string{p.Name}
	}

	s.dir.AddUser(ldaptest.User{Attributes: attrs, UserName: p.UserName, Password: password})

	for _, g := range p.Groups {
		s.dir.AddGroup(ldaptest.Group{Name: g, Users: []string{p.UserName}, Groups: nil, Link: ldaptest.LinkBoth})
	}

	byGUID, err := ldapauth.GUIDText(guid)
	if err != nil {
		byGUID = ""
	}

	return subjects{uuid: strings.ToLower(uuid), guid: byGUID}
}

// fixture returns the fixture of a method over the directory with the
// settings ds. With groups on, AddUser puts a person with no groups in the
// group "staff", so that each sign-in of the suite has a group.
func (s setup) fixture(t *testing.T, ds sessiontest.DirectorySettings) sessiontest.PasswordFixture {
	t.Helper()

	m := newMethod(t, s.baseConfig(ds))
	groupsOn := ds.Groups != sessiontest.DirectoryGroupsOff

	return sessiontest.PasswordFixture{
		Method: m,
		AddUser: func(t *testing.T, p sessiontest.TestPerson, password string) string {
			t.Helper()

			if groupsOn && !p.GroupsPresent {
				p.Groups, p.GroupsPresent = []string{"staff"}, true
			}

			subs := s.addUser(t, p, password, nil)
			if ds.Subject == sessiontest.SubjectObjectGUID {
				return subs.guid
			}

			return subs.uuid
		},
		UserNameForms: func(name string) []string { return []string{strings.ToUpper(name)} },
		Stop:          func(*testing.T) { s.dir.Stop() },
		Disable:       func(*testing.T) { s.off.Store(true) },
		Now:           time.Now, ProviderID: providerID, Groups: groupsOn, ReportsDisabled: true,
		Directory: &sessiontest.Directory{
			Configure: s.fixture,
			AddUserWithSubject: func(t *testing.T, p sessiontest.TestPerson, password string, raw []byte) {
				t.Helper()
				s.addUser(t, p, password, raw)
			},
			AddGroup: func(_ *testing.T, g sessiontest.DirectoryGroup) {
				s.dir.AddGroup(ldaptest.Group{Name: g.Name, Users: g.Users, Groups: g.Groups, Link: link(g.Link)})
			},
			SetFault:    func(_ *testing.T, f sessiontest.DirectoryFault) { s.dir.SetFault(fault(f)) },
			UserDN:      ldaptest.UserDN,
			Binds:       s.dir.Binds,
			Connections: s.dir.Connections,
		},
	}
}

// link translates the link of the suite.
func link(l sessiontest.DirectoryLink) ldaptest.Link {
	return map[sessiontest.DirectoryLink]ldaptest.Link{
		sessiontest.LinkBoth:     ldaptest.LinkBoth,
		sessiontest.LinkMember:   ldaptest.LinkMember,
		sessiontest.LinkMemberOf: ldaptest.LinkMemberOf,
	}[l]
}

// fault translates the fault of the suite.
func fault(f sessiontest.DirectoryFault) ldaptest.Fault {
	return map[sessiontest.DirectoryFault]ldaptest.Fault{
		sessiontest.FaultNone:            ldaptest.FaultNone,
		sessiontest.FaultSlow:            ldaptest.FaultSlow,
		sessiontest.FaultSizeLimit:       ldaptest.FaultSizeLimit,
		sessiontest.FaultSearchReference: ldaptest.FaultSearchReference,
		sessiontest.FaultSearchReferral:  ldaptest.FaultSearchReferral,
		sessiontest.FaultBindReferral:    ldaptest.FaultBindReferral,
	}[f]
}

// defaults returns the settings of the suite with the group source g.
func defaults(g sessiontest.DirectoryGroups) sessiontest.DirectorySettings {
	return sessiontest.DirectorySettings{
		OperationTimeout: 0, Subject: sessiontest.SubjectEntryUUID, Groups: g,
		Transport: sessiontest.TransportTLS, AllowPlain: false, EgressRefused: false,
	}
}

// TestMethodConformance runs the suite of session.PasswordMethod on the
// method with groups off and with groups on.
func TestMethodConformance(t *testing.T) {
	t.Parallel()

	for name, g := range map[string]sessiontest.DirectoryGroups{
		"groups off": sessiontest.DirectoryGroupsOff, "groups search": sessiontest.DirectoryGroupsSearch,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			sessiontest.RunPasswordMethod(t, func(t *testing.T) sessiontest.PasswordFixture {
				t.Helper()

				return newSetup(t).fixture(t, defaults(g))
			})
		})
	}
}
