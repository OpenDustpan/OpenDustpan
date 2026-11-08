package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/installation"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/session"
)

// devCodePath is the route that signs a browser in with the one-time code of
// opendustpan dev. Only a handler that got a DevCode serves it.
const devCodePath = "/api/dustpan/v1/sign-in/dev-code"

// Names of the person that the one-time code signs in. The provider name is
// part of the subject, as for each other person that a sign-in makes.
const (
	devProvider    = session.DevProviderID
	devSubject     = principal.DevAdminSubject
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

// errDevKind reports a dev sign-in on a handler of another installation kind.
var errDevKind = errors.New("the dev sign-in needs the installation kind dev")

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

	// Defense in depth: devSignInRoutes mounts this route only for the kind
	// dev, and the option of the server refuses a dev sign-in on another
	// kind. This check stays so that a handler built by another path, or a
	// change to the route table, never signs in on a production installation.
	if h.kind != installation.KindDev {
		h.writeError(ctx, w, errDevKind)

		return
	}

	if h.signIn == nil || h.signIn.devCode == nil {
		h.writeError(ctx, w, errDevCode)

		return
	}

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

	e := signInEntry(r, devSubject, session.ActionSignIn)

	// A grant that fails after this call leaves the person in place. The
	// person holds no role and no session, so it can do nothing, and the
	// next valid sign-in finds it and gives the role.
	p, _, err := h.principals.SignInDevAdministrator(ctx, e, devDisplayName)
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
		UserAgent: e.UserAgent, Generation: 0, Stored: false, Alert: nil,
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	http.SetCookie(w, sessionCookie(secret.Reveal(), h.sessionLength(ctx, sess)))
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
	// Defense in depth: the handler refuses the request on another kind too,
	// and the option of the server refuses the dev sign-in there. This check
	// stays so that the route is not even mounted on a production installation.
	if h.kind != installation.KindDev || h.signIn == nil || h.signIn.devCode == nil {
		return nil
	}

	return []Route{
		{
			Method: http.MethodPost, Path: devCodePath, Scope: ScopePublic, Actions: nil, Resolve: nil,
			ActionsFor: nil, OpaqueDenial: false, Serve: h.devCodeSignIn,
		},
	}
}
