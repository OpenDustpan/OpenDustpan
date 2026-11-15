package ldapauth_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/internal/ldapauth"
	"opendustpan.org/opendustpan/internal/ldaptest"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/session"
	"opendustpan.org/opendustpan/pkg/session/sessiontest"
)

// The tests in this file send hostile input to one layer of the method
// each, as if every other layer had failed.

// errState is the error of a setting that cannot be read.
var errState = errors.New("the state cannot be read")

// Passwords of the tests.
const (
	goodPassword  = "amber-river-north-62-candle"
	wrongPassword = "slate-forest-mild-19-kettle"
)

// openGate is a session.BindGate that lets each bind through.
type openGate struct{}

func (openGate) BeforeBind(context.Context, string) error { return nil }

// credentials returns the credentials of the provider of the tests.
func credentials(userName, password string) session.Credentials {
	return session.Credentials{ProviderID: providerID, UserName: userName, Password: session.NewPassword(password)}
}

// testPerson returns a person with the user name.
func testPerson(userName string) sessiontest.TestPerson {
	return sessiontest.TestPerson{
		AuthTime: time.Time{}, Subject: "", UserName: userName, Name: "", Email: "", Groups: nil,
		GroupsPresent: false, Disabled: false,
	}
}

// configWith returns the default Config over s, changed by change.
func (s setup) configWith(change func(c *ldapauth.Config)) ldapauth.Config {
	c := s.baseConfig(defaults(sessiontest.DirectoryGroupsOff))
	change(&c)

	return c
}

// TestNewMethodRefusesASettingThatIsNotValid is layer 1 for the settings.
func TestNewMethodRefusesASettingThatIsNotValid(t *testing.T) {
	t.Parallel()

	s := newSetup(t)

	cases := map[string]func(c *ldapauth.Config){
		"an http URL":              func(c *ldapauth.Config) { c.URL = "http://127.0.0.1:389" },
		"a URL with a path":        func(c *ldapauth.Config) { c.URL = "ldaps://127.0.0.1:636/dc=example" },
		"a URL with user info":     func(c *ldapauth.Config) { c.URL = "ldaps://admin@127.0.0.1:636" },
		"a URL with a query":       func(c *ldapauth.Config) { c.URL = "ldaps://127.0.0.1:636?x" },
		"an ldapi URL":             func(c *ldapauth.Config) { c.URL = "ldapi:///var/run/slapd" },
		"StartTLS on ldaps":        func(c *ldapauth.Config) { c.StartTLS = true },
		"an empty bind password":   func(c *ldapauth.Config) { c.BindPassword = session.NewPassword("") },
		"a filter with no user":    func(c *ldapauth.Config) { c.UserFilter = "(uid=alice)" },
		"a filter that is broken":  func(c *ldapauth.Config) { c.UserFilter = "(uid={user}" },
		"a filter with a newline":  func(c *ldapauth.Config) { c.UserFilter = "(uid={user})\n" },
		"a base DN that is broken": func(c *ldapauth.Config) { c.BaseDN = "dc=example,,=" },
		"an empty base DN":         func(c *ldapauth.Config) { c.BaseDN = "" },
		"a bad attribute type":     func(c *ldapauth.Config) { c.EmailAttribute = "mail)(x" },
		"a subject attribute for a fixed kind": func(c *ldapauth.Config) {
			c.Subject = ldapauth.Subject{Kind: ldapauth.SubjectEntryUUID, Attribute: "uid"}
		},
		"an unknown subject kind": func(c *ldapauth.Config) { c.Subject.Kind = "dn" },
		"a timeout over the limit": func(c *ldapauth.Config) {
			c.OperationTimeout = ldapauth.MaxTimeout + 1
		},
		"a negative timeout":       func(c *ldapauth.Config) { c.ConnectTimeout = -1 },
		"the local provider":       func(c *ldapauth.Config) { c.ProviderID = session.LocalProviderID },
		"no switch":                func(c *ldapauth.Config) { c.Enabled = nil },
		"no plain-transport check": func(c *ldapauth.Config) { c.AllowPlain = nil },
		"a disabled rule with no attribute": func(c *ldapauth.Config) {
			c.Disabled = ldapauth.Disabled{Attribute: "", Values: nil, Mask: 2}
		},
		"an unknown group source": func(c *ldapauth.Config) { c.Groups.Source = "nested" },
		"a group search with no member attribute": func(c *ldapauth.Config) {
			c.Groups = ldapauth.Groups{
				Source: ldapauth.GroupsSearch, BaseDN: ldaptest.GroupsDN, Filter: "(objectClass=*)",
				MemberAttribute: "", MemberOfAttribute: "", NameAttribute: "cn",
			}
		},
	}

	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := ldapauth.NewMethod(s.configWith(change)); !errors.Is(err, ldapauth.ErrConfig) {
				t.Fatalf("NewMethod() = %v, want ErrConfig", err)
			}
		})
	}
}

// TestABoundaryRefusalOpensNoConnection is layer 1 for the credentials: an
// input that fails the boundary never reaches the directory.
func TestABoundaryRefusalOpensNoConnection(t *testing.T) {
	t.Parallel()

	cases := map[string]session.Credentials{
		"an empty password":         credentials("alice", ""),
		"a password over the limit": credentials("alice", strings.Repeat("p", ldapauth.MaxPassword+1)),
		"a user name with a NUL":    credentials("alice\x00", goodPassword),
		"a user name with CR LF":    credentials("alice\r\n", goodPassword),
		"a user name not UTF-8":     credentials("alice\xff", goodPassword),
	}

	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := newSetup(t)
			s.addUser(t, testPerson("alice"), goodPassword, nil)

			_, err := newMethod(t, s.configWith(func(*ldapauth.Config) {})).Verify(t.Context(), c, openGate{})
			if !errors.Is(err, session.ErrMethodInput) {
				t.Fatalf("Verify() = %v, want ErrMethodInput", err)
			}

			if got := s.dir.Connections(); got != 0 {
				t.Fatalf("connections = %d, want 0", got)
			}
		})
	}
}

// TestAFilterThatChangesItsStructureIsRefused is layer 2 for the filter: a
// filter with an added item is refused, as if the escape had failed.
func TestAFilterThatChangesItsStructureIsRefused(t *testing.T) {
	t.Parallel()

	m := newMethod(t, newSetup(t).configWith(func(*ldapauth.Config) {}))

	for _, f := range []string{
		"(&(objectClass=person)(uid=*)(uid=*))",
		"(&(objectClass=person)(|(uid=alice)(uid=*)))",
		"(&(objectClass=person)(uid=a",
	} {
		if _, err := ldapauth.CheckFilterForTest(m, f); !errors.Is(err, session.ErrMethodInput) {
			t.Fatalf("checkFilter(%q) = %v, want ErrMethodInput", f, err)
		}
	}

	if _, err := ldapauth.CheckFilterForTest(m, `(&(objectClass=person)(uid=\2a))`); err != nil {
		t.Fatalf("checkFilter() of an escaped value = %v, want nil", err)
	}
}

// TestTheDialGuardChecksTheResolvedAddress is layer 2 for the connection:
// the egress rules and the plain-transport rule apply to the address of
// each connection, whatever the URL said.
func TestTheDialGuardChecksTheResolvedAddress(t *testing.T) {
	t.Parallel()

	private := egress.Rules{Allow: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}, LoopbackForTests: true}
	public := egress.Rules{Allow: nil, LoopbackForTests: false}

	cases := []struct {
		name     string
		address  string
		want     error
		rules    egress.Rules
		rule     ldapauth.PlainRuleForTest
		wantText string
		notText  string
	}{
		{
			"a metadata address",
			"169.254.169.254:389",
			egresshttp.ErrDialRefused,
			private,
			ldapauth.PlainNotUsedForTest,
			"",
			"",
		},
		{
			"a private address outside the rules", "192.168.1.1:636", egresshttp.ErrDialRefused, private,
			ldapauth.PlainNotUsedForTest, "", "",
		},
		{
			"loopback with no test rule",
			"127.0.0.1:636",
			egresshttp.ErrDialRefused,
			public,
			ldapauth.PlainNotUsedForTest,
			"",
			"",
		},
		{
			"plain to a private address",
			"10.0.0.5:389",
			ldapauth.ErrPlainRefused,
			private,
			ldapauth.PlainLoopbackOnlyForTest,
			"",
			"",
		},
		{
			"the refusal of a plain connection names the setting",
			"10.0.0.5:389",
			ldapauth.ErrPlainRefused,
			private,
			ldapauth.PlainLoopbackOnlyForTest,
			"egress.allowPlainConnections",
			"",
		},
		{
			"plain to a private address with the setting",
			"10.0.0.5:389",
			nil,
			private,
			ldapauth.PlainAllowedForTest,
			"",
			"",
		},
		{"plain to loopback", "127.0.0.1:389", nil, private, ldapauth.PlainLoopbackOnlyForTest, "", ""},
		{"plain to the IPv6 loopback", "[::1]:389", nil, private, ldapauth.PlainLoopbackOnlyForTest, "", ""},
		{"TLS to a private address", "10.0.0.5:636", nil, private, ldapauth.PlainNotUsedForTest, "", ""},
		{
			"the refusal of a plain connection names the transports to use",
			"10.0.0.5:389",
			ldapauth.ErrPlainRefused,
			private,
			ldapauth.PlainLoopbackOnlyForTest,
			"use ldaps:// or StartTLS",
			"",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := ldapauth.DialControlForTest(tc.rules, tc.rule)("tcp", tc.address, nil)

			if (tc.want == nil) != (err == nil) || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("control(%q) = %v, want %v", tc.address, err, tc.want)
			}

			if tc.wantText != "" && (err == nil || !strings.Contains(err.Error(), tc.wantText)) {
				t.Errorf("control(%q) = %v, want the text %q", tc.address, err, tc.wantText)
			}

			if tc.notText != "" && err != nil && strings.Contains(err.Error(), tc.notText) {
				t.Errorf("control(%q) = %v, want no text %q", tc.address, err, tc.notText)
			}
		})
	}
}

// TestASubjectOfTheWrongFormIsErrMethodAnswer is layer 2 for the subject:
// a value of the directory that is not a subject never leaves the method.
func TestASubjectOfTheWrongFormIsErrMethodAnswer(t *testing.T) {
	t.Parallel()

	cases := []struct {
		subject ldapauth.Subject
		attrs   map[string][]string
		name    string
	}{
		{
			name:    "a binary value of a named attribute",
			subject: ldapauth.Subject{Kind: ldapauth.SubjectAttribute, Attribute: "employeeNumber"},
			attrs:   map[string][]string{"employeeNumber": {"\xff\xfe\x00\x01"}},
		},
		{
			name:    "a control character in a named attribute",
			subject: ldapauth.Subject{Kind: ldapauth.SubjectAttribute, Attribute: "employeeNumber"},
			attrs:   map[string][]string{"employeeNumber": {"42\n"}},
		},
		{
			name:    "two values of a named attribute",
			subject: ldapauth.Subject{Kind: ldapauth.SubjectAttribute, Attribute: "employeeNumber"},
			attrs:   map[string][]string{"employeeNumber": {"42", "43"}},
		},
		{
			name:    "no value",
			subject: ldapauth.Subject{Kind: ldapauth.SubjectAttribute, Attribute: "employeeNumber"},
			attrs:   map[string][]string{},
		},
		{
			name:    "an entryUUID that is not UUID text",
			subject: ldapauth.Subject{Kind: ldapauth.SubjectEntryUUID, Attribute: ""},
			attrs:   map[string][]string{"entryUUID": {"not-a-uuid"}},
		},
		{
			name:    "an objectGUID of 15 bytes",
			subject: ldapauth.Subject{Kind: ldapauth.SubjectObjectGUID, Attribute: ""},
			attrs:   map[string][]string{"objectGUID": {strings.Repeat("\x01", 15)}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newSetup(t)
			s.dir.AddUser(ldaptest.User{Attributes: tc.attrs, UserName: "alice", Password: goodPassword})

			m := newMethod(t, s.configWith(func(c *ldapauth.Config) { c.Subject = tc.subject }))

			a, err := m.Verify(t.Context(), credentials("alice", goodPassword), openGate{})
			if !errors.Is(err, session.ErrMethodAnswer) || a.Subject != "" {
				t.Fatalf("Verify() = %+v, %v, want ErrMethodAnswer", a, err)
			}
		})
	}
}

// TestANamedSubjectAttributeIsTheSubject checks the subject of an
// attribute that the administrator names.
func TestANamedSubjectAttributeIsTheSubject(t *testing.T) {
	t.Parallel()

	s := newSetup(t)
	s.dir.AddUser(ldaptest.User{
		Attributes: map[string][]string{"employeeNumber": {"E-1042"}, "userAccountControl": {"512"}},
		UserName:   "alice", Password: goodPassword,
	})

	m := newMethod(t, s.configWith(func(c *ldapauth.Config) {
		c.Subject = ldapauth.Subject{Kind: ldapauth.SubjectAttribute, Attribute: "employeeNumber"}
	}))

	a, err := m.Verify(t.Context(), credentials("alice", goodPassword), openGate{})
	if err != nil || a.Subject != "E-1042" {
		t.Fatalf("Verify() = %+v, %v, want the subject E-1042", a, err)
	}
}

// TestTheDisabledRule checks both forms of the disabled rule, and that a
// value that does not parse fails closed.
func TestTheDisabledRule(t *testing.T) {
	t.Parallel()

	mask := ldapauth.Disabled{Attribute: "userAccountControl", Values: nil, Mask: 2}
	values := ldapauth.Disabled{Attribute: "nsAccountLock", Values: []string{"true"}, Mask: 0}

	cases := []struct {
		want  error
		rule  ldapauth.Disabled
		name  string
		value string
	}{
		{name: "the bit is set", rule: mask, value: "514", want: session.ErrSubjectDisabled},
		{name: "the bit is not set", rule: mask, value: "512", want: nil},
		{name: "a value that is not a number", rule: mask, value: "x514", want: session.ErrMethodAnswer},
		{name: "a negative number", rule: mask, value: "-2", want: session.ErrMethodAnswer},
		{name: "a value of the list", rule: values, value: "TRUE", want: session.ErrSubjectDisabled},
		{name: "a value not in the list", rule: values, value: "false", want: nil},
		{name: "no value with a mask", rule: mask, value: "", want: session.ErrMethodAnswer},
		{name: "no value with a list", rule: values, value: "", want: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newSetup(t)
			s.addUser(t, testPerson("bob"), goodPassword, nil)
			attrs := map[string][]string{"entryUUID": {"6ba7b810-9dad-11d1-80b4-00c04fd430c8"}}
			if tc.value != "" {
				attrs[tc.rule.Attribute] = []string{tc.value}
			}

			s.dir.AddUser(ldaptest.User{Attributes: attrs, UserName: "alice", Password: goodPassword})

			m := newMethod(t, s.configWith(func(c *ldapauth.Config) { c.Disabled = tc.rule }))

			a, err := m.Verify(t.Context(), credentials("alice", goodPassword), openGate{})

			switch {
			case tc.want == nil && err != nil:
				t.Fatalf("Verify() = %v, want nil", err)
			case tc.want != nil && !errors.Is(err, tc.want):
				t.Fatalf("Verify() = %v, want %v", err, tc.want)
			case errors.Is(tc.want, session.ErrSubjectDisabled) &&
				(a.Subject != "6ba7b810-9dad-11d1-80b4-00c04fd430c8" || a.ProviderID != providerID):
				t.Fatalf("Assertion = %+v, want the subject and the provider", a)
			default:
			}

			if _, err := m.Verify(t.Context(), credentials("bob", goodPassword), openGate{}); err != nil {
				t.Fatalf("Verify(bob) = %v, want nil", err)
			}
		})
	}
}

// TestAnEmptyPasswordNeverReachesTheBind is layer 5 for the bind: the bind
// refuses an empty password also when the boundary check is skipped.
func TestAnEmptyPasswordNeverReachesTheBind(t *testing.T) {
	t.Parallel()

	m := newMethod(t, newSetup(t).configWith(func(*ldapauth.Config) {}))

	if err := ldapauth.BindUserForTest(m, session.NewPassword("")); !errors.Is(err, session.ErrMethodInput) {
		t.Fatalf("bindUser() = %v, want ErrMethodInput", err)
	}
}

// TestEachFaultFailsClosed is layer 5: each fault of the settings, the
// network or the directory refuses the sign-in.
func TestEachFaultFailsClosed(t *testing.T) {
	t.Parallel()

	other := x509.NewCertPool()

	cases := []struct {
		change func(c *ldapauth.Config)
		want   error
		name   string
	}{
		{
			name: "the state of the provider cannot be read", want: session.ErrMethodUnavailable,
			change: func(c *ldapauth.Config) {
				c.Enabled = func(context.Context) (bool, error) { return true, errState }
			},
		},
		{
			name: "the plain-transport setting cannot be read", want: ldapauth.ErrPlainRefused,
			change: func(c *ldapauth.Config) {
				c.URL = strings.Replace(c.URL, "ldaps://127.0.0.1", "ldap://localhost", 1)
				c.AllowPlain = func(context.Context) (bool, error) { return true, errState }
			},
		},
		{
			name: "a certificate of another authority", want: session.ErrMethodUnavailable,
			change: func(c *ldapauth.Config) { c.RootCAs = other },
		},
		{
			name: "a wrong service account password", want: session.ErrMethodUnavailable,
			change: func(c *ldapauth.Config) { c.BindPassword = session.NewPassword(wrongPassword) },
		},
		{
			name: "a base DN that does not exist", want: session.ErrMethodAnswer,
			change: func(c *ldapauth.Config) { c.BaseDN = "dc=other,dc=org" },
		},
		{
			name: "each group outside the group base DN", want: session.ErrGroupsMissing,
			change: func(c *ldapauth.Config) {
				c.Groups = ldapauth.Groups{
					Source: ldapauth.GroupsMemberOf, BaseDN: "ou=sub," + ldaptest.GroupsDN, Filter: "",
					MemberAttribute: "", MemberOfAttribute: "memberOf", NameAttribute: "cn",
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newSetup(t)
			p := testPerson("alice")
			p.Groups, p.GroupsPresent = []string{"ops"}, true
			s.addUser(t, p, goodPassword, nil)

			m := newMethod(t, s.configWith(tc.change))

			a, err := m.Verify(t.Context(), credentials("alice", goodPassword), openGate{})
			if !errors.Is(err, tc.want) || a.Subject != "" {
				t.Fatalf("Verify() = %+v, %v, want %v", a, err, tc.want)
			}
		})
	}
}

// TestACanceledContextIsErrMethodUnavailable checks that the method stops
// when its context ends.
func TestACanceledContextIsErrMethodUnavailable(t *testing.T) {
	t.Parallel()

	s := newSetup(t)
	s.addUser(t, testPerson("alice"), goodPassword, nil)
	m := newMethod(t, s.configWith(func(*ldapauth.Config) {}))

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := m.Verify(ctx, credentials("alice", goodPassword), openGate{}); !errors.Is(err,
		session.ErrMethodUnavailable) {
		t.Fatalf("Verify() = %v, want ErrMethodUnavailable", err)
	}
}

// TestStartTLS checks a sign-in over StartTLS on the plain listener.
func TestStartTLS(t *testing.T) {
	t.Parallel()

	s := newSetup(t)
	s.addUser(t, testPerson("alice"), goodPassword, nil)

	m := newMethod(t, s.configWith(func(c *ldapauth.Config) {
		c.URL, c.StartTLS = s.dir.PlainNameURL(), true
	}))

	if _, err := m.Verify(t.Context(), credentials("alice", goodPassword), openGate{}); err != nil {
		t.Fatalf("Verify() = %v, want nil", err)
	}
}

// syncBuffer is a bytes.Buffer that is safe for concurrent use.
type syncBuffer struct {
	buf bytes.Buffer
	mu  sync.Mutex
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n, err := b.buf.Write(p)
	if err != nil {
		return n, fmt.Errorf("write: %w", err)
	}

	return n, nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.String()
}

// TestNoPasswordInAnyErrorOrLog checks that no error, no log record and no
// form of the Config holds the password of the person or of the service
// account.
func TestNoPasswordInAnyErrorOrLog(t *testing.T) {
	t.Parallel()

	faults := []ldaptest.Fault{
		ldaptest.FaultNone, ldaptest.FaultSizeLimit, ldaptest.FaultSearchReferral, ldaptest.FaultBindReferral,
	}

	for _, f := range faults {
		t.Run(fmt.Sprint(f), func(t *testing.T) {
			t.Parallel()

			s := newSetup(t)
			disabled := testPerson("dave")
			disabled.Disabled = true

			s.addUser(t, testPerson("alice"), goodPassword, nil)
			s.addUser(t, disabled, goodPassword, nil)
			s.dir.SetFault(f)

			var logs syncBuffer

			cfg := s.configWith(func(c *ldapauth.Config) {
				c.Logger = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			})
			m := newMethod(t, cfg)

			var text strings.Builder

			for _, c := range []session.Credentials{
				credentials("alice", wrongPassword), credentials("carol", wrongPassword),
				credentials("dave", wrongPassword), credentials("alice\n", wrongPassword),
			} {
				_, err := m.Verify(t.Context(), c, openGate{})
				fmt.Fprintf(&text, "%v %+v %#v\n", err, err, err)
			}

			fmt.Fprintf(&text, "%v %+v %#v\n", cfg, cfg, cfg)

			bad := cfg
			bad.URL = "http://x"

			_, err := ldapauth.NewMethod(bad)
			fmt.Fprintf(&text, "%v\n%s", err, logs.String())

			for _, secret := range []string{wrongPassword, s.dir.ServicePassword()} {
				if strings.Contains(text.String(), secret) {
					t.Fatalf("an error or a log record holds a password:\n%s", text.String())
				}
			}
		})
	}
}

// TestAnEntryOutsideTheBaseIsErrMethodAnswer is layer 4 for the DNs of
// the directory: each check of a DN against its base refuses an entry that
// the directory answers from another part of the tree, as if the search
// base did not limit the answer.
func TestAnEntryOutsideTheBaseIsErrMethodAnswer(t *testing.T) {
	t.Parallel()

	search := ldapauth.Groups{
		Source: ldapauth.GroupsSearch, BaseDN: ldaptest.GroupsDN, Filter: "(objectClass=groupOfNames)",
		MemberAttribute: "member", MemberOfAttribute: "", NameAttribute: "cn",
	}
	memberOf := ldapauth.Groups{
		Source: ldapauth.GroupsMemberOf, BaseDN: ldaptest.GroupsDN, Filter: "", MemberAttribute: "",
		MemberOfAttribute: "memberOf", NameAttribute: "cn",
	}

	cases := []struct {
		name     string
		groups   ldapauth.Groups
		fault    ldaptest.Fault
		userBind bool
	}{
		{name: "a user outside the base DN", groups: search, fault: ldaptest.FaultUserOutsideBase, userBind: false},
		{
			name:     "a group outside the group base DN",
			groups:   search,
			fault:    ldaptest.FaultGroupOutsideBase,
			userBind: true,
		},
		{
			name:     "the read of a group answers another group",
			groups:   memberOf,
			fault:    ldaptest.FaultGroupOtherEntry,
			userBind: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newSetup(t)
			p := testPerson("alice")
			p.Groups, p.GroupsPresent = []string{"ops"}, true
			s.addUser(t, p, goodPassword, nil)
			s.dir.SetFault(tc.fault)

			m := newMethod(t, s.configWith(func(c *ldapauth.Config) { c.Groups = tc.groups }))

			a, err := m.Verify(t.Context(), credentials("alice", goodPassword), openGate{})
			if !errors.Is(err, session.ErrMethodAnswer) || a.Subject != "" || a.ProviderID != "" {
				t.Fatalf("Verify() = %+v, %v, want ErrMethodAnswer and the zero Assertion", a, err)
			}

			// The groups are read after the bind as the user, so only the
			// check of the user DN comes before it.
			if bound := boundAsUser(s.dir.Binds()); bound != tc.userBind {
				t.Fatalf("binds = %q, want a bind as the user: %v", s.dir.Binds(), tc.userBind)
			}

			if strings.Contains(err.Error(), ldaptest.OutsideDN) {
				t.Fatalf("error %q names the DN", err)
			}
		})
	}
}

// boundAsUser reports whether binds holds a bind that is not the bind of
// the service account.
func boundAsUser(binds []string) bool {
	return slices.ContainsFunc(binds, func(dn string) bool { return !strings.EqualFold(dn, ldaptest.ServiceDN) })
}

// TestAGroupFilterThatChangesItsStructureIsRefused is layer 2 for the
// group filter: a filter with an added item is refused, as if the escape of
// the DN had failed.
func TestAGroupFilterThatChangesItsStructureIsRefused(t *testing.T) {
	t.Parallel()

	m := newMethod(t, newSetup(t).configWith(func(c *ldapauth.Config) {
		c.Groups = ldapauth.Groups{
			Source: ldapauth.GroupsSearch, BaseDN: ldaptest.GroupsDN, Filter: "(objectClass=groupOfNames)",
			MemberAttribute: "member", MemberOfAttribute: "", NameAttribute: "cn",
		}
	}))

	for _, f := range []string{
		"(&(objectClass=groupOfNames)(member=*))(member=*)",
		"(&(objectClass=groupOfNames)(|(member=x)(member=*)))",
		"(&(objectClass=groupOfNames)(member=x)",
	} {
		if _, err := ldapauth.CheckGroupFilterForTest(m, f); !errors.Is(err, session.ErrMethodAnswer) {
			t.Fatalf("checkGroupFilter(%q) = %v, want ErrMethodAnswer", f, err)
		}
	}

	if _, err := ldapauth.CheckGroupFilterForTest(m, `(&(objectClass=groupOfNames)(member=uid\3dx))`); err != nil {
		t.Fatalf("checkGroupFilter() of an escaped DN = %v, want nil", err)
	}
}

// TestTheReadOfTheGroupsHasOneDeadline is layer 3 for the groups: one
// deadline covers the whole read of the groups, shorter than the time of
// one operation.
func TestTheReadOfTheGroupsHasOneDeadline(t *testing.T) {
	t.Parallel()

	const deadline = 300 * time.Millisecond

	s := newSetup(t)
	p := testPerson("alice")
	p.Groups, p.GroupsPresent = []string{"ops"}, true
	s.addUser(t, p, goodPassword, nil)
	s.dir.SetFault(ldaptest.FaultSlowGroups)

	m := newMethod(t, s.configWith(func(c *ldapauth.Config) {
		c.GroupTimeout = deadline
		c.Groups = ldapauth.Groups{
			Source: ldapauth.GroupsSearch, BaseDN: ldaptest.GroupsDN, Filter: "(objectClass=groupOfNames)",
			MemberAttribute: "member", MemberOfAttribute: "", NameAttribute: "cn",
		}
	}))

	start := time.Now()

	a, err := m.Verify(t.Context(), credentials("alice", goodPassword), openGate{})
	if !errors.Is(err, session.ErrMethodUnavailable) || a.Subject != "" {
		t.Fatalf("Verify() = %+v, %v, want ErrMethodUnavailable", a, err)
	}

	if took := time.Since(start); took > ldapauth.MaxTimeout/2 {
		t.Fatalf("Verify took %v, want about %v", took, deadline)
	}
}

// TestASlowRebindIsBoundedByTheGroupTimeout is layer 3 for the groups: the
// deadline of the read of the groups starts before the rebind as the
// service account, so a rebind that the directory holds ends at the
// deadline.
func TestASlowRebindIsBoundedByTheGroupTimeout(t *testing.T) {
	t.Parallel()

	const groupTimeout = 300 * time.Millisecond

	s := newSetup(t)
	p := testPerson("alice")
	p.Groups, p.GroupsPresent = []string{"ops"}, true
	s.addUser(t, p, goodPassword, nil)

	release := make(chan struct{})
	rebind := make(chan struct{})

	var releaseOnce sync.Once

	releaseBinds := func() { releaseOnce.Do(func() { close(release) }) }

	t.Cleanup(releaseBinds)

	var serviceBinds atomic.Int32

	s.dir.SetBindHook(func(dn string) {
		if !strings.EqualFold(dn, ldaptest.ServiceDN) {
			return
		}

		if serviceBinds.Add(1) == 2 {
			close(rebind)
			<-release
		}
	})

	m := newMethod(t, s.configWith(func(c *ldapauth.Config) {
		c.GroupTimeout = groupTimeout
		c.Groups = ldapauth.Groups{
			Source: ldapauth.GroupsSearch, BaseDN: ldaptest.GroupsDN, Filter: "(objectClass=groupOfNames)",
			MemberAttribute: "member", MemberOfAttribute: "", NameAttribute: "cn",
		}
	}))

	type outcome struct {
		assertion session.Assertion
		err       error
	}

	done := make(chan outcome, 1)

	go func() {
		a, err := m.Verify(t.Context(), credentials("alice", goodPassword), openGate{})
		done <- outcome{assertion: a, err: err}
	}()

	select {
	case <-rebind:
	case <-time.After(ldapauth.MaxTimeout):
		t.Fatal("the rebind as the service account did not reach the directory")
	}

	select {
	case got := <-done:
		if !errors.Is(got.err, session.ErrMethodUnavailable) || got.assertion.Subject != "" {
			t.Fatalf("Verify() = %+v, %v, want ErrMethodUnavailable", got.assertion, got.err)
		}
	case <-time.After(ldapauth.MaxTimeout / 2):
		releaseBinds()
		<-done
		t.Fatal("Verify() did not return; the rebind is not bounded by the group timeout")
	}
}

// TestNoDirectoryTextInAnError checks that an error of the method holds no
// diagnostic text of the directory, which a caller could show to a person,
// and that the log of the operator holds it.
func TestNoDirectoryTextInAnError(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		change func(c *ldapauth.Config)
		fault  ldaptest.Fault
	}{
		"a wrong password": {change: func(*ldapauth.Config) {}, fault: ldaptest.FaultNone},
		"a refused service account": {
			change: func(c *ldapauth.Config) { c.BindPassword = session.NewPassword(wrongPassword) },
			fault:  ldaptest.FaultNone,
		},
		"a base DN that is missing": {
			change: func(c *ldapauth.Config) { c.BaseDN = "dc=other,dc=org" },
			fault:  ldaptest.FaultNone,
		},
		"a size limit":             {change: func(*ldapauth.Config) {}, fault: ldaptest.FaultSizeLimit},
		"a referral of the search": {change: func(*ldapauth.Config) {}, fault: ldaptest.FaultSearchReferral},
		"a referral of the bind":   {change: func(*ldapauth.Config) {}, fault: ldaptest.FaultBindReferral},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := newSetup(t)
			s.addUser(t, testPerson("alice"), goodPassword, nil)
			s.dir.SetFault(tc.fault)

			var logs syncBuffer

			m := newMethod(t, s.configWith(func(c *ldapauth.Config) {
				tc.change(c)
				c.Logger = slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
			}))

			_, err := m.Verify(t.Context(), credentials("alice", wrongPassword), openGate{})
			if err == nil {
				t.Fatal("Verify() = nil, want an error")
			}

			if text := fmt.Sprintf("%v %+v", err, err); strings.Contains(text, ldaptest.DiagnosticPrefix) {
				t.Fatalf("error %q holds the text of the directory", text)
			}

			if !strings.Contains(logs.String(), ldaptest.DiagnosticPrefix) {
				t.Fatalf("the log holds no text of the directory:\n%s", logs.String())
			}
		})
	}
}

// TestNestedGroupsOverTheLimitAreErrMethodAnswer is layer 3 for the walk of
// nested groups: the person is in one group, and that group reaches more
// groups than the limit through a chain.
func TestNestedGroupsOverTheLimitAreErrMethodAnswer(t *testing.T) {
	t.Parallel()

	for _, groups := range []ldapauth.Groups{
		{
			Source: ldapauth.GroupsSearch, BaseDN: ldaptest.GroupsDN, Filter: "(objectClass=groupOfNames)",
			MemberAttribute: "member", MemberOfAttribute: "", NameAttribute: "cn",
		},
		{
			Source: ldapauth.GroupsMemberOf, BaseDN: ldaptest.GroupsDN, Filter: "", MemberAttribute: "",
			MemberOfAttribute: "memberOf", NameAttribute: "cn",
		},
	} {
		t.Run(string(groups.Source), func(t *testing.T) {
			t.Parallel()

			s := newSetup(t)
			s.addUser(t, testPerson("alice"), goodPassword, nil)
			s.dir.AddGroup(ldaptest.Group{Name: "g-0", Users: []string{"alice"}, Groups: nil, Link: ldaptest.LinkBoth})

			for i := 1; i <= ldapauth.MaxGroups; i++ {
				s.dir.AddGroup(ldaptest.Group{
					Name: fmt.Sprintf("g-%d", i), Users: nil, Groups: []string{fmt.Sprintf("g-%d", i-1)},
					Link: ldaptest.LinkBoth,
				})
			}

			m := newMethod(t, s.configWith(func(c *ldapauth.Config) { c.Groups = groups }))

			a, err := m.Verify(t.Context(), credentials("alice", goodPassword), openGate{})
			if !errors.Is(err, session.ErrMethodAnswer) || a.Subject != "" {
				t.Fatalf("Verify() = %+v, %v, want ErrMethodAnswer", a, err)
			}
		})
	}
}
