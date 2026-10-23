package server

import (
	"context"
	"log/slog"
	"net/http"

	"opendustpan.org/opendustpan/pkg/identity"
)

// WithPrincipalForTest returns ctx carrying p, as enforce stores the caller
// before it runs a resolver, so a test can call a resolver directly.
func WithPrincipalForTest(ctx context.Context, p identity.Principal) context.Context {
	return withPrincipal(ctx, p)
}

// ClassifyForTest returns the status and the message that the error
// responses of the handlers give for err.
func ClassifyForTest(err error) (int, string) {
	f := classify(err)

	return f.status, f.message
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

// DevCodeSignInForTest calls the dev sign-in handler of h directly, as the
// route table would, so a test sees what the handler alone does when its
// route is not mounted.
func DevCodeSignInForTest(h *Handler, w http.ResponseWriter, r *http.Request) {
	//nolint:exhaustruct_v5 // The handler ignores the caller and the object.
	h.devCodeSignIn(w, r, identity.Principal{}, identity.Object{})
}
