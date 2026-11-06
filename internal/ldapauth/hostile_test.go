package ldapauth_test

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
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
		name    string
		address string
		want    error
		rules   egress.Rules
		rule    ldapauth.PlainRuleForTest
	}{
		{"a metadata address", "169.254.169.254:389", egresshttp.ErrDialRefused, private, ldapauth.PlainNotUsedForTest},
		{
			"a private address outside the rules", "192.168.1.1:636", egresshttp.ErrDialRefused, private,
			ldapauth.PlainNotUsedForTest,
		},
		{
			"loopback with no test rule",
			"127.0.0.1:636",
			egresshttp.ErrDialRefused,
			public,
			ldapauth.PlainNotUsedForTest,
		},
		{
			"plain to a private address",
			"10.0.0.5:389",
			ldapauth.ErrPlainRefused,
			private,
			ldapauth.PlainLoopbackOnlyForTest,
		},
		{"plain to a private address with the setting", "10.0.0.5:389", nil, private, ldapauth.PlainAllowedForTest},
		{"plain to loopback", "127.0.0.1:389", nil, private, ldapauth.PlainLoopbackOnlyForTest},
		{"TLS to a private address", "10.0.0.5:636", nil, private, ldapauth.PlainNotUsedForTest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := ldapauth.DialControlForTest(tc.rules, tc.rule)("tcp", tc.address, nil)

			if (tc.want == nil) != (err == nil) || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("control(%q) = %v, want %v", tc.address, err, tc.want)
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
		Attributes: map[string][]string{"employeeNumber": {"E-1042"}}, UserName: "alice", Password: goodPassword,
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
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newSetup(t)
			s.addUser(t, testPerson("bob"), goodPassword, nil)
			s.dir.AddUser(ldaptest.User{
				Attributes: map[string][]string{
					"entryUUID": {"6ba7b810-9dad-11d1-80b4-00c04fd430c8"}, tc.rule.Attribute: {tc.value},
				},
				UserName: "alice", Password: goodPassword,
			})

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
