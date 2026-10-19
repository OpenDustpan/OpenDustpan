package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

// devCodePath is the route that signs a browser in with the one-time code of
// opendustpan dev. Only a handler that got a DevCode serves it.
const devCodePath = "/api/dustpan/v1/sign-in/dev-code"

// Names of the person that the one-time code signs in. The provider name is
// part of the subject, as for each other person that a sign-in makes.
const (
	devProvider    = "dev"
	devSubject     = devProvider + ":admin"
	devDisplayName = "Dev administrator"
	// maxDevCodeBody caps the body of a request, in bytes. The code holds
	// 43 characters.
	maxDevCodeBody = 1024
)

// errDevAccess reports a handler that has no access service. The dev sign-in
// then cannot give the role of the administrator, and a session without the
// role is not the session that the link promises.
var errDevAccess = errors.New("the dev sign-in needs the access service to give the administrator role")

// errDevAdmin reports a dev sign-in with no bootstrap administrator to grant.
var errDevAdmin = errors.New("the dev sign-in needs the bootstrap administrator")

// errDevCode reports a code that is wrong, spent or too old. One text covers
// the three cases, so an answer tells nothing about the code.
var errDevCode = errors.New(
	"the sign-in code is not valid: sign in to the console with the token from the token file",
)

// devCodeRequest is the body of the request of the dev sign-in.
type devCodeRequest struct {
	Code string `json:"code"`
}

// readDevCode reads the code from the body of r. A body that is not one JSON
// object with the field code gives an empty code.
func readDevCode(w http.ResponseWriter, r *http.Request) string {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDevCodeBody))
	decoder.DisallowUnknownFields()

	var body devCodeRequest
	if err := decoder.Decode(&body); err != nil {
		return ""
	}

	return body.Code
}

// devCodeSignIn signs a browser in as the dev administrator when the body
// holds the one-time code. It answers 204 with the session cookie. A wrong,
// spent or old code spends one unit of the failure budget of the address of
// the request, and answers 403 with one text.
func (h *Handler) devCodeSignIn(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	noStore(w)

	if err := h.admitSignIn(ctx); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if h.access == nil {
		h.writeError(ctx, w, errDevAccess)

		return
	}

	if !h.signIn.devAdmin.Admin || h.signIn.devAdmin.Subject == "" {
		h.writeError(ctx, w, errDevAdmin)

		return
	}

	if !h.signIn.devCode.Redeem(readDevCode(w, r)) {
		h.failSignIn(ctx)
		h.writeError(ctx, w, errDevCode)

		return
	}

	e := signInEntry(r, devSubject, "", session.ActionSignIn)

	p, _, err := h.principals.SignInPrincipal(ctx, e, principal.SignInInput{
		Subject: devSubject, Source: principal.SourceLocal, Names: []string{devDisplayName}, Create: true,
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if grantErr := h.grantDevAdministrator(ctx, e, p.ID); grantErr != nil {
		h.writeError(ctx, w, grantErr)

		return
	}

	sess, secret, err := h.signIn.sessions.Start(ctx, e, session.Start{
		AuthTime: h.signIn.sessions.Now(), PrincipalID: p.ID, Provider: devProvider, SourceIP: e.SourceIP,
		UserAgent: e.UserAgent, Stored: false,
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	http.SetCookie(w, browserCookie(SessionCookie, secret.Reveal(), h.sessionLength(ctx, sess)))
	w.WriteHeader(http.StatusNoContent)
}

// grantDevAdministrator gives the site-administrator role on the
// installation to the principal id. The grant is made by the bootstrap
// administrator of dev, the principal of the token in the token file. A
// principal that holds the grant already is not an error: the person of an
// earlier start keeps it. It fails when the handler has no access service,
// so a session never starts without the role.
func (h *Handler) grantDevAdministrator(ctx context.Context, e audit.Entry, id string) error {
	if h.access == nil {
		return errDevAccess
	}

	_, err := h.access.Grant(ctx, e, h.signIn.devAdmin, access.GrantInput{
		Object:  identity.ServerObject(),
		Role:    access.RoleSiteAdministrator,
		Subject: access.Subject{Kind: access.SubjectPrincipal, ID: id},
	})
	if err != nil && !errors.Is(err, access.ErrAlreadyExists) {
		return err
	}

	return nil
}

// devSignInRoutes returns the row of the dev sign-in. No code mounts no
// route.
func (h *Handler) devSignInRoutes() []Route {
	if h.signIn == nil || h.signIn.devCode == nil {
		return nil
	}

	return []Route{
		{
			Method: http.MethodPost, Path: devCodePath, Scope: ScopePublic, Actions: nil, Resolve: nil,
			ActionsFor: nil, Serve: h.devCodeSignIn,
		},
	}
}
