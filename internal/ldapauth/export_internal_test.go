package ldapauth

import (
	"context"
	"syscall"

	"github.com/go-ldap/ldap/v3"

	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/session"
)

// CheckFilterForTest calls checkFilter, so a test can give it a filter that
// EscapeFilter never makes.
func CheckFilterForTest(m *Method, f string) (string, error) {
	return m.checkFilter(f)
}

// PlainRuleForTest is the rule of the dial guard for a connection with no
// TLS.
type PlainRuleForTest = plainRule

// Rules of the dial guard for a connection with no TLS.
const (
	PlainNotUsedForTest      = plainNotUsed
	PlainLoopbackOnlyForTest = plainLoopbackOnly
	PlainAllowedForTest      = plainAllowed
)

// DialControlForTest returns the Control function of the dialer, so a test
// can give it an address that no test directory has.
func DialControlForTest(rules egress.Rules, rule plainRule) func(string, string, syscall.RawConn) error {
	return dialControl(rules, rule)
}

// OpenForTest calls open, so a test can give the method a plain URL and
// check the refusal of that layer. The caller closes the connection.
func OpenForTest(ctx context.Context, m *Method) (*ldap.Conn, func(), error) {
	return m.open(ctx)
}

// BindUserForTest calls bindUser with no connection, so a test can check
// that an empty password stops before the connection is used.
func BindUserForTest(m *Method, password session.Password) error {
	return m.bindUser(context.Background(), nil, "uid=alice,dc=example,dc=org", password)
}

// CheckGroupFilterForTest calls checkGroupFilter, so a test can give it a
// filter that EscapeFilter never makes.
func CheckGroupFilterForTest(m *Method, f string) (string, error) {
	return m.checkGroupFilter(f)
}
