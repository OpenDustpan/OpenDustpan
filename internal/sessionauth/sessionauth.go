// Package sessionauth checks the console session cookie of a request. Each
// HTTP adapter that the console calls uses one Checker, so the cookie, the
// origin check and the CSRF check are the same on every API.
//
// A request with the cookie must come from the site of the console: its
// Origin header, when present, must be an allowed origin, and without one
// the browser must not mark it as cross-site. A request with a method that
// can change state must also send the CSRF value of the session in the
// X-Dustpan-CSRF header. Both checks run before the store read.
package sessionauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

const (
	// Cookie is the name of the session cookie. The prefix __Host- makes
	// the browser refuse a cookie that is not Secure, that has a Domain, or
	// whose Path is not "/".
	Cookie = "__Host-dustpan_session"
	// CSRFHeader is the header that carries the CSRF value of the session.
	CSRFHeader = "X-Dustpan-Csrf"
)

// Errors of the cookie check. Each wraps identity.ErrDenied.
var (
	ErrOtherSite = fmt.Errorf("%w: a request with the session cookie comes from another site", identity.ErrDenied)
	ErrCSRF      = fmt.Errorf("%w: the X-Dustpan-CSRF header is missing or wrong", identity.ErrDenied)
)

// errNoSessions reports a Checker with no session service.
var errNoSessions = errors.New("sessionauth: no session service")

// Sessions checks the secret of a session. *session.Sessions meets it.
// Authenticate returns session.ErrUnauthenticated for an unknown, ended,
// idle or expired session, or a session of a disabled owner.
type Sessions interface {
	Authenticate(ctx context.Context, secret session.Secret) (session.Match, error)
}

// Checker checks the session cookie of a request.
type Checker struct {
	sessions Sessions
	origins  []string
	refuse   bool
	// onRefused is called with the subject of each dev person that refuse
	// turns away. Nil means no call.
	onRefused func(ctx context.Context, subject string)
}

// Option changes a Checker.
type Option func(*Checker)

// RefuseDevSubjects makes the Checker refuse the session of a person whose
// subject starts with principal.DevSubjectPrefix. A server that is not of
// the kind dev sets it, so a session that a restored dev database holds
// authenticates nothing.
func RefuseDevSubjects() Option {
	return func(c *Checker) { c.refuse = true }
}

// OnDevRefused makes the Checker call f with the subject of each dev person
// whose session it refuses (see RefuseDevSubjects). f returns within a bounded
// time, and it must not put the cookie or any request data in a record.
func OnDevRefused(f func(ctx context.Context, subject string)) Option {
	return func(c *Checker) { c.onRefused = f }
}

// New returns a Checker that checks sessions with sessions and lets a
// request with the cookie come from origins.
func New(sessions Sessions, origins []string, opts ...Option) (*Checker, error) {
	if sessions == nil {
		return nil, errNoSessions
	}

	c := &Checker{sessions: sessions, origins: slices.Clone(origins), refuse: false, onRefused: nil}
	for _, opt := range opts {
		opt(c)
	}

	return c, nil
}

// Caller is the console session that authenticated a request, and its
// secret, from which the CSRF value comes.
type Caller struct {
	Match  session.Match
	Secret session.Secret
}

// Authenticate resolves the session cookie of r to its owner. A nil Checker,
// a request with no cookie or a cookie of the wrong form, and a session
// that Sessions refuses return identity.ErrUnauthenticated. A request from
// another site returns ErrOtherSite, and a missing or wrong CSRF value on a
// method that can change state returns ErrCSRF. The returned principal is
// not an administrator and has no token ID.
func (c *Checker) Authenticate(r *http.Request) (identity.Principal, Caller, error) {
	var none Caller

	if c == nil {
		return identity.Principal{}, none, identity.ErrUnauthenticated
	}

	cookie, err := r.Cookie(Cookie)
	if err != nil {
		return identity.Principal{}, none, identity.ErrUnauthenticated
	}

	secret, err := session.ParseSecret(cookie.Value)
	if err != nil {
		return identity.Principal{}, none, identity.ErrUnauthenticated
	}

	if !c.sameSite(r) {
		return identity.Principal{}, none, ErrOtherSite
	}

	if changesState(r.Method) && !session.CheckCSRF(secret, r.Header.Get(CSRFHeader)) {
		return identity.Principal{}, none, ErrCSRF
	}

	m, err := c.sessions.Authenticate(r.Context(), secret)
	if errors.Is(err, session.ErrUnauthenticated) {
		return identity.Principal{}, none, fmt.Errorf("%w: %w", identity.ErrUnauthenticated, err)
	}

	if err != nil {
		return identity.Principal{}, none, fmt.Errorf("check session: %w", err)
	}

	if c.refuse && principal.IsDevSubject(m.Owner.Subject) {
		if c.onRefused != nil {
			c.onRefused(r.Context(), m.Owner.Subject)
		}

		return identity.Principal{}, none, fmt.Errorf("%w: a session of a dev person is not valid on this installation",
			identity.ErrUnauthenticated)
	}

	p := identity.Principal{
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     time.Time{},
		Subject:                m.Owner.Subject, DisplayName: m.Owner.DisplayName, Admin: false, TokenID: "",
		TokenExpiresAt: time.Time{},
		Kind:           identity.KindPerson,
		Credential:     identity.CredentialSession,
		JobID:          "",
	}

	return p, Caller{Match: m, Secret: secret}, nil
}

// sameSite reports whether a request with the session cookie may come from
// where it comes from: its Origin header, when present, must be an allowed
// origin; without one, the browser must not mark it as cross-site.
func (c *Checker) sameSite(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		return slices.Contains(c.origins, origin)
	}

	return r.Header.Get("Sec-Fetch-Site") != "cross-site"
}

// changesState reports whether a request with method can change state:
// every method other than GET, HEAD and OPTIONS.
func changesState(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// sessionKey marks the context of a request that the session cookie
// authenticated.
type sessionKey struct{}

// WithSession returns ctx marked as the context of a request that the
// session cookie authenticated.
func WithSession(ctx context.Context) context.Context {
	return context.WithValue(ctx, sessionKey{}, true)
}

// FromSession reports whether WithSession marked ctx.
func FromSession(ctx context.Context) bool {
	marked, ok := ctx.Value(sessionKey{}).(bool)

	return ok && marked
}

// AuditChannel returns the audit channel of r: ChannelConsole for a request
// that the session cookie signed in, else the channel that the client
// declares in the header named header.
func AuditChannel(r *http.Request, header string) audit.Channel {
	if FromSession(r.Context()) {
		return audit.ChannelConsole
	}

	return audit.NormalizeChannel(r.Header.Get(header))
}
