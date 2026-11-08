package server

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/clientip"
	"opendustpan.org/opendustpan/internal/devcode"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/oauth"
	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/internal/signinsvc"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/session"
)

// Names of the cookies and the header of the console sign-in. The prefix
// __Host- makes the browser refuse a cookie that is not Secure, that names
// a Domain, or whose Path is not "/".
const (
	// SessionCookie carries the secret of a console session.
	SessionCookie = sessionauth.Cookie
	// SignInCookie binds the browser to the state of one sign-in, from its
	// start to its callback.
	SignInCookie = "__Host-dustpan_signin"
	// CSRFHeader carries the CSRF value of the session on each request with
	// the session cookie whose method is not GET, HEAD or OPTIONS. Header
	// names ignore case: the docs write it X-Dustpan-CSRF.
	CSRFHeader = sessionauth.CSRFHeader
)

// Paths of the sign-in routes.
const (
	signInPath = "/api/server/sign-in/{provider}"
	// signInStartPrefix starts the path of the start of a sign-in, before
	// the provider ID.
	signInStartPrefix = "/api/server/sign-in/"
	callbackPath      = "/api/server/sign-in/{provider}/callback"
	sessionPath       = "/api/server/session"
	signOutPath       = "/api/server/sign-out"
)

// defaultReturnPath is where the browser goes after a sign-in that names
// no return path.
const defaultReturnPath = "/"

// Errors of the sign-in routes.
var (
	errUnknownProvider        = errors.New("unknown sign-in provider")
	errProviderDown           = errors.New("identity provider is not available: try again later")
	errReturnPath             = errors.New("return path is not a path of this server")
	errSignInFailed           = errors.New("sign-in failed: start the sign-in again")
	errRemovalAlertUnrecorded = errors.New("the removal alert and its audit entry could not be written")
	errSignInRefused          = errcode.Refusal(errcode.SignInRefused, "sign-in refused")
	errNoSession              = errors.New("the request has no console session")
	errCSRF                   = sessionauth.ErrCSRF
	errOtherSite              = sessionauth.ErrOtherSite
	errFreshParam             = errors.New(`the query parameter fresh must be "1" or absent`)
)

// SignInLimiter is the per-address limit of the sign-in routes. It has its
// own budget, apart from the token lookups. *authgate.Gate meets it.
type SignInLimiter interface {
	// Admit waits or refuses as a failed token lookup of the same address
	// does. It returns an error that wraps identity.ErrRateLimited.
	Admit(ctx context.Context) error
	// Fail counts one failure of the address of the request.
	Fail(ctx context.Context)
}

// SignInProvider is one redirect provider of the console sign-in: OpenID
// Connect or plain OAuth 2.0. Method is its sign-in method, for example an
// *oauth.Method. ID is part of the subject of each principal that signs in
// through it, so it must never change after the first sign-in. AutoCreate
// makes a principal at the first sign-in of a person; with false, an
// administrator makes each principal first.
type SignInProvider struct {
	Method      session.RedirectMethod
	ID          string
	DisplayName string
	AutoCreate  bool
	// Stored reports a provider that the API keeps. Its session is created
	// in the same write as a check that the provider is still enabled.
	Stored bool
}

// SignIn configures the console sign-in. Sessions is required. A nil
// Limiter sets no failure limit. Origins are the origins that may send a
// request with the session cookie: the origin of the public URL and each
// allowed origin of the console.
//
// Providers are the providers of the configuration of the server. Stored
// finds the providers that the API keeps, and Admin manages them; with a
// nil Admin, no provider route is mounted. A provider of the
// configuration wins over a stored provider with the same ID.
type SignIn struct {
	Sessions *session.Sessions
	// Service is the sign-in service. It is required: the callback and the
	// password sign-in start a session only through it.
	Service   *signinsvc.Service
	Limiter   SignInLimiter
	Stored    StoredProviders
	Admin     *session.ProviderAdmin
	Providers []SignInProvider
	Origins   []string
	// PasswordLimiter is the per-address budget of the routes that check a
	// password or a one-time code of a local user: the password sign-in, the
	// redeem of a code and the change of the own password. It is a budget of
	// its own, so these routes never spend the budget of the other sign-ins.
	// Nil mounts none of these routes.
	PasswordLimiter SignInLimiter
	// Passkeys is the service of the passkeys of the caller. A passkey needs
	// the console session of a person, so it belongs to the console sign-in.
	// Nil mounts no passkey route: the server keeps no passkeys, or its public
	// URL gives no relying party.
	Passkeys PasskeyService
	// PasskeySignIn is the part of the service of the passkeys that the
	// passkey sign-in uses. Nil mounts no passkey sign-in route.
	PasskeySignIn PasskeySignInService
	// PasskeyBeginLimiter is the per-address budget of the begins of a passkey
	// sign-in. Each begin writes a challenge row. Nil mounts no passkey
	// sign-in route.
	PasskeyBeginLimiter SignInLimiter
	// PasskeyLimiter is the per-address budget of the failed responses of a
	// passkey sign-in. Nil mounts no passkey sign-in route.
	PasskeyLimiter SignInLimiter
	// DevCode is the one-time code of opendustpan dev. With it, the handler
	// mounts the route that signs a browser in as the dev administrator.
	// Nil mounts no such route.
	DevCode *devcode.Code
	// DevAdmin is the bootstrap administrator of opendustpan dev. The dev
	// sign-in grants the role of the site administrator as DevAdmin, and
	// refuses to sign in when the handler has no access service.
	DevAdmin identity.Principal
	// OnDevRefused is called with the subject of each dev person whose session
	// a server of another kind refuses. Nil means no call.
	OnDevRefused func(ctx context.Context, subject string)
}

// StoredProviders finds the sign-in providers that the API keeps.
type StoredProviders interface {
	// Provider returns the enabled stored provider id. It returns false
	// for an ID with no enabled stored provider, and an error when the
	// store or the client secret fails.
	Provider(ctx context.Context, id string) (SignInProvider, bool, error)
}

// signInState is the sign-in of a handler.
type signInState struct {
	service   *signinsvc.Service
	sessions  *session.Sessions
	limiter   SignInLimiter
	passwords SignInLimiter
	passkeys  PasskeyService
	// passkeySignIn, passkeyBegins and passkeyFailures are the passkey
	// sign-in and its two budgets.
	passkeySignIn   PasskeySignInService
	passkeyBegins   SignInLimiter
	passkeyFailures SignInLimiter
	stored          StoredProviders
	admin           *session.ProviderAdmin
	providers       map[string]SignInProvider
	checker         *sessionauth.Checker
	devCode         *devcode.Code
	devAdmin        identity.Principal
}

// provider returns the sign-in provider id: a provider of the
// configuration, else an enabled stored provider. It returns false for an
// unknown ID.
func (s *signInState) provider(ctx context.Context, id string) (SignInProvider, bool, error) {
	if p, ok := s.providers[id]; ok {
		return p, true, nil
	}

	if s.stored == nil {
		return SignInProvider{
			Method:      nil,
			ID:          "",
			DisplayName: "",
			AutoCreate:  false,
			Stored:      false,
		}, false, nil
	}

	p, ok, err := s.stored.Provider(ctx, id)
	if err != nil {
		return p, false, fmt.Errorf("find sign-in provider %q: %w", id, err)
	}

	return p, ok, nil
}

// newSignInState returns the sign-in of cfg, or nil when cfg is nil.
func newSignInState(cfg *SignIn, kind installation.Kind) (*signInState, error) {
	if cfg == nil {
		return nil, nil //nolint:nilnil // No sign-in is a valid configuration.
	}

	if cfg.Sessions == nil {
		return nil, fmt.Errorf("%w: sign-in sessions", ErrMissingDependency)
	}

	if cfg.Service == nil {
		return nil, fmt.Errorf("%w: sign-in service", ErrMissingDependency)
	}

	providers := make(map[string]SignInProvider, len(cfg.Providers))
	for _, p := range cfg.Providers {
		if p.Method == nil {
			return nil, fmt.Errorf("%w: method of sign-in provider %q", ErrMissingDependency, p.ID)
		}

		providers[p.ID] = p
	}

	var opts []sessionauth.Option
	if kind != installation.KindDev {
		opts = append(opts, sessionauth.RefuseDevSubjects(), sessionauth.OnDevRefused(cfg.OnDevRefused))
	}

	checker, err := sessionauth.New(cfg.Sessions, cfg.Origins, opts...)
	if err != nil {
		return nil, fmt.Errorf("session cookie check: %w", err)
	}

	return &signInState{
		service:       cfg.Service,
		sessions:      cfg.Sessions,
		limiter:       cfg.Limiter,
		passwords:     cfg.PasswordLimiter,
		passkeys:      cfg.Passkeys,
		passkeySignIn: cfg.PasskeySignIn, passkeyBegins: cfg.PasskeyBeginLimiter,
		passkeyFailures: cfg.PasskeyLimiter,
		stored:          cfg.Stored,
		admin:           cfg.Admin,
		providers:       providers,
		checker:         checker,
		devCode:         cfg.DevCode,
		devAdmin:        cfg.DevAdmin,
	}, nil
}

// signInRoutes returns the sign-in rows of the route table. No sign-in
// mounts no route.
func (h *Handler) signInRoutes() []Route {
	if h.signIn == nil {
		return nil
	}

	self := []identity.Action{identity.ActionCallerRead}

	return []Route{
		{
			Method: http.MethodGet, Path: signInPath, Scope: ScopePublic, Actions: nil, Resolve: nil,
			ActionsFor: nil, OpaqueDenial: false, Serve: h.startSignIn,
		},
		{
			Method: http.MethodGet, Path: callbackPath, Scope: ScopePublic, Actions: nil, Resolve: nil,
			ActionsFor: nil, OpaqueDenial: false, Serve: h.finishSignIn,
		},
		{
			Method: http.MethodGet, Path: sessionPath, Scope: ScopeAuthorized, Actions: self, Resolve: resolveCaller,
			ActionsFor: nil, OpaqueDenial: false, Serve: h.readSession,
		},
		{
			Method: http.MethodPost, Path: signOutPath, Scope: ScopeAuthorized, Actions: self, Resolve: resolveCaller,
			ActionsFor: nil, OpaqueDenial: false, Serve: h.signOut,
		},
	}
}

// admitSignIn waits or refuses the request as the failure limit says.
func (h *Handler) admitSignIn(ctx context.Context) error {
	if h.signIn.limiter == nil {
		return nil
	}

	return h.signIn.limiter.Admit(ctx)
}

// failSignIn spends one unit of the sign-in budget of the address of the
// request. Each start of a sign-in and each failed callback spends one, so
// the address of a request bounds the sign-in requests that it can store.
func (h *Handler) failSignIn(ctx context.Context) {
	if h.signIn.limiter != nil {
		h.signIn.limiter.Fail(ctx)
	}
}

// startSignIn stores a new sign-in request, binds the browser to it with
// the sign-in cookie, and sends the browser to the identity provider. The
// query parameter return names where the browser goes after the sign-in:
// a path of this server. Each start spends one unit of the sign-in budget
// of its address, whatever its result. The budget is not the one of the
// token lookups, so sign-ins never slow them.
func (h *Handler) startSignIn(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	if err := h.admitSignIn(ctx); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.failSignIn(ctx)

	provider, ok, err := h.signIn.provider(ctx, r.PathValue("provider"))
	if err != nil || !ok {
		h.writeError(ctx, w, cmp.Or(err, errUnknownProvider))

		return
	}

	fresh, err := freshParam(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	returnPath := queryOf(r).Get("return")
	if returnPath == "" {
		returnPath = defaultReturnPath
	}

	if !session.ValidReturnPath(returnPath) {
		h.writeError(ctx, w, errReturnPath)

		return
	}

	state, err := session.NewSecret()
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	redirect, err := provider.Method.Begin(
		ctx,
		session.BeginInput{ProviderID: provider.ID, State: state.Reveal(), Fresh: fresh},
	)
	if err != nil {
		h.logger.WarnContext(ctx, "identity provider not available", slog.String("provider", provider.ID),
			slog.String("error", err.Error()))
		h.writeError(ctx, w, errProviderDown)

		return
	}

	// The stored request has a max_age only when the start sent one: a
	// fresh start of an OIDC provider sends max_age=0. A plain OAuth 2.0
	// provider has no such parameter.
	pending := redirect.Pending

	err = h.signIn.sessions.BeginRequest(ctx, session.Begin{
		State: state, Provider: provider.ID, Nonce: pending.Nonce, Verifier: pending.Verifier, ReturnPath: returnPath,
		MaxAge: 0, HasMaxAge: pending.Fresh && provider.Method.Kind() == session.MethodOIDC,
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	noStore(w)
	http.SetCookie(w, browserCookie(SignInCookie, state.Reveal(), session.RequestLifetime, http.SameSiteLaxMode))
	http.Redirect(w, r, redirect.URL, http.StatusFound)
}

// finishSignIn is the callback of a sign-in: it parses the callback, calls
// the method of the provider, gives the result to the sign-in service, and
// answers. The service does the steps of a sign-in and writes the audit
// entry of each refusal.
func (h *Handler) finishSignIn(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	noStore(w)
	http.SetCookie(w, browserCookie(SignInCookie, "", -1, http.SameSiteLaxMode))

	if err := h.admitSignIn(ctx); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	provider, ok, err := h.signIn.provider(ctx, r.PathValue("provider"))
	if err != nil || !ok {
		h.failSignIn(ctx)
		h.writeError(ctx, w, cmp.Or(err, errUnknownProvider))

		return
	}

	cb := h.parseCallback(r, provider)

	var assertion session.Assertion

	methodErr := cb.err
	if methodErr == nil {
		assertion, methodErr = provider.Method.Finish(ctx, cb.callback)
	}

	res, err := h.signIn.service.Complete(ctx, &signinsvc.Request{
		MethodErr: methodErr, Entry: signInEntry(r, "", session.ActionSignIn),
		Provider: signinsvc.Provider{
			ID: provider.ID, Kind: provider.Method.Kind(), AutoCreate: provider.AutoCreate, Stored: provider.Stored,
		},
		Assertion: assertion,
	})
	if err != nil {
		h.answerSignInError(ctx, w, err)

		return
	}

	http.SetCookie(w, sessionCookie(res.Secret.Reveal(), h.sessionLength(ctx, res.Session)))
	// The return path passed session.ValidReturnPath when the request was
	// stored: it is a path of this server.
	http.Redirect(w, r, cb.returnPath, http.StatusSeeOther)
}

// answerSignInError counts the failure of the address and answers the error
// of the sign-in service. A refusal has its audit entry already.
func (h *Handler) answerSignInError(ctx context.Context, w http.ResponseWriter, err error) {
	if errors.Is(err, session.ErrProviderDisabled) || errors.Is(err, session.ErrProviderNotFound) {
		// The provider was disabled or deleted after the lookup of this
		// callback: answer as for a start of a disabled provider.
		h.failSignIn(ctx)
		h.writeError(ctx, w, errUnknownProvider)

		return
	}

	refusal, ok := errors.AsType[*signinsvc.RefusedError](err)
	if !ok {
		h.writeError(ctx, w, err)

		return
	}

	h.failSignIn(ctx)

	if refusal.Answer == signinsvc.AnswerRefused {
		// The reason is safe to show: it names no code, token or state.
		h.writeError(ctx, w, signInRefusalError{reason: refusal.Reason})

		return
	}

	h.writeError(ctx, w, errSignInFailed)
}

// parsedCallback is what the parse of a callback gives: the input of the
// method, the return path of the sign-in request, and the error of the parse.
type parsedCallback struct {
	err        error
	callback   session.Callback
	returnPath string
}

// parseCallback copies the fields of the callback r of provider into a
// session.Callback. It checks the state against the sign-in cookie and takes
// the sign-in request once. A callback that does not belong to a request
// gives an error of the class session.ErrRequestBinding, which the sign-in
// service records. The method is not called for it.
func (h *Handler) parseCallback(r *http.Request, provider SignInProvider) parsedCallback {
	ctx := r.Context()
	q := queryOf(r)

	binding := func(why string) parsedCallback {
		return parsedCallback{
			err: fmt.Errorf("%w: %s", session.ErrRequestBinding, why),
			callback: session.Callback{
				Pending: nil, State: "", Code: "", Ticket: "", ProviderError: "", SAMLResponse: nil,
			},
			returnPath: "",
		}
	}

	//nolint:forbidigo // An identity provider calls the sign-in callback, which is outside the description.
	cookie, err := r.Cookie(SignInCookie)
	if err != nil {
		return binding("the browser holds no sign-in request")
	}

	if oauth.CheckState(q.Get("state"), oauth.Pending{
		State: cookie.Value, Nonce: "", Verifier: "", MaxAge: 0, HasMaxAge: false,
	}) != nil {
		return binding("the state does not match")
	}

	state, err := session.ParseSecret(cookie.Value)
	if err != nil {
		return binding("the sign-in cookie is not a state")
	}

	// The request works once: TakeRequest deletes it before the exchange.
	req, err := h.signIn.sessions.TakeRequest(ctx, state)
	if err != nil || req.Provider != provider.ID {
		return binding("the sign-in request is unknown or expired")
	}

	return parsedCallback{
		err: nil, returnPath: req.ReturnPath,
		callback: session.Callback{
			Pending: &session.PendingRedirect{
				StartedAt: req.CreatedAt, ProviderID: req.Provider, State: cookie.Value, Nonce: req.Nonce,
				Verifier: req.Verifier, RequestID: "", PrincipalID: "", SignInID: "", Fresh: req.HasMaxAge,
			},
			State: q.Get("state"), Code: q.Get("code"), Ticket: "", ProviderError: q.Get("error"), SAMLResponse: nil,
		},
	}
}

// sessionLength returns how long the session cookie of sess lives: until
// the maximum length of the current policy ends.
func (h *Handler) sessionLength(ctx context.Context, sess session.Session) time.Duration {
	limits := session.DefaultLimits()

	if h.sessions != nil {
		if policy, err := h.sessions.Policy(ctx); err == nil {
			limits = policy.Limits
		}
	}

	return time.Until(sess.CreatedAt.Add(limits.MaxLength))
}

// signInRefusalError is errSignInRefused with the reason of the refusal. The reason
// is safe to show.
type signInRefusalError struct{ reason string }

func (e signInRefusalError) Error() string { return errSignInRefused.Error() + ": " + e.reason }

// Unwrap returns errSignInRefused.
func (signInRefusalError) Unwrap() error { return errSignInRefused }

// signInEntry is the audit entry of a sign-in or a sign-out from the
// console.
func signInEntry(r *http.Request, subject, action string) audit.Entry {
	return audit.Entry{
		At: time.Time{}, Namespace: "", Subject: subject, IDPSubject: "", TokenID: "", Action: action,
		Object: "", Version: "", ValuePath: "", ValueCount: nil, Changes: nil, SourceIP: clientip.String(r),
		UserAgent: audit.TruncateUserAgent(userAgent(r)), Channel: audit.ChannelConsole, Kind: "", Outcome: "",
		RequestID: requestid.FromContext(r.Context()), ObjectID: "", PrevHash: "", Hash: "", Sequence: 0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}
}

// sessionResponse is the console session of the caller on the wire.
// CSRFToken is the value of the X-Dustpan-CSRF header.
//
// Contract: never remove or rename these fields.
type sessionResponse struct {
	Subject     string `json:"subject"`
	DisplayName string `json:"displayName"`
	Provider    string `json:"provider"`
	CreatedAt   string `json:"createdAt"`
	AuthTime    string `json:"authTime"`
	ExpiresAt   string `json:"expiresAt"`
	CSRFToken   string `json:"csrfToken"`
}

// readSession answers the console session of the caller and its CSRF
// value. A caller with an access token has no session: 404.
func (h *Handler) readSession(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	caller, ok := sessionFromRequest(r)
	if !ok {
		h.writeError(ctx, w, errNoSession)

		return
	}

	limits := session.DefaultLimits()

	if h.sessions != nil {
		policy, err := h.sessions.Policy(ctx)
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		limits = policy.Limits
	}

	s := caller.Match.Session

	noStore(w)
	h.writeJSON(ctx, w, http.StatusOK, sessionResponse{
		Subject:     caller.Match.Owner.Subject,
		DisplayName: caller.Match.Owner.DisplayName,
		Provider:    s.Provider,
		CreatedAt:   s.CreatedAt.UTC().Format(time.RFC3339Nano),
		AuthTime:    s.AuthTime.UTC().Format(time.RFC3339Nano),
		ExpiresAt:   s.CreatedAt.Add(limits.MaxLength).UTC().Format(time.RFC3339Nano),
		CSRFToken:   session.CSRFValue(caller.Secret),
	})
}

// signOut ends the console session of the caller and clears its cookie. A
// caller with an access token has no session: 404.
func (h *Handler) signOut(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	caller, ok := sessionFromRequest(r)
	if !ok {
		h.writeError(ctx, w, errNoSession)

		return
	}

	e := signInEntry(r, p.Subject, session.ActionSignOut)
	if err := h.signIn.sessions.SignOut(ctx, e, caller.Match.Owner.ID, caller.Match.Session.ID); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	http.SetCookie(w, sessionCookie("", -1))
	w.WriteHeader(http.StatusNoContent)
}

// sessionCaller is the console session that authenticated a request.
type sessionCaller = sessionauth.Caller

// sessionKey carries the sessionCaller of a request.
type sessionKey struct{}

// sessionFromRequest returns the console session that authenticated r,
// and false for a request with an access token.
func sessionFromRequest(r *http.Request) (sessionCaller, bool) {
	c, ok := r.Context().Value(sessionKey{}).(sessionCaller)

	return c, ok
}

// authenticateSession resolves the session cookie of r with the cookie
// check that every API of the console shares.
func (h *Handler) authenticateSession(r *http.Request) (identity.Principal, *sessionCaller, error) {
	if h.signIn == nil {
		return identity.Principal{}, nil, identity.ErrUnauthenticated
	}

	p, caller, err := h.signIn.checker.Authenticate(r)
	if err != nil {
		return identity.Principal{}, nil, err
	}

	return p, &caller, nil
}

// browserCookie returns a cookie of the console: Secure, HttpOnly, the
// SameSite mode that the caller gives, and Path=/, as the prefix __Host-
// needs. A negative maxAge deletes the cookie.
func browserCookie(name, value string, maxAge time.Duration, sameSite http.SameSite) *http.Cookie {
	seconds := int(maxAge / time.Second)
	if maxAge < 0 {
		seconds = -1
	}

	// Each mode is a constant in its own cookie, so the cookie never gets a
	// mode that the code does not name, and the mode is set once.
	if sameSite == http.SameSiteStrictMode {
		return &http.Cookie{
			Name: name, Value: value, Path: "/", MaxAge: seconds, Secure: true, HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
		}
	}

	return &http.Cookie{
		Name: name, Value: value, Path: "/", MaxAge: seconds, Secure: true, HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
}

// sessionCookie returns the session cookie of the console. It is SameSite=Strict,
// so the browser never sends it with a request that a page of another site
// starts. The sign-in cookie stays Lax: the return from the identity provider
// is a cross-site navigation that must carry it.
func sessionCookie(value string, maxAge time.Duration) *http.Cookie {
	return browserCookie(SessionCookie, value, maxAge, http.SameSiteStrictMode)
}

// noStore keeps a browser and a proxy from storing the answer.
func noStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}

// classifySignIn maps the errors of the sign-in routes.
func classifySignIn(err error) (failure, bool) {
	switch {
	case errors.Is(err, errUnknownProvider):
		return fail(http.StatusNotFound, errUnknownProvider.Error()), true
	case errors.Is(err, errProviderDown):
		return fail(http.StatusBadGateway, errProviderDown.Error()), true
	case errors.Is(err, errReturnPath):
		return fail(http.StatusBadRequest, errReturnPath.Error()), true
	case errors.Is(err, errSignInFailed):
		return fail(http.StatusBadRequest, errSignInFailed.Error()), true
	case errors.Is(err, signinsvc.ErrRemovalAlertUnrecorded):
		return failCoded(
			http.StatusServiceUnavailable, errRemovalAlertUnrecorded.Error(), errcode.PrincipalRemovalAlertUnrecorded,
		), true
	case errors.Is(err, errNoSession), errors.Is(err, session.ErrEnded):
		return fail(http.StatusNotFound, errNoSession.Error()), true
	case errors.Is(err, errDevCode), errors.Is(err, errDevKind):
		return fail(http.StatusForbidden, err.Error()), true
	case errors.Is(err, errSignInRefused), errors.Is(err, errCSRF), errors.Is(err, errOtherSite):
		return fail(http.StatusForbidden, err.Error()), true
	case errors.Is(err, errFreshParam):
		return fail(http.StatusBadRequest, err.Error()), true
	case errors.Is(err, errReauthenticate):
		return fail(http.StatusUnauthorized, err.Error()), true
	default:
		return fail(0, ""), false
	}
}
