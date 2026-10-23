// Package routekit holds the request steps that every tool-neutral API runs
// before a handler: the authentication of the credential, the authorization
// of each declared action at the point of use, the strict JSON body reader,
// and the ServeMux that records each pattern for the route test. Each API
// keeps its route table, its resolvers and the order of its own checks.
package routekit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/api/problem"
	"opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/identity"
)

// The Authorization schemes that a tool-neutral API reads. Both carry an
// access token.
const (
	tokenScheme  = "token "
	bearerScheme = "Bearer "
)

// Guard runs the authentication and the authorization of a request. Tokens
// and Refusals must be set. A nil Sessions refuses every request without an
// Authorization header.
type Guard struct {
	Tokens   identity.Authenticator
	Sessions *sessionauth.Checker
	Refusals *refusal.Recorder
}

// Authenticate resolves the credential of r to a principal. It reads the
// "Authorization: token <t>" and the "Authorization: Bearer <t>" header. A
// request with no Authorization header uses the console session cookie, and
// a request with the header never uses the cookie. The caller is the session
// that signed the request in, or nil for an access token: pass it to
// WithCaller. Any other scheme, and an empty token, answer
// identity.ErrUnauthenticated.
func (g Guard) Authenticate(r *http.Request) (identity.Principal, *sessionauth.Caller, error) {
	header := apidesc.Header(r, "Authorization")
	if header == "" {
		p, caller, err := g.Sessions.Authenticate(r)
		if err != nil {
			return identity.Principal{}, nil, err
		}

		return p, &caller, nil
	}

	raw, ok := strings.CutPrefix(header, tokenScheme)
	if !ok {
		raw, ok = strings.CutPrefix(header, bearerScheme)
	}

	if !ok || raw == "" {
		return identity.Principal{}, nil, identity.ErrUnauthenticated
	}

	p, err := g.Tokens.AuthenticateToken(r.Context(), raw)
	if err != nil {
		return identity.Principal{}, nil, err
	}

	return p, nil, nil
}

// WithCaller returns ctx for the request of caller. For a session, it carries
// sessionauth.WithSession and the sign-in of the session, so the checks of a
// fresh sign-in and the audit channel see it. For an access token (a nil
// caller), it returns ctx.
func WithCaller(ctx context.Context, caller *sessionauth.Caller) context.Context {
	if caller == nil {
		return ctx
	}

	ctx = sessionauth.WithSession(ctx)

	return sessionauth.WithSignIn(ctx, sessionauth.SignIn{
		At: caller.Match.Session.AuthTime, Provider: caller.Match.Session.Provider,
	})
}

// AuthorizeAll authorizes each action on obj for p, in order, at the point of
// use, and returns the first refusal. The recorder writes the refusal to the
// audit log. A route with no action passes; the route test refuses such a
// route.
func (g Guard) AuthorizeAll(
	r *http.Request,
	p identity.Principal,
	obj identity.Object,
	actions []identity.Action,
) error {
	for _, action := range actions {
		if err := g.Refusals.Authorize(r.Context(), r, p, action, obj); err != nil {
			return fmt.Errorf("authorize %s: %w", action, err)
		}
	}

	return nil
}

// Mutating reports whether method changes data.
func Mutating(method string) bool {
	return method != http.MethodGet && method != http.MethodHead
}

// DecodeBody decodes one JSON object of at most limit bytes into v. It
// refuses an unknown field, a second value and a body over the limit. The
// error text is fixed: it never holds a part of the body.
func DecodeBody(w http.ResponseWriter, r *http.Request, limit int64, v any) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if err != nil {
		return problem.BadRequest("request.body-invalid",
			"the request body is not readable or is over "+sizeText(limit))
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	if err = dec.Decode(v); err != nil || dec.More() {
		return problem.BadRequest("request.body-invalid", "the request body must be one JSON object with known fields")
	}

	return nil
}

// The binary units of a size text.
const (
	kib = 1 << 10
	mib = 1 << 20
)

// sizeText names n bytes in the largest binary unit that divides it.
func sizeText(n int64) string {
	switch {
	case n >= mib && n%mib == 0:
		return fmt.Sprintf("%d MiB", n/mib)
	case n >= kib && n%kib == 0:
		return fmt.Sprintf("%d KiB", n/kib)
	default:
		return fmt.Sprintf("%d bytes", n)
	}
}

// Mux is an http.ServeMux that records every pattern registered on it, so the
// route test can list every served route.
type Mux struct {
	servemux *http.ServeMux
	patterns []string
}

// NewMux returns an empty Mux.
func NewMux() *Mux {
	return &Mux{servemux: http.NewServeMux(), patterns: nil}
}

// Handle registers pattern and records it.
func (m *Mux) Handle(pattern string, handler http.Handler) {
	m.patterns = append(m.patterns, pattern)
	m.servemux.Handle(pattern, handler)
}

// HandleFunc registers pattern and records it.
func (m *Mux) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	m.Handle(pattern, http.HandlerFunc(handler))
}

// ServeHTTP implements http.Handler.
func (m *Mux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	m.servemux.ServeHTTP(w, r)
}

// Patterns returns a copy of the recorded patterns, in registration order.
func (m *Mux) Patterns() []string {
	return append([]string(nil), m.patterns...)
}
