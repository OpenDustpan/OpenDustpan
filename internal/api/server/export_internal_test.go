package server

import (
	"context"
	"log/slog"
	"net/http"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/internal/localusersvc"
	"opendustpan.org/opendustpan/pkg/identity"
)

// WithPrincipalForTest returns ctx carrying p, as enforce stores the caller
// before it runs a resolver, so a test can call a resolver directly.
func WithPrincipalForTest(ctx context.Context, p identity.Principal) context.Context {
	return withPrincipal(ctx, p)
}

// ServeAccessLimitSaveForTest calls the save of an access limit through the
// route of h, with p as the caller and a console session whose sign-in is
// now. The route table, the authentication and the policy check are not in
// the way, so a test reaches the freshness check and the group reach check
// with the caller that it names.
func ServeAccessLimitSaveForTest(
	h *Handler, w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	var caller sessionCaller

	caller.Match.Session.AuthTime = h.signIn.sessions.Now()
	caller.Match.Session.Provider = "corp"

	ctx := context.WithValue(r.Context(), sessionKey{}, caller)
	h.fresh(identity.ActionNamespaceManageGates, h.writeAccessLimit)(w, r.WithContext(ctx), p, obj)
}

// ErrInvalidBodyForTest is the error of a request body that is not valid JSON.
var ErrInvalidBodyForTest = errInvalidBody

// ErrOverrideSwitchesBodyForTest is the error of a switch change without the
// body that it needs.
var ErrOverrideSwitchesBodyForTest = errOverrideSwitchesBody

// ErrInvalidNamespaceBodyForTest is the error of a namespace create body that
// is not valid JSON.
var ErrInvalidNamespaceBodyForTest = errInvalidNamespaceBody

// ErrNamespaceCursorForTest is the error of a cursor that does not decode to a
// namespace name.
var ErrNamespaceCursorForTest = errNamespaceCursor

// ClassifyForTest returns the status and the message that the error
// responses of the handlers give for err.
func ClassifyForTest(err error) (int, string) {
	f := classify(err)

	return f.status, f.message
}

// TokenCodeForTest returns the code of the catalog that the token classifier
// gives for err, and false when the classifier maps no error.
func TokenCodeForTest(err error) (string, bool) {
	f, ok := classifyToken(err)
	if !ok {
		return "", false
	}

	return f.code, true
}

// WriteErrorForTest writes the error answer of err, as a handler of the
// installation API does.
func WriteErrorForTest(w http.ResponseWriter, r *http.Request, err error) {
	var h Handler

	h.logger = slog.New(slog.DiscardHandler)
	h.writeError(r.Context(), w, err)
}

// WriteEnterErrorForTest writes the answer of a failed enter of maintenance
// mode with err, as the enter handler does.
func WriteEnterErrorForTest(w http.ResponseWriter, r *http.Request, err error) {
	var h Handler

	h.logger = slog.New(slog.DiscardHandler)
	h.writeEnterError(r.Context(), w, err)
}

// WriteErrorLogForTest writes the error answer of err with logger, so a test
// can read the log line of the answer.
func WriteErrorLogForTest(w http.ResponseWriter, r *http.Request, err error, logger *slog.Logger) {
	var h Handler

	h.logger = logger
	h.writeError(r.Context(), w, err)
}

// ServeEmptyForTest serves r with a handler that has no route, so every path
// answers the 404 of an unknown route.
func ServeEmptyForTest(w http.ResponseWriter, r *http.Request) {
	var h Handler

	h.logger = slog.New(slog.DiscardHandler)
	h.mux = newPatternMux()
	h.ServeHTTP(w, r)
}

// ServeRouteForTest serves r with route through the registration wrapper of h,
// so a test can exercise the wrapper on a route that it builds.
func (h *Handler) ServeRouteForTest(w http.ResponseWriter, r *http.Request, route Route) {
	h.enforce(route)(w, r)
}

// DevCodeSignInForTest calls the dev sign-in handler of h directly, as the
// route table would, so a test sees what the handler alone does when its
// route is not mounted.
func DevCodeSignInForTest(h *Handler, w http.ResponseWriter, r *http.Request) {
	//nolint:exhaustruct_v5 // The handler ignores the caller and the object.
	h.devCodeSignIn(w, r, identity.Principal{}, identity.Object{})
}

// CodedErrorForTest returns a coded error with status, code, message and
// values, so a test can give the answer a code or arguments that the catalog
// refuses.
func CodedErrorForTest(status int, code, message string, values ...errcode.Value) error {
	return &codedError{status: status, code: code, message: message, args: values}
}

// SignInRefusalForTest returns the refusal of a sign-in with reason, as the
// sign-in handler builds it.
func SignInRefusalForTest(reason string) error { return signInRefusalError{reason: reason} }

// The body, query and store errors of the token and principal routes, for the
// test of their answers.
var (
	ErrTokenBodyForTest              = errTokenBody
	ErrRotateBodyForTest             = errRotateBody
	ErrTokenQueryForTest             = errTokenQuery
	ErrPolicyBodyForTest             = errPolicyBody
	ErrBreakGlassGoneForTest         = errBreakGlassGone
	ErrPrincipalTokenBodyForTest     = errPrincipalTokenBody
	ErrNoTokenStoreForTest           = errNoTokenStore
	ErrPrincipalBodyForTest          = errPrincipalBody
	ErrPrincipalChangeBodyForTest    = errPrincipalChangeBody
	ErrGroupBodyForTest              = errGroupBody
	ErrGroupChangeBodyForTest        = errGroupChangeBody
	ErrMemberBodyForTest             = errMemberBody
	ErrNamespacePrincipalBodyForTest = errNamespacePrincipalBody
	ErrPageQueryForTest              = errPageQuery
	ErrNoPrincipalStoreForTest       = errNoPrincipalStore
)

// CompleteLocalForTest gives the password sign-in in to the sign-in service,
// as the password route does after the check of the password, so that a test
// can send it a hostile Assertion that the service never makes. It returns
// true when the service started a session.
func (h *Handler) CompleteLocalForTest(w http.ResponseWriter, r *http.Request, in localusersvc.SignedIn) bool {
	_, ok := h.completeLocal(w, r, in)

	return ok
}

// BodyTooLargeForTest returns the error of a body longer than limit bytes.
func BodyTooLargeForTest(limit int64) error {
	return errBodyTooLarge(limit)
}
