package pulumi

import (
	"errors"
	"net/http"

	"opendustpan.org/opendustpan/internal/sessionauth"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Refusals of a console session on the routes of the Pulumi CLI that the
// console does not use. Each answers 403.
var (
	// errSessionUpdate refuses a session on the routes that create and
	// start an update. The lease of an update that a session starts has
	// no access token to check, so a sign-out would not stop it.
	errSessionUpdate = errors.New("a console session cannot start an update: use the Pulumi CLI with an access token")
	// errSessionSecrets refuses a session on the routes of the secrets
	// provider of the Pulumi CLI.
	errSessionSecrets = errors.New(
		"a console session cannot encrypt or decrypt secrets: use the Pulumi CLI with an access token",
	)
)

// refuseSession returns next for a request with an access token, and
// answers refusal to a request that the console session cookie signed in.
func (h *Handler) refuseSession(refusal error, next RouteHandler) RouteHandler {
	return func(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
		if sessionauth.FromSession(r.Context()) {
			h.writeError(r.Context(), w, refusal)

			return
		}

		next(w, r, p, obj)
	}
}
