package ldapauth

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/go-ldap/ldap/v3"

	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/session"
)

// userSizeLimit is the size limit of the user search: a second entry
// refuses the sign-in.
const userSizeLimit = 2

// errRefused is the one error of an unknown user name and of a wrong
// password, so that the error does not tell that a user exists.
var errRefused = fmt.Errorf("%w: the directory refused the user name or the password", session.ErrMethodRejected)

// Method is the sign-in method of one LDAP directory. It implements
// session.PasswordMethod. It is safe for concurrent use: each Verify opens
// its own connection.
type Method struct {
	enabled          func(ctx context.Context) (bool, error)
	allowPlainFn     func(ctx context.Context) (bool, error)
	now              func() time.Time
	logger           *slog.Logger
	rootCAs          *x509.CertPool
	baseDN           *ldap.DN
	groupBaseDN      *ldap.DN
	providerID       string
	bindDN           string
	baseDNText       string
	groupBaseText    string
	userFilter       string
	userNameAttr     string
	emailAttr        string
	nameAttr         string
	bindPassword     session.Password
	endpoint         endpoint
	subject          Subject
	disabled         Disabled
	groups           Groups
	egress           egress.Rules
	filterNodes      int
	connectTimeout   time.Duration
	operationTimeout time.Duration
}

var _ session.PasswordMethod = (*Method)(nil)

// NewMethod returns the method of cfg. It returns ErrConfig for a setting
// that is not valid. The error never holds the password of the service
// account.
func NewMethod(cfg Config) (*Method, error) {
	ep, err := checkConfig(cfg)
	if err != nil {
		return nil, err
	}

	bindDN, _, err := canonicalDN(cfg.BindDN)
	if err != nil {
		return nil, configError("the bind DN is not a valid DN")
	}

	baseText, base, err := canonicalDN(cfg.BaseDN)
	if err != nil {
		return nil, configError("the base DN is not a valid DN")
	}

	var (
		groupBase     *ldap.DN
		groupBaseText string
	)

	if cfg.Groups.Source != GroupsOff {
		if groupBaseText, groupBase, err = canonicalDN(cfg.Groups.BaseDN); err != nil {
			return nil, configError("the group base DN is not a valid DN")
		}
	}

	nodes, err := filterNodes(strings.ReplaceAll(cfg.UserFilter, UserPlaceholder, "x"))
	if err != nil {
		return nil, configError("the user filter does not compile")
	}

	return &Method{
		enabled: cfg.Enabled, allowPlainFn: cfg.AllowPlain, now: orNow(cfg.Now), logger: orDiscard(cfg.Logger),
		rootCAs: cfg.RootCAs, baseDN: base, groupBaseDN: groupBase, providerID: cfg.ProviderID, bindDN: bindDN,
		baseDNText: baseText, groupBaseText: groupBaseText, userFilter: cfg.UserFilter,
		userNameAttr: cfg.UserNameAttribute, emailAttr: cfg.EmailAttribute, nameAttr: cfg.NameAttribute,
		bindPassword: cfg.BindPassword, endpoint: ep, subject: cfg.Subject, disabled: cfg.Disabled,
		groups: cfg.Groups, egress: cfg.Egress, filterNodes: nodes,
		connectTimeout: orMax(cfg.ConnectTimeout), operationTimeout: orMax(cfg.OperationTimeout),
	}, nil
}

// orNow returns now, or time.Now when now is nil.
func orNow(now func() time.Time) func() time.Time {
	if now == nil {
		return time.Now
	}

	return now
}

// orDiscard returns l, or a logger that discards each record when l is nil.
func orDiscard(l *slog.Logger) *slog.Logger {
	if l == nil {
		return slog.New(slog.DiscardHandler)
	}

	return l
}

// orMax returns d, or MaxTimeout when d is zero.
func orMax(d time.Duration) time.Duration {
	if d == 0 {
		return MaxTimeout
	}

	return d
}

// Kind implements session.PasswordMethod.
func (m *Method) Kind() session.MethodKind {
	return session.MethodLDAP
}

// Verify implements session.PasswordMethod. On any error the Assertion is
// the zero value, except for session.ErrSubjectDisabled. Each error and each
// log record holds no password.
func (m *Method) Verify(ctx context.Context, c session.Credentials, gate session.BindGate) (session.Assertion, error) {
	var none session.Assertion

	a, err := m.signIn(ctx, c, gate)
	if err != nil {
		m.logger.LogAttrs(ctx, slog.LevelInfo, "ldap sign-in refused",
			slog.String("provider", m.providerID), slog.String("reason", err.Error()))

		if errors.Is(err, session.ErrSubjectDisabled) {
			return a, err
		}

		return none, err
	}

	return a, nil
}

// signIn is Verify with no log record.
func (m *Method) signIn(ctx context.Context, c session.Credentials, gate session.BindGate) (session.Assertion, error) {
	var none session.Assertion

	if err := m.checkInput(c, gate); err != nil {
		return none, err
	}

	if err := m.checkEnabled(ctx); err != nil {
		return none, err
	}

	conn, done, err := m.open(ctx)
	if err != nil {
		return none, err
	}
	defer done()

	entry, userDN, err := m.findUser(conn, c.UserName)
	if err != nil {
		return none, err
	}

	subject, err := m.subjectOf(entry)
	if err != nil {
		return none, fmt.Errorf("%w: %w", session.ErrMethodAnswer, err)
	}

	if derr := m.checkDisabled(entry); derr != nil {
		return session.Assertion{
			AuthTime: time.Time{}, ProviderID: m.providerID, PrincipalID: "", Subject: subject, UserName: "",
			Email: "", Method: session.MethodLDAP, Names: nil, Groups: nil, Factors: nil, EmailVerified: false,
			GroupsPresent: false, AuthTimeKnown: false,
		}, derr
	}

	if gerr := gate.BeforeBind(ctx, subject); gerr != nil {
		return none, fmt.Errorf("the bind gate stopped the bind: %w", gerr)
	}

	authTime := m.now().Truncate(time.Millisecond)

	if berr := m.bindUser(conn, userDN, c.Password); berr != nil {
		return none, berr
	}

	groups, err := m.groupsOf(conn, entry, userDN)
	if err != nil {
		return none, err
	}

	return m.assertion(entry, c.UserName, subject, groups, authTime)
}

// checkInput is the boundary check of the credentials and the gate.
func (m *Method) checkInput(c session.Credentials, gate session.BindGate) error {
	switch {
	case c.ProviderID != m.providerID:
		return fmt.Errorf("%w: the provider ID is not the provider of the method", session.ErrMethodInput)
	case gate == nil:
		return fmt.Errorf("%w: the bind gate is missing", session.ErrMethodInput)
	case c.UserName == "" || len(c.UserName) > session.MaxUserName || !validText(c.UserName):
		return fmt.Errorf("%w: the user name must have 1 to %d bytes of UTF-8 and no control character",
			session.ErrMethodInput, session.MaxUserName)
	case c.Password.Reveal() == "":
		// An empty password makes an unauthenticated bind, which many
		// directories accept.
		return fmt.Errorf("%w: the password is empty", session.ErrMethodInput)
	case len(c.Password.Reveal()) > MaxPassword:
		return fmt.Errorf("%w: the password is longer than %d bytes", session.ErrMethodInput, MaxPassword)
	}

	return nil
}

// checkEnabled returns session.ErrProviderDisabled for a provider that is
// off, and session.ErrMethodUnavailable when its state cannot be read. It
// fails closed.
func (m *Method) checkEnabled(ctx context.Context) error {
	on, err := m.enabled(ctx)
	if err != nil {
		return fmt.Errorf("%w: read the state of the provider: %w", session.ErrMethodUnavailable, err)
	}

	if !on {
		return session.ErrProviderDisabled
	}

	return nil
}

// userFilterFor returns the user filter with the escaped user name.
func (m *Method) userFilterFor(userName string) (string, error) {
	return m.checkFilter(strings.ReplaceAll(m.userFilter, UserPlaceholder, EscapeFilter(userName)))
}

// checkFilter returns f, or ErrMethodInput when the count of the items of
// f is not the count of the template, so that a value that adds an item
// never reaches the directory.
func (m *Method) checkFilter(f string) (string, error) {
	// Defense in depth: EscapeFilter escapes each special byte, and the
	// boundary check refuses control characters. This check stays so that a
	// change of the escape cannot add an item to the filter.
	n, err := filterNodes(f)
	if err != nil || n != m.filterNodes {
		return "", fmt.Errorf("%w: the user name changes the structure of the filter", session.ErrMethodInput)
	}

	return f, nil
}

// findUser searches for the one entry of the user name, and returns it
// with its canonical DN.
func (m *Method) findUser(conn *ldap.Conn, userName string) (*ldap.Entry, string, error) {
	filter, err := m.userFilterFor(userName)
	if err != nil {
		return nil, "", err
	}

	attrs := m.userAttributes()
	req := ldap.NewSearchRequest(m.baseDNText, ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, userSizeLimit,
		int(m.operationTimeout/time.Second), false, filter, attrs, nil)

	res, err := conn.Search(req)
	if err != nil {
		return nil, "", searchError("the user search", err)
	}

	switch {
	case len(res.Entries) == 0 && len(res.Referrals) > 0:
		return nil, "", fmt.Errorf("%w: the user search answered only a referral, which the method never follows",
			session.ErrMethodAnswer)
	case len(res.Entries) == 0:
		return nil, "", errRefused
	case len(res.Entries) > 1:
		return nil, "", fmt.Errorf("%w: the user search found more than one user", session.ErrMethodAnswer)
	}

	entry := res.Entries[0]

	dn, parsed, err := canonicalDN(entry.DN)
	if err != nil {
		return nil, "", fmt.Errorf("%w: the DN of the user is not valid", session.ErrMethodAnswer)
	}

	// Defense in depth: the search starts at the base DN. This check stays
	// so that a directory that answers an entry from another part of the
	// tree never receives a bind for it.
	if !m.baseDN.AncestorOfFold(parsed) {
		return nil, "", fmt.Errorf("%w: the DN of the user is not below the base DN", session.ErrMethodAnswer)
	}

	return entry, dn, nil
}

// userAttributes returns the attributes that the user search reads.
func (m *Method) userAttributes() []string {
	out := []string{m.subjectAttribute()}

	for _, a := range []string{m.userNameAttr, m.emailAttr, m.nameAttr, m.disabled.Attribute} {
		if a != "" {
			out = append(out, a)
		}
	}

	if m.groups.Source == GroupsMemberOf {
		out = append(out, m.groups.MemberOfAttribute)
	}

	return out
}

// searchError translates an error of a search.
func searchError(what string, err error) error {
	switch {
	case ldap.IsErrorWithCode(err, ldap.LDAPResultSizeLimitExceeded):
		return fmt.Errorf("%w: %s is over the size limit of the directory", session.ErrMethodAnswer, what)
	case ldap.IsErrorWithCode(err, ldap.LDAPResultReferral):
		return fmt.Errorf("%w: %s answered a referral, which the method never follows", session.ErrMethodAnswer,
			what)
	case network(err):
		return fmt.Errorf("%w: %s: %w", session.ErrMethodUnavailable, what, err)
	default:
		return fmt.Errorf("%w: %s: %w", session.ErrMethodAnswer, what, err)
	}
}

// checkDisabled returns session.ErrSubjectDisabled when the entry e has
// the disabled value of the setting. A value that does not parse fails
// closed with session.ErrMethodAnswer.
func (m *Method) checkDisabled(e *ldap.Entry) error {
	d := m.disabled
	if d.Attribute == "" {
		return nil
	}

	for _, v := range e.GetEqualFoldAttributeValues(d.Attribute) {
		if d.Mask != 0 {
			n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
			if err != nil {
				return fmt.Errorf("%w: the disabled attribute is not a number", session.ErrMethodAnswer)
			}

			if n&d.Mask != 0 {
				return session.ErrSubjectDisabled
			}

			continue
		}

		for _, want := range d.Values {
			if strings.EqualFold(v, want) {
				return session.ErrSubjectDisabled
			}
		}
	}

	return nil
}

// bindUser binds as the user of dn with the password.
func (m *Method) bindUser(conn *ldap.Conn, dn string, password session.Password) error {
	// Defense in depth: checkInput refuses an empty password. This check
	// stays so that a new caller cannot make an unauthenticated bind.
	if password.Reveal() == "" {
		return fmt.Errorf("%w: the password is empty", session.ErrMethodInput)
	}

	err := conn.Bind(dn, password.Reveal())

	switch {
	case err == nil:
		return nil
	case ldap.IsErrorWithCode(err, ldap.LDAPResultReferral):
		return fmt.Errorf("%w: the bind answered a referral, which the method never follows",
			session.ErrMethodAnswer)
	case network(err):
		return fmt.Errorf("%w: bind as the user: %w", session.ErrMethodUnavailable, err)
	default:
		return errRefused
	}
}

// assertion builds the Assertion of the entry e and checks it.
func (m *Method) assertion(
	e *ldap.Entry, typed, subject string, groups []string, authTime time.Time,
) (session.Assertion, error) {
	var none session.Assertion

	userName := typed
	if m.userNameAttr != "" {
		userName = e.GetEqualFoldAttributeValue(m.userNameAttr)
	}

	var names []string
	if v := m.attr(e, m.nameAttr); v != "" {
		names = []string{v}
	}

	a := session.Assertion{
		AuthTime: authTime, ProviderID: m.providerID, PrincipalID: "", Subject: subject, UserName: userName,
		Email: m.attr(e, m.emailAttr), Method: session.MethodLDAP, Names: names, Groups: groups,
		Factors: []session.Factor{session.FactorPassword}, EmailVerified: false,
		GroupsPresent: m.groups.Source != GroupsOff, AuthTimeKnown: true,
	}

	// Defense in depth: the sign-in service checks each Assertion too. This
	// check stays so that the method never returns a value over a limit.
	if err := session.CheckAssertion(a, m.now()); err != nil {
		return none, fmt.Errorf("%w: %w", session.ErrMethodAnswer, err)
	}

	return a, nil
}

// attr returns the first value of the attribute name of e, or "" when name
// is empty.
func (m *Method) attr(e *ldap.Entry, name string) string {
	if name == "" {
		return ""
	}

	return e.GetEqualFoldAttributeValue(name)
}
