package oauth

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/session"
)

// This file holds the adapter of the port session.RedirectMethod for an
// OpenID Connect provider and for a plain OAuth 2.0 provider. The adapter
// checks the callback at its boundary, binds it to its pending request,
// calls the client, and translates each error of the client into an error
// of package session. It returns a session.Assertion and never makes a
// principal or a session.

// maxCallbackValue is the largest state, code or provider error of a
// callback that the method takes, in bytes. A real code, state and error
// code are much shorter.
const maxCallbackValue = 2048

// MethodClient is the client of one provider that a Method calls. *Lazy and
// *PlainClient meet it.
type MethodClient interface {
	// Begin returns the authorization URL and the pending request.
	Begin(ctx context.Context, opts Options) (string, Pending, error)
	// Finish exchanges the code and returns the person.
	Finish(ctx context.Context, code string, p Pending) (Identity, error)
}

// MethodConfig configures a Method.
type MethodConfig struct {
	// Client is the client of the provider. Required.
	Client MethodClient
	// Enabled reports whether the provider is on. The method calls it at
	// each Begin and Finish. It returns an error when the state of the
	// provider cannot be read. Required.
	Enabled func(ctx context.Context) (bool, error)
	// Now is the clock. Nil uses time.Now.
	Now func() time.Time
	// ProviderID names the sign-in provider. Required.
	ProviderID string
	// Kind is session.MethodOIDC or session.MethodOAuth.
	Kind session.MethodKind
}

// Method is the sign-in method of one OpenID Connect provider or one plain
// OAuth 2.0 provider. It implements session.RedirectMethod. It is safe for
// concurrent use.
type Method struct {
	client     MethodClient
	enabled    func(ctx context.Context) (bool, error)
	now        func() time.Time
	providerID string
	kind       session.MethodKind
}

var _ session.RedirectMethod = (*Method)(nil)

// NewMethod returns the method of cfg. It returns ErrConfig for a missing
// client or switch, a provider ID that is not valid, or a kind other than
// session.MethodOIDC and session.MethodOAuth.
func NewMethod(cfg MethodConfig) (*Method, error) {
	switch {
	case cfg.Client == nil:
		return nil, fmt.Errorf("%w: the client is missing", ErrConfig)
	case cfg.Enabled == nil:
		return nil, fmt.Errorf("%w: the switch of the provider is missing", ErrConfig)
	case !session.ValidProviderID(cfg.ProviderID):
		return nil, fmt.Errorf("%w: the provider ID is not valid", ErrConfig)
	case cfg.Kind != session.MethodOIDC && cfg.Kind != session.MethodOAuth:
		return nil, fmt.Errorf("%w: the kind %q is not %q or %q", ErrConfig, cfg.Kind, session.MethodOIDC,
			session.MethodOAuth)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	return &Method{
		client: cfg.Client, enabled: cfg.Enabled, now: now, providerID: cfg.ProviderID, kind: cfg.Kind,
	}, nil
}

// Kind implements session.RedirectMethod.
func (m *Method) Kind() session.MethodKind {
	return m.kind
}

// Begin implements session.RedirectMethod. An OIDC method asks for a fresh
// authentication with max_age=0. A plain OAuth 2.0 method has no such
// parameter: it keeps Fresh in the pending request and sends nothing.
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

	startedAt := m.now()
	fresh := in.Fresh && m.kind == session.MethodOIDC

	authURL, p, err := m.client.Begin(ctx, Options{State: in.State, MaxAge: 0, HasMaxAge: fresh})
	if err != nil {
		return none, fmt.Errorf("%w: %w", session.ErrMethodUnavailable, err)
	}

	// Defense in depth: the client builds the URL from the checked endpoints
	// of the provider. This check stays so that a client of another form
	// cannot send the browser to a URL that is not absolute and secure.
	if u, parseErr := url.Parse(authURL); parseErr != nil || u.Host == "" || !secureURL(u) {
		return none, fmt.Errorf("%w: the authorization URL is not an absolute https URL", session.ErrMethodAnswer)
	}

	return session.Redirect{URL: authURL, Pending: session.PendingRedirect{
		StartedAt: startedAt, ProviderID: m.providerID, State: p.State, Nonce: p.Nonce, Verifier: p.Verifier,
		RequestID: "", PrincipalID: "", SignInID: "", Fresh: in.Fresh,
	}}, nil
}

// Finish implements session.RedirectMethod. On any error, the Assertion is
// the zero value.
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

	if cb.Code == "" {
		return none, fmt.Errorf("%w: the callback has no code", session.ErrMethodInput)
	}

	if err := m.checkEnabled(ctx); err != nil {
		return none, err
	}

	// bind refused a nil pending request.
	pending := *cb.Pending
	fresh := pending.Fresh && m.kind == session.MethodOIDC

	id, err := m.client.Finish(ctx, cb.Code, Pending{
		State: pending.State, Nonce: pending.Nonce, Verifier: pending.Verifier, MaxAge: 0, HasMaxAge: fresh,
	})
	if err != nil {
		return none, methodError(err)
	}

	return m.assertion(id, pending)
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
// characters of each value, and no value of another protocol.
func checkCallback(cb session.Callback) error {
	for _, v := range []string{cb.State, cb.Code, cb.ProviderError} {
		if !validCallbackValue(v, 0) {
			return fmt.Errorf("%w: a callback value must have at most %d printable ASCII bytes",
				session.ErrMethodInput, maxCallbackValue)
		}
	}

	if cb.Ticket != "" || len(cb.SAMLResponse) > 0 {
		return fmt.Errorf("%w: the callback holds a value of another protocol", session.ErrMethodInput)
	}

	return nil
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

// assertion returns the Assertion of the person id. It refuses a subject or
// a name over the limits of package session, and a fresh authentication
// with no known time or with a time before the start of its request.
func (m *Method) assertion(id Identity, pending session.PendingRedirect) (session.Assertion, error) {
	var none session.Assertion

	if !validIdentityText(id.Subject, 1, session.MaxAssertionSubject) {
		return none, fmt.Errorf("%w: the subject must have 1 to %d bytes and no control character",
			session.ErrMethodAnswer, session.MaxAssertionSubject)
	}

	names, err := identityNames(id)
	if err != nil {
		return none, err
	}

	a := session.Assertion{
		AuthTime: time.Time{}, ProviderID: m.providerID, PrincipalID: "", Subject: id.Subject,
		UserName: id.PreferredUsername, Email: id.Email, Method: m.kind, Names: names, Groups: nil, Factors: nil,
		EmailVerified: false, GroupsPresent: false, AuthTimeKnown: false,
	}

	// Plain OAuth 2.0 gives no time of the authentication: the time of the
	// client is the time of Finish, and the method never reports it as the
	// time of the authentication.
	if m.kind == session.MethodOIDC && id.AuthTimeKnown {
		a.AuthTime, a.AuthTimeKnown = id.AuthTime, true
	}

	if m.kind == session.MethodOIDC && pending.Fresh &&
		(!a.AuthTimeKnown || a.AuthTime.Before(pending.StartedAt.Add(-clockSkew))) {
		// Defense in depth: the client refuses an authentication older than
		// the max_age of the request. This check stays so that a client that
		// does not check max_age cannot pass an old authentication as fresh.
		return none, fmt.Errorf("%w: the fresh authentication is older than its request", session.ErrMethodAnswer)
	}

	if a.AuthTimeKnown && a.AuthTime.After(m.now().Add(clockSkew)) {
		// Defense in depth: the client refuses an auth_time in the future.
		// This check stays so that a client of another form cannot skip it.
		return none, fmt.Errorf("%w: the authentication time is in the future", session.ErrMethodAnswer)
	}

	return a, nil
}

// identityNames returns the display name candidates of id that are not
// empty, in the order of preference: the name, the user name and the email.
// It refuses a value over the limit of package session or with a control
// character.
func identityNames(id Identity) ([]string, error) {
	candidates := []string{id.Name, id.PreferredUsername, id.Email}
	names := make([]string, 0, len(candidates))

	for _, v := range candidates {
		if !validIdentityText(v, 0, session.MaxAssertionName) {
			return nil, fmt.Errorf("%w: a name or an email must have at most %d bytes and no control character",
				session.ErrMethodAnswer, session.MaxAssertionName)
		}

		if v != "" {
			names = append(names, v)
		}
	}

	return names, nil
}

// validCallbackValue reports whether v has minLen to maxCallbackValue bytes
// and only printable ASCII: an OAuth state, code and error code have no
// other character.
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

// validIdentityText reports whether v has minLen to maxLen bytes and no
// control character.
func validIdentityText(v string, minLen, maxLen int) bool {
	return len(v) >= minLen && len(v) <= maxLen &&
		!strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || (r >= 0x7f && r <= 0x9f) })
}

// refusedError is an answer of the provider that refuses the sign-in: a
// refused code exchange or a user-info answer that is not 200. Its text is
// the text of err.
type refusedError struct {
	err error
}

func (e refusedError) Error() string { return e.err.Error() }

func (e refusedError) Unwrap() error { return e.err }

// unavailableError is a provider whose metadata the client cannot read.
// Its text is the text of err.
type unavailableError struct {
	err error
}

func (e unavailableError) Error() string { return e.err.Error() }

func (e unavailableError) Unwrap() error { return e.err }

// methodError translates an error of a client into an error of package
// session, and keeps err in the chain. An answer that fails a check comes
// first, so a redirect to http or an answer over the size limit is never
// reported as a provider that does not answer. An error of no known class
// is ErrMethodAnswer, so it fails closed.
func methodError(err error) error {
	var (
		refused     refusedError
		unavailable unavailableError
		urlErr      *url.Error
		netErr      net.Error
	)

	switch {
	case errors.Is(err, ErrIDToken), errors.Is(err, ErrUserInfo), errors.Is(err, errDowngrade),
		errors.Is(err, errTooManyRedirects), errors.Is(err, errResponseTooLarge):
		return fmt.Errorf("%w: %w", session.ErrMethodAnswer, err)
	case errors.As(err, &refused):
		return fmt.Errorf("%w: %w", session.ErrMethodRejected, err)
	case errors.As(err, &unavailable), errors.As(err, &urlErr), errors.As(err, &netErr),
		errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return fmt.Errorf("%w: %w", session.ErrMethodUnavailable, err)
	default:
		return fmt.Errorf("%w: %w", session.ErrMethodAnswer, err)
	}
}
