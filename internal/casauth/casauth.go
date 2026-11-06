// Package casauth is the adapter of the port session.RedirectMethod for a
// CAS 3.0 server. The method sends the browser to <cas>/login and checks the
// service ticket of the callback with a server-to-server call to
// <cas>/p3/serviceValidate, over HTTPS only, through the egress rules. It
// uses net/http and encoding/xml only. It returns a session.Assertion and
// never makes a principal or a session.
package casauth

import (
	"context"
	"crypto/subtle"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/session"
)

// Limits and fixed values of the method.
const (
	// MaxTicket is the longest service ticket, in characters.
	MaxTicket = 256
	// TicketPrefix starts each service ticket. A proxy ticket starts with
	// PT- and is refused.
	TicketPrefix = "ST-"
	// DefaultTimeout bounds one validation request when Config.Timeout is
	// zero.
	DefaultTimeout = egress.Timeout
	// ClockSkew is the clock skew that the method allows for the
	// authentication time of an answer.
	ClockSkew = session.AssertionSkew
	// maxCallbackValue is the largest state or provider error of a
	// callback, in bytes.
	maxCallbackValue = 2048
	// authDateAttribute is the attribute of a CAS 3.0 server that holds
	// the time of the authentication.
	authDateAttribute = "authenticationDate"
	// newLoginAttribute is the attribute of a CAS 3.0 server that tells
	// whether the ticket comes from a new login.
	newLoginAttribute = "isFromNewLogin"
	// callbackPrefix and callbackSuffix enclose the provider ID in the
	// path of the callback of the server.
	callbackPrefix = "/api/server/sign-in/"
	callbackSuffix = "/callback"
)

// ErrConfig reports a configuration of the method that is not valid.
var ErrConfig = errors.New("CAS method configuration is not valid")

// ticketPattern is the allowlist of a service ticket.
var ticketPattern = regexp.MustCompile(`^ST-[A-Za-z0-9._-]{1,253}$`)

// attributePattern is the allowlist of an attribute name in the settings.
var attributePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_.-]{0,63}$`)

// Config configures a Method.
type Config struct {
	// Enabled reports whether the provider is on. The method calls it at
	// each Begin and Finish. It returns an error when the state of the
	// provider cannot be read. Required.
	Enabled func(ctx context.Context) (bool, error)
	// Tickets records the used tickets. Required.
	Tickets session.UsedTickets
	// Now is the clock. Nil uses time.Now.
	Now func() time.Time
	// RootCAs holds the certificate authorities that the method trusts for
	// the CAS server. Nil means the system roots.
	RootCAs *x509.CertPool
	// Resolver answers the name lookups of the method. Nil means the system
	// resolver. The dial guard checks each address that it returns.
	Resolver *net.Resolver
	// ServerURL is the base URL of the CAS server, for example
	// https://cas.example.com/cas. It must be an absolute https URL with no
	// query, fragment or user information. Required.
	ServerURL string
	// ExternalURL is the configured external URL of the installation. The
	// service URL starts with it. It must be an absolute https URL with no
	// query, fragment or user information. Required.
	ExternalURL string
	// ProviderID names the sign-in provider. Required.
	ProviderID string
	// SubjectAttribute names the attribute that holds the subject. Empty
	// uses the CAS user. A rename of the user in the CAS server then makes
	// a new subject.
	SubjectAttribute string
	// UserNameAttribute names the attribute that holds the user name. Empty
	// uses the CAS user.
	UserNameAttribute string
	// NameAttribute names the attribute that holds the display name, or is
	// empty.
	NameAttribute string
	// EmailAttribute names the attribute that holds the email, or is empty.
	EmailAttribute string
	// GroupsAttribute names the attribute that holds the groups. Empty
	// turns groups off.
	GroupsAttribute string
	// Rules are the egress rules of the validation request.
	Rules egress.Rules
	// Timeout bounds one validation request. Zero uses DefaultTimeout.
	Timeout time.Duration
}

// Method is the sign-in method of one CAS 3.0 server. It implements
// session.RedirectMethod. It is safe for concurrent use.
type Method struct {
	enabled     func(ctx context.Context) (bool, error)
	tickets     session.UsedTickets
	now         func() time.Time
	client      *http.Client
	server      *url.URL
	external    *url.URL
	providerID  string
	subjectAttr string
	userAttr    string
	nameAttr    string
	emailAttr   string
	groupsAttr  string
	timeout     time.Duration
}

var _ session.RedirectMethod = (*Method)(nil)

// New returns the method of cfg. It returns ErrConfig for a missing switch
// or ticket store, a provider ID that is not valid, a server URL or an
// external URL that is not an absolute https URL or that the egress rules
// refuse, an attribute name outside the allowlist, or a negative timeout.
func New(cfg Config) (*Method, error) {
	if err := checkConfig(cfg); err != nil {
		return nil, err
	}

	server, err := baseURL(cfg.ServerURL)
	if err != nil {
		return nil, fmt.Errorf("%w: the CAS server URL: %w", ErrConfig, err)
	}

	if err = cfg.Rules.CheckURL(server.String(), egress.HTTPSOnly); err != nil {
		return nil, fmt.Errorf("%w: the CAS server URL: %w", ErrConfig, err)
	}

	external, err := baseURL(cfg.ExternalURL)
	if err != nil {
		return nil, fmt.Errorf("%w: the external URL: %w", ErrConfig, err)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	timeout := cfg.Timeout
	if timeout == 0 {
		timeout = DefaultTimeout
	}

	client := egresshttp.ClientWith(cfg.Rules, egress.NoRedirect, egress.HTTPSOnly,
		egresshttp.Options{RootCAs: cfg.RootCAs, Resolver: cfg.Resolver})
	client.Timeout = timeout

	return &Method{
		enabled: cfg.Enabled, tickets: cfg.Tickets, now: now, client: client, server: server, external: external,
		providerID: cfg.ProviderID, subjectAttr: cfg.SubjectAttribute, userAttr: cfg.UserNameAttribute,
		nameAttr: cfg.NameAttribute, emailAttr: cfg.EmailAttribute, groupsAttr: cfg.GroupsAttribute,
		timeout: timeout,
	}, nil
}

// checkConfig checks each setting of cfg except the URLs.
func checkConfig(cfg Config) error {
	switch {
	case cfg.Enabled == nil:
		return fmt.Errorf("%w: the switch of the provider is missing", ErrConfig)
	case cfg.Tickets == nil:
		return fmt.Errorf("%w: the store of used tickets is missing", ErrConfig)
	case !session.ValidProviderID(cfg.ProviderID):
		return fmt.Errorf("%w: the provider ID is not valid", ErrConfig)
	case cfg.Timeout < 0:
		return fmt.Errorf("%w: the timeout is negative", ErrConfig)
	}

	for _, a := range []string{
		cfg.SubjectAttribute, cfg.UserNameAttribute, cfg.NameAttribute, cfg.EmailAttribute, cfg.GroupsAttribute,
	} {
		if a != "" && !attributePattern.MatchString(a) {
			return fmt.Errorf("%w: an attribute name must match %s", ErrConfig, attributePattern)
		}
	}

	return nil
}

// errURL reports a URL of the settings that is not an absolute https URL
// with a host and no query, fragment or user information.
var errURL = errors.New("the URL must be an absolute https URL with a host and no user information, query or " +
	"fragment")

// baseURL parses raw as an absolute https URL with a host and no query,
// fragment or user information, and removes a trailing slash of its path.
func baseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: the URL does not parse", errURL)
	}

	if u.Scheme != "https" || u.Host == "" || u.Opaque != "" || u.User != nil || u.RawQuery != "" ||
		u.ForceQuery || u.Fragment != "" {
		return nil, errURL
	}

	u.Path = strings.TrimRight(u.Path, "/")
	u.RawPath = ""

	return u, nil
}

// Close closes the idle connections of the method.
func (m *Method) Close() {
	m.client.CloseIdleConnections()
}

// Kind implements session.RedirectMethod.
func (m *Method) Kind() session.MethodKind {
	return session.MethodCAS
}

// ServiceURL returns the service URL of the sign-in with the state: the
// callback of the provider under the external URL, with the state as its
// only query parameter. It never uses a value of a request.
func (m *Method) ServiceURL(state string) string {
	u := *m.external
	u.Path += callbackPrefix + m.providerID + callbackSuffix
	u.RawQuery = url.Values{"state": {state}}.Encode()

	return u.String()
}

// Begin implements session.RedirectMethod. A fresh sign-in sends
// renew=true.
func (m *Method) Begin(ctx context.Context, in session.BeginInput) (session.Redirect, error) {
	var none session.Redirect

	if in.ProviderID != m.providerID {
		return none, fmt.Errorf("%w: the provider ID is not the provider of the method", session.ErrMethodInput)
	}

	if !validCallbackValue(in.State, 1) {
		return none, fmt.Errorf("%w: the state must have 1 to %d printable ASCII bytes", session.ErrMethodInput,
			maxCallbackValue)
	}

	if err := m.checkEnabled(ctx); err != nil {
		return none, err
	}

	q := url.Values{"service": {m.ServiceURL(in.State)}}
	if in.Fresh {
		q.Set("renew", "true")
	}

	login := m.server.JoinPath("login")
	login.RawQuery = q.Encode()

	return session.Redirect{URL: login.String(), Pending: session.PendingRedirect{
		StartedAt: m.now(), ProviderID: m.providerID, State: in.State, Nonce: "", Verifier: "",
		RequestID: "", PrincipalID: "", SignInID: "", Fresh: in.Fresh,
	}}, nil
}

// Finish implements session.RedirectMethod. It records the ticket as used
// before it sends it to the CAS server. On any error, the Assertion is the
// zero value.
func (m *Method) Finish(ctx context.Context, cb session.Callback) (session.Assertion, error) {
	var none session.Assertion

	if err := checkCallback(cb); err != nil {
		return none, err
	}

	if err := m.bind(cb); err != nil {
		return none, err
	}

	if cb.ProviderError != "" {
		return none, fmt.Errorf("%w: the identity provider answered an error", session.ErrMethodRejected)
	}

	if cb.Ticket == "" {
		return none, fmt.Errorf("%w: the callback has no ticket", session.ErrMethodInput)
	}

	if err := m.checkEnabled(ctx); err != nil {
		return none, err
	}

	// bind refused a nil pending request.
	pending := *cb.Pending

	if err := m.use(ctx, cb.Ticket); err != nil {
		return none, err
	}

	resp, err := m.validate(ctx, pending, cb.Ticket)
	if err != nil {
		return none, err
	}

	return m.assertion(resp, pending)
}

// use records the ticket as used. It fails closed: an error of the store
// refuses the sign-in.
func (m *Method) use(ctx context.Context, ticket string) error {
	err := m.tickets.Use(ctx, ticket, m.now().Add(session.UsedTicketTTL))

	switch {
	case err == nil:
		return nil
	case errors.Is(err, session.ErrReplay):
		return fmt.Errorf("%w: the ticket was already used", session.ErrReplay)
	case errors.Is(err, session.ErrMethodInput):
		return fmt.Errorf("%w: the store refused the ticket", session.ErrMethodInput)
	default:
		return fmt.Errorf("%w: record the ticket: %w", session.ErrMethodUnavailable, err)
	}
}

// validate sends the ticket of the sign-in pending to the validation
// endpoint of the CAS server and returns its parsed answer. A fresh sign-in
// sends renew=true. It never sends pgtUrl.
func (m *Method) validate(ctx context.Context, pending session.PendingRedirect, ticket string) (Response, error) {
	var none Response

	q := url.Values{"service": {m.ServiceURL(pending.State)}, "ticket": {ticket}}
	if pending.Fresh {
		q.Set("renew", "true")
	}

	target := m.server.JoinPath("p3", "serviceValidate")
	target.RawQuery = q.Encode()

	// Defense in depth: New refuses a server URL that is not https, and the
	// egress client refuses each URL that is not https. This check stays so
	// that a change of either cannot send a ticket over plain HTTP.
	if target.Scheme != "https" || target.Host == "" {
		return none, fmt.Errorf("%w: the validation URL is not an absolute https URL", session.ErrMethodUnavailable)
	}

	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), http.NoBody)
	if err != nil {
		return none, fmt.Errorf("%w: build the validation request: %w", session.ErrMethodUnavailable, err)
	}

	req.Header.Set("Accept", "application/xml")

	res, err := m.client.Do(req)
	if err != nil {
		return none, fmt.Errorf("%w: the validation request failed: %w", session.ErrMethodUnavailable, err)
	}

	body, err := readAnswer(res)
	if err != nil {
		return none, err
	}

	return answerOf(body)
}

// answerOf returns the parsed answer of body, and translates a failure
// answer into an error of package session.
func answerOf(body []byte) (Response, error) {
	parsed, err := ParseResponse(body)
	if err != nil {
		return Response{}, fmt.Errorf("%w: %w", session.ErrMethodAnswer, err)
	}

	switch {
	case parsed.Success:
		return parsed, nil
	case parsed.FailureCode == "INTERNAL_ERROR":
		return Response{}, fmt.Errorf("%w: the CAS server answered %s", session.ErrMethodUnavailable,
			parsed.FailureCode)
	default:
		return Response{}, fmt.Errorf("%w: the CAS server answered %s", session.ErrMethodRejected, parsed.FailureCode)
	}
}

// readAnswer reads at most MaxAnswerBytes of the body of res, closes it,
// and translates each failure into an error of package session: a status
// of 500 or more and a failed read are session.ErrMethodUnavailable; each
// other status that is not 200, and a larger body, are
// session.ErrMethodAnswer.
func readAnswer(res *http.Response) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(res.Body, MaxAnswerBytes+1))

	if closeErr := res.Body.Close(); closeErr != nil && err == nil {
		err = closeErr
	}

	switch {
	case res.StatusCode >= http.StatusInternalServerError:
		return nil, fmt.Errorf("%w: the CAS server answered %d", session.ErrMethodUnavailable, res.StatusCode)
	case res.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%w: the CAS server answered %d", session.ErrMethodAnswer, res.StatusCode)
	case err != nil:
		return nil, fmt.Errorf("%w: read the answer: %w", session.ErrMethodUnavailable, err)
	case len(b) > MaxAnswerBytes:
		return nil, fmt.Errorf("%w: the answer is larger than %d bytes", session.ErrMethodAnswer, MaxAnswerBytes)
	}

	return b, nil
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

// checkCallback is the boundary check of a callback: the size and the
// characters of each value, the allowlist of the ticket, and no value of
// another protocol.
func checkCallback(cb session.Callback) error {
	for _, v := range []string{cb.State, cb.ProviderError} {
		if !validCallbackValue(v, 0) {
			return fmt.Errorf("%w: a callback value must have at most %d printable ASCII bytes",
				session.ErrMethodInput, maxCallbackValue)
		}
	}

	if cb.Code != "" || len(cb.SAMLResponse) > 0 {
		return fmt.Errorf("%w: the callback holds a value of another protocol", session.ErrMethodInput)
	}

	if cb.Ticket != "" && !ValidTicket(cb.Ticket) {
		return fmt.Errorf("%w: the ticket must start with %s, have at most %d characters and hold only "+
			"A-Z, a-z, 0-9, '-', '.' and '_'", session.ErrMethodInput, TicketPrefix, MaxTicket)
	}

	return nil
}

// ValidTicket reports whether t is a service ticket of the allowlist: the
// prefix ST-, at most MaxTicket characters, and only A-Z, a-z, 0-9, '-',
// '.' and '_'.
func ValidTicket(t string) bool {
	return len(t) <= MaxTicket && ticketPattern.MatchString(t)
}

// bind returns session.ErrRequestBinding unless the callback belongs to its
// pending request: a pending request of this provider, with the state of
// the callback. The compare of the state takes constant time.
func (m *Method) bind(cb session.Callback) error {
	if cb.Pending == nil {
		return fmt.Errorf("%w: the browser holds no sign-in request", session.ErrRequestBinding)
	}

	if cb.Pending.ProviderID != m.providerID {
		return fmt.Errorf("%w: the sign-in request is of another provider", session.ErrRequestBinding)
	}

	if cb.State == "" || cb.Pending.State == "" ||
		subtle.ConstantTimeCompare([]byte(cb.State), []byte(cb.Pending.State)) != 1 {
		return fmt.Errorf("%w: the state does not match", session.ErrRequestBinding)
	}

	return nil
}

// validCallbackValue reports whether v has minLen to maxCallbackValue bytes
// and only printable ASCII.
func validCallbackValue(v string, minLen int) bool {
	if len(v) < minLen || len(v) > maxCallbackValue {
		return false
	}

	for i := range len(v) {
		if v[i] < 0x20 || v[i] > 0x7e {
			return false
		}
	}

	return true
}
