package server

import (
	"context"
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
	return classify(err)
}

// DevCodeSignInForTest calls the dev sign-in handler of h directly, as the
// route table would, so a test sees what the handler alone does when its
// route is not mounted.
func DevCodeSignInForTest(h *Handler, w http.ResponseWriter, r *http.Request) {
	//nolint:exhaustruct_v5 // The handler ignores the caller and the object.
	h.devCodeSignIn(w, r, identity.Principal{}, identity.Object{})
}
