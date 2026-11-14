package server

// Routes of the links from Git host accounts to principals. A person in a
// console session starts a link request, a signed event of the Git host proves
// the account that holds the one-time token, and the person accepts the account
// that proved itself. The routes below are the person's side of this flow. The
// proof comes from the intake of the events of the Git host and has no route of
// its own, so no client can name an account.
//
//   - caller:manage-git-host-links guards the changes of the person, on the
//     caller. A change accepts a console session only, never an access token,
//   - caller:read guards the list of the links of the person,
//   - server:manage-principals guards the delete of a link of another principal,
//     on the installation.
//
// No answer holds a token hash. The token appears once, in the answer to the
// start of a request.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/forge"
	"opendustpan.org/opendustpan/pkg/forgelink"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Paths of the routes of the Git host account links.
const (
	gitHostLinkRequestsPath = "/api/dustpan/v1/me/linked-account-requests"
	gitHostLinkShowPath     = gitHostLinkRequestsPath + "/show"
	gitHostLinksPath        = "/api/dustpan/v1/me/linked-accounts"
	gitHostLinkPath         = gitHostLinksPath + "/{kind}/{host}/{account}"
	principalGitHostLinks   = "/api/dustpan/v1/principals/{principal}/linked-accounts/{kind}/{host}/{account}"
)

// gitHostLinkRouteCount is the number of routes of the links when the intake is set.
const gitHostLinkRouteCount = 6

// maxGitHostLinkBodyBytes bounds the body of a route of the links. A token has
// at most 512 bytes.
const maxGitHostLinkBodyBytes = 4 * 1024

// The errors of the routes of the links that no domain sentinel carries, and the
// answers for the sentinels.
var (
	errGitHostLinkUnavailable = &codedError{
		status: http.StatusNotImplemented, code: errcode.GitHostLinkUnavailable, args: nil,
		message: "the store of this server keeps no Git host account links",
	}
	errGitHostLinkPersonRequired = &codedError{
		status: http.StatusForbidden, code: errcode.GitHostLinkPersonRequired, args: nil,
		message: "only a person who signed in to the console can link a Git host account: sign in and try again",
	}
	errGitHostLinkBody = &codedError{
		status: http.StatusBadRequest, code: errcode.GitHostLinkBodyInvalid, args: nil,
		message: `invalid body: send {"token":"<the token of the link request>"}`,
	}
	errGitHostLinkBodyTooLarge = &codedError{
		status: http.StatusRequestEntityTooLarge, code: errcode.GitHostLinkBodyTooLarge, args: nil,
		message: "request body too large: the limit is 4096 bytes",
	}
	errGitHostLinkAccount = &codedError{
		status: http.StatusBadRequest, code: errcode.GitHostLinkAccountInvalid, args: nil,
		message: "the kind, the host or the ID of the Git host account is not valid: the host is in lowercase letters",
	}
)

// linkFailure is a refused call of a route of the links. The zero value is no
// failure. outcome is the outcome of the audit entry of the refusal, and empty
// for an error of the server, which writes none.
type linkFailure struct {
	err     error
	outcome audit.Outcome
}

// failed reports whether f is a failure.
func (f linkFailure) failed() bool {
	return f.err != nil
}

// noFailure is the value of a call that did not fail.
func noFailure() linkFailure {
	return linkFailure{err: nil, outcome: ""}
}

// clientFailure returns the failure for the coded error c. A refusal of the
// caller (403) is a denied entry, and any other refusal of the client a failure
// entry.
func clientFailure(c *codedError) linkFailure {
	if c.status == http.StatusForbidden {
		return linkFailure{err: c, outcome: audit.OutcomeDenied}
	}

	return linkFailure{err: c, outcome: audit.OutcomeFailure}
}

// serverFailure returns the failure for an error that no code of this area
// names. The other classifiers answer it, and it writes no entry.
func serverFailure(err error) linkFailure {
	return linkFailure{err: err, outcome: ""}
}

// The answers for the sentinels of the link service and the errors that no
// sentinel carries.
var (
	errGitHostLinkLimit = &codedError{
		status: http.StatusTooManyRequests, code: errcode.GitHostLinkLimit, args: nil,
		message: "too many link requests that can still be accepted: wait 5 minutes or accept one",
	}
	errGitHostLinkExpired = &codedError{
		status: http.StatusGone, code: errcode.GitHostLinkRequestExpired, args: nil,
		message: "the link request expired: start a new one",
	}
	errGitHostLinkUsed = &codedError{
		status: http.StatusConflict, code: errcode.GitHostLinkRequestUsed, args: nil,
		message: "the link request was accepted already",
	}
	errGitHostLinkNotProved = &codedError{
		status: http.StatusConflict, code: errcode.GitHostLinkNotProved, args: nil,
		message: "no Git host account proved itself for this link request: post the token from the account first",
	}
	errGitHostLinkConflict = &codedError{
		status: http.StatusConflict, code: errcode.GitHostLinkConflict, args: nil,
		message: "the Git host account has another link than the one you saw: show the request again",
	}
	errGitHostLinkRequestNotFound = &codedError{
		status: http.StatusNotFound, code: errcode.GitHostLinkRequestNotFound, args: nil,
		message: "no link request of yours has this token",
	}
	errGitHostLinkNotFound = &codedError{
		status: http.StatusNotFound, code: errcode.GitHostLinkNotFound, args: nil,
		message: "no link of this Git host account exists",
	}
)

// gitHostLinkFailure maps an error of the link service that both kinds of call
// share. missing is the answer for an unknown request or link. A request of
// another person and an unknown token give the same answer.
func gitHostLinkFailure(err error, missing *codedError) linkFailure {
	switch {
	case errors.Is(err, forgelink.ErrNotPerson):
		return clientFailure(errGitHostLinkPersonRequired)
	case errors.Is(err, forgelink.ErrNotFound), errors.Is(err, forgelink.ErrDenied):
		return clientFailure(missing)
	case errors.Is(err, forgelink.ErrLimit):
		return clientFailure(errGitHostLinkLimit)
	case errors.Is(err, forgelink.ErrExpired):
		return clientFailure(errGitHostLinkExpired)
	case errors.Is(err, forgelink.ErrUsed):
		return clientFailure(errGitHostLinkUsed)
	case errors.Is(err, forgelink.ErrNotProved):
		return clientFailure(errGitHostLinkNotProved)
	case errors.Is(err, forgelink.ErrConflict):
		return clientFailure(errGitHostLinkConflict)
	case errors.Is(err, forgelink.ErrInvalid):
		return clientFailure(errGitHostLinkAccount)
	default:
		return serverFailure(err)
	}
}

// requestFailure maps an error of a call on a link request.
func requestFailure(err error) linkFailure {
	return gitHostLinkFailure(err, errGitHostLinkRequestNotFound)
}

// linkFailureOf maps an error of a call on a link.
func linkFailureOf(err error) linkFailure {
	return gitHostLinkFailure(err, errGitHostLinkNotFound)
}

// gitHostAccountBody is a Git host account on the wire. The login is text for a
// person to read: it is never compared.
type gitHostAccountBody struct {
	Kind  string `json:"kind"`
	Host  string `json:"host"`
	ID    string `json:"id"`
	Login string `json:"login"`
}

// gitHostLinkBody is a link on the wire.
type gitHostLinkBody struct {
	Account   gitHostAccountBody `json:"account"`
	Principal string             `json:"principal"`
	LinkedAt  time.Time          `json:"linkedAt"`
}

// gitHostLinkOf returns the wire form of l.
func gitHostLinkOf(l forgelink.Link) gitHostLinkBody {
	return gitHostLinkBody{
		Account: gitHostAccountBody{
			Kind: string(l.Account.Kind), Host: l.Account.Host, ID: l.Account.ID, Login: l.Login,
		},
		Principal: l.Principal, LinkedAt: l.LinkedAt.UTC(),
	}
}

// startGitHostLinkResponse is the answer to the start of a link request. Token
// is shown here and nowhere else.
type startGitHostLinkResponse struct {
	Token     string    `json:"token"`
	CreatedAt time.Time `json:"createdAt"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// gitHostLinkTokenRequest is the body of the routes that name a request by its
// token. The token is in the body and not in the URL, so no access log holds it.
type gitHostLinkTokenRequest struct {
	Token string `json:"token"`
}

// acceptGitHostLinkRequest is the body of the accept. PreviousPrincipal is the
// principal of the current link of the account that the page showed, and absent
// when the page showed none.
type acceptGitHostLinkRequest struct {
	Token             string  `json:"token"`
	PreviousPrincipal *string `json:"previousPrincipal,omitempty"`
}

// currentGitHostLinkBody is the link that the account has now, as the accept
// page shows it.
type currentGitHostLinkBody struct {
	Principal   string    `json:"principal"`
	DisplayName string    `json:"displayName"`
	LinkedAt    time.Time `json:"linkedAt"`
}

// gitHostLinkRequestResponse is the state of a link request for the accept page.
// Account is absent until a Git host account proved itself, and CurrentLink is
// absent when that account has no link.
type gitHostLinkRequestResponse struct {
	CreatedAt   time.Time               `json:"createdAt"`
	ExpiresAt   time.Time               `json:"expiresAt"`
	Proved      bool                    `json:"proved"`
	Account     *gitHostAccountBody     `json:"account,omitempty"`
	CurrentLink *currentGitHostLinkBody `json:"currentLink,omitempty"`
}

// acceptGitHostLinkResponse is the answer to an accept. ReplacedPrincipal is
// absent when the accept replaced no link.
type acceptGitHostLinkResponse struct {
	Link              gitHostLinkBody `json:"link"`
	ReplacedPrincipal *string         `json:"replacedPrincipal,omitempty"`
}

// gitHostLinkListResponse is the list of the links of the person.
type gitHostLinkListResponse struct {
	Links []gitHostLinkBody `json:"links"`
}

// gitHostLinkRoutes returns the rows of the routes of the Git host account links.
func (h *Handler) gitHostLinkRoutes() []Route {
	// Like the Git host routes, these routes are not mounted when the store keeps
	// no links. The handlers check the service again.
	if h.gitHostLinks == nil {
		return nil
	}

	manage := []identity.Action{identity.ActionCallerManageGitHostLinks}

	routes := make([]Route, 0, gitHostLinkRouteCount)
	routes = append(routes, []Route{
		{
			Method: http.MethodGet,
			Path:   gitHostLinksPath,
			Scope:  ScopeAuthorized,
			Actions: []identity.Action{
				identity.ActionCallerRead,
			},
			Resolve:      resolveCaller,
			ActionsFor:   nil,
			OpaqueDenial: false,
			Serve:        h.listGitHostLinks,
		},
		{
			Method: http.MethodDelete, Path: gitHostLinkPath, Scope: ScopeAuthorized, Actions: manage,
			Resolve: resolveCaller, ActionsFor: nil, OpaqueDenial: false, Serve: h.deleteGitHostLink,
		},
		{
			Method: http.MethodDelete,
			Path:   principalGitHostLinks,
			Scope:  ScopeAuthorized,
			Actions: []identity.Action{
				identity.ActionServerManagePrincipals,
			},
			Resolve:      resolveServer,
			ActionsFor:   nil,
			OpaqueDenial: false,
			Serve:        h.deletePrincipalGitHostLink,
		},
	}...)

	// A request that no Git host account can prove makes no link, so the routes
	// that start, show and accept one wait for the intake of the signed events.
	if !h.gitHostLinks.HasIntake() {
		return routes
	}

	return append(routes, []Route{
		{
			Method: http.MethodPost, Path: gitHostLinkRequestsPath, Scope: ScopeAuthorized, Actions: manage,
			Resolve: resolveCaller, ActionsFor: nil, OpaqueDenial: false, Serve: h.startGitHostLink,
		},
		{
			Method: http.MethodPost, Path: gitHostLinkShowPath, Scope: ScopeAuthorized, Actions: manage,
			Resolve: resolveCaller, ActionsFor: nil, OpaqueDenial: false, Serve: h.showGitHostLink,
		},
		{
			Method: http.MethodPost, Path: gitHostLinksPath, Scope: ScopeAuthorized, Actions: manage,
			Resolve: resolveCaller, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.fresh(identity.ActionCallerManageGitHostLinks, h.acceptGitHostLink),
		},
	}...)
}

// requireSessionCaller refuses a request that a console session did not make.
//
// Defense in depth: the link service refuses a caller without a session too,
// and the fresh check of the accept route refuses an access token before this
// handler runs. This check stays so that the body of such a caller is never
// read, and so that an access token cannot reach the service at all.
func requireSessionCaller(r *http.Request) linkFailure {
	if _, ok := sessionFromRequest(r); !ok {
		return clientFailure(errGitHostLinkPersonRequired)
	}

	return noFailure()
}

// readGitHostLinkBody reads one JSON object with no unknown field into dst, and
// refuses a body that is too large or not valid.
func readGitHostLinkBody(w http.ResponseWriter, r *http.Request, dst any) linkFailure {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxGitHostLinkBodyBytes))
	if tooLarge, isLimit := errors.AsType[*http.MaxBytesError](err); isLimit && tooLarge.Limit > 0 {
		return clientFailure(errGitHostLinkBodyTooLarge)
	}

	if err != nil {
		return clientFailure(errGitHostLinkBody)
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	if decErr := dec.Decode(dst); decErr != nil {
		return clientFailure(errGitHostLinkBody)
	}

	if _, more := dec.Token(); !errors.Is(more, io.EOF) {
		return clientFailure(errGitHostLinkBody)
	}

	return noFailure()
}

// refuseGitHostLink writes the entry of a refused change, when f has an outcome,
// and the error answer.
func (h *Handler) refuseGitHostLink(w http.ResponseWriter, r *http.Request, p identity.Principal, f linkFailure) {
	if f.outcome != "" {
		h.appendServerChange(r, p, serverChange{
			at: h.now(), action: identity.ActionCallerManageGitHostLinks, object: "@caller", outcome: f.outcome,
		})
	}

	h.writeError(r.Context(), w, f.err)
}

// startGitHostLink answers POST /api/dustpan/v1/me/linked-account-requests. It
// answers 201 with the one-time token, which is shown here only.
func (h *Handler) startGitHostLink(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	if h.gitHostLinks == nil {
		h.writeError(ctx, w, errGitHostLinkUnavailable)

		return
	}

	if f := requireSessionCaller(r); f.failed() {
		h.refuseGitHostLink(w, r, p, f)

		return
	}

	started, err := h.gitHostLinks.Start(ctx, p)
	if err != nil {
		h.refuseGitHostLink(w, r, p, requestFailure(err))

		return
	}

	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, http.StatusCreated, startGitHostLinkResponse{
		Token: started.Token, CreatedAt: started.Request.CreatedAt, ExpiresAt: started.Request.ExpiresAt,
	})
}

// showGitHostLink answers POST /api/dustpan/v1/me/linked-account-requests/show:
// the state of the request, the account that proved itself, and the link that
// the account has now. It changes nothing.
func (h *Handler) showGitHostLink(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	if h.gitHostLinks == nil {
		h.writeError(ctx, w, errGitHostLinkUnavailable)

		return
	}

	if f := requireSessionCaller(r); f.failed() {
		h.refuseGitHostLink(w, r, p, f)

		return
	}

	var req gitHostLinkTokenRequest
	if f := readGitHostLinkBody(w, r, &req); f.failed() {
		h.refuseGitHostLink(w, r, p, f)

		return
	}

	view, err := h.gitHostLinks.Show(ctx, p, req.Token)
	if err != nil {
		h.refuseGitHostLink(w, r, p, requestFailure(err))

		return
	}

	body := gitHostLinkRequestResponse{
		CreatedAt: view.Request.CreatedAt, ExpiresAt: view.Request.ExpiresAt, Proved: view.Request.IsProved(),
		Account: nil, CurrentLink: nil,
	}

	if view.Request.IsProved() {
		a := view.Request.Proved
		body.Account = &gitHostAccountBody{
			Kind: string(a.Account.Kind), Host: a.Account.Host, ID: a.Account.ID, Login: a.Login,
		}
	}

	if view.HasCurrent {
		body.CurrentLink = &currentGitHostLinkBody{
			Principal: view.Current.Principal, DisplayName: view.CurrentName, LinkedAt: view.Current.LinkedAt.UTC(),
		}
	}

	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, http.StatusOK, body)
}

// acceptGitHostLink answers POST /api/dustpan/v1/me/linked-accounts. The link
// names the account of the proof and the principal that started the request,
// never a value of the body. It answers 201 with the link, and 409 when the
// account has a link other than previousPrincipal. The route table wraps this
// handler with the fresh check: the accept needs a console session whose
// sign-in is inside the token freshness window.
func (h *Handler) acceptGitHostLink(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	if h.gitHostLinks == nil {
		h.writeError(ctx, w, errGitHostLinkUnavailable)

		return
	}

	if f := requireSessionCaller(r); f.failed() {
		h.refuseGitHostLink(w, r, p, f)

		return
	}

	var req acceptGitHostLinkRequest
	if f := readGitHostLinkBody(w, r, &req); f.failed() {
		h.refuseGitHostLink(w, r, p, f)

		return
	}

	previous := ""
	if req.PreviousPrincipal != nil {
		previous = *req.PreviousPrincipal
	}

	link, replaced, err := h.gitHostLinks.Accept(
		ctx, p, req.Token, previous, callerEntry(r, p, identity.ActionCallerManageGitHostLinks),
	)
	if err != nil {
		h.refuseGitHostLink(w, r, p, requestFailure(err))

		return
	}

	body := acceptGitHostLinkResponse{Link: gitHostLinkOf(link), ReplacedPrincipal: nil}
	if replaced != "" {
		body.ReplacedPrincipal = &replaced
	}

	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, http.StatusCreated, body)
}

// listGitHostLinks answers GET /api/dustpan/v1/me/linked-accounts with the links
// of the person.
func (h *Handler) listGitHostLinks(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	if h.gitHostLinks == nil {
		h.writeError(ctx, w, errGitHostLinkUnavailable)

		return
	}

	links, err := h.gitHostLinks.List(ctx, p)
	if err != nil {
		h.writeError(ctx, w, linkFailureOf(err).err)

		return
	}

	body := gitHostLinkListResponse{Links: make([]gitHostLinkBody, 0, len(links))}
	for i := range links {
		body.Links = append(body.Links, gitHostLinkOf(links[i]))
	}

	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, http.StatusOK, body)
}

// gitHostLinkAccount reads the account that the path names. The kind, the host
// and the ID match exactly, so a path in other letters names another account.
func gitHostLinkAccount(r *http.Request) (forgelink.Account, linkFailure) {
	a := forgelink.Account{
		Kind: forge.Kind(r.PathValue("kind")), Host: r.PathValue("host"), ID: r.PathValue("account"),
	}

	if err := a.Validate(); err != nil {
		var none forgelink.Account

		return none, clientFailure(errGitHostLinkAccount)
	}

	return a, noFailure()
}

// deleteGitHostLink answers DELETE /api/dustpan/v1/me/linked-accounts/{kind}/{host}/{account}.
// It deletes a link of the person and answers 204. A link of another principal
// answers 404 as a link that does not exist.
func (h *Handler) deleteGitHostLink(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	if h.gitHostLinks == nil {
		h.writeError(ctx, w, errGitHostLinkUnavailable)

		return
	}

	if f := requireSessionCaller(r); f.failed() {
		h.refuseGitHostLink(w, r, p, f)

		return
	}

	a, f := gitHostLinkAccount(r)
	if f.failed() {
		h.refuseGitHostLink(w, r, p, f)

		return
	}

	if err := h.gitHostLinks.Delete(ctx, p, a, callerEntry(r, p, identity.ActionCallerManageGitHostLinks)); err != nil {
		h.refuseGitHostLink(w, r, p, linkFailureOf(err))

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// deletePrincipalGitHostLink answers DELETE
// /api/dustpan/v1/principals/{principal}/linked-accounts/{kind}/{host}/{account}.
// An administrator deletes a link of the principal in the path and answers 204.
// A link of another principal answers 404.
func (h *Handler) deletePrincipalGitHostLink(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	if h.gitHostLinks == nil {
		h.writeError(ctx, w, errGitHostLinkUnavailable)

		return
	}

	a, f := gitHostLinkAccount(r)
	if f.failed() {
		h.refuseGitHostLink(w, r, p, f)

		return
	}

	err := h.gitHostLinks.DeleteFor(
		ctx, r.PathValue("principal"), a, callerEntry(r, p, identity.ActionServerManagePrincipals),
	)
	if err != nil {
		h.refuseGitHostLink(w, r, p, linkFailureOf(err))

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
