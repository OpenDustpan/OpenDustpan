package server

// Access token routes of the installation API (ADR 53). Each kind of token
// has its own action (decision 3):
//
//   - token:manage-own lets a person list, create, rotate and revoke its own
//     personal tokens under /api/server/caller/tokens. Its object is
//     @caller, and every authenticated person holds it with no grant.
//   - server:read-tokens lists and reads the tokens of every principal,
//     never their secrets. Its object is @server.
//   - server:manage-tokens revokes the personal tokens of other persons.
//     Its object is @server. No route makes or rotates a personal token of
//     another person: each person makes and rotates its own.
//   - principal:manage-tokens creates, rotates and revokes the tokens of a
//     service principal, under /api/server/principals/{principal}/tokens and
//     /api/server/service-tokens, and through DELETE /api/server/tokens/{id}.
//     Its object is the service principal, which the installation holds.
//
// A token acts as its principal. A holder of principal:manage-tokens can act
// as the service principal, with every grant that it has now or gets later:
// the grant is the decision. A revoke never raises access.
//
// A create and a rotation answer the secret of the new token once, with
// Cache-Control: no-store. No other answer holds a secret, and the server
// keeps only its digest.
//
// Each change writes its audit entry in the transaction of the change (fail
// closed). A refused change writes a failure entry after the fact, best
// effort, as the principal routes do.
//
// The list routes answer one page, newest first: the query parameters limit
// (1 to 1000; 100 when the request names none), cursor (the nextCursor of
// the page before), principal (the ID of one principal, on the
// administrator route) and status (active, expiring for an active token
// with 14 days or less left, or ended for a revoked or expired token; every
// token when the request names none).

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Body and query errors of the token routes. classify maps each to 400.
var (
	errTokenBody = errors.New(
		`invalid access token body: provide {"principalID":"prn-...","name":"...","expiresAt":"<RFC 3339 time>"}, ` +
			`or {"name":"...","expiresAt":"..."} with no principalID for an own token`,
	)
	errRotateBody = errors.New(
		`invalid rotation body: provide {} or {"oldTokenEndsAt":"<RFC 3339 time>","expiresAt":"<RFC 3339 time>"}`,
	)
	errTokenQuery = errors.New(
		"invalid token list query: status is active, expiring or ended, and limit is a number from 1 to 1000",
	)
	errPrincipalTokenBody = errors.New(
		`invalid access token body: provide {"name":"...","expiresAt":"<RFC 3339 time>"}; ` +
			`the path names the principal`,
	)
)

// errNoTokenStore reports a server whose store keeps no access tokens.
var errNoTokenStore = errors.New("the store of this server keeps no access tokens")

// tokenResponse is one access token on the wire, without its secret. Status
// is active, expired, revoked or disabled at the time of the answer. A null
// lastUsedAt means that no request used the token. ReplacedBy names the
// token that a rotation issued for this one, or is empty.
//
// Contract: never remove or rename these fields. The console and the
// clients read only this API, and they update on their own schedule.
type tokenResponse struct {
	LastUsedAt *string `json:"lastUsedAt"`
	RevokedAt  *string `json:"revokedAt"`
	DisabledAt *string `json:"disabledAt"`
	// ExpiresInDays is the count of days until the expiry, rounded up, at
	// the time of the answer by the server clock, or null for a token that
	// is not active. A client warns from it with no need of its own clock.
	ExpiresInDays *int64 `json:"expiresInDays"`
	ID            string `json:"id"`
	PrincipalID   string `json:"principalID"`
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Status        string `json:"status"`
	CreatedAt     string `json:"createdAt"`
	ExpiresAt     string `json:"expiresAt"`
	ReplacedBy    string `json:"replacedBy"`
	// CreatedBy is the subject of the caller that made the token: by a
	// create, or by the rotation that issued it.
	CreatedBy string `json:"createdBy"`
	// ExpiryWarning is none, soon (14 days or less before the expiry) or
	// imminent (3 days or less), at the time of the answer (ADR 53,
	// decision 11).
	ExpiryWarning string `json:"expiryWarning"`
}

// newTokenResponse is the answer of a create: the token and its secret,
// shown once.
//
// Contract: never remove or rename these fields.
type newTokenResponse struct {
	tokenResponse

	Secret string `json:"secret"`
}

// rotationResponse is the answer of a rotation: the new token with its
// secret, shown once, and the old token with its new end time.
//
// Contract: never remove or rename these fields.
type rotationResponse struct {
	Token    newTokenResponse `json:"token"`
	Previous tokenResponse    `json:"previous"`
}

// limitedTokenListResponse is one page of tokens with the longest lifetime
// that a new token of the listed principal can get.
//
// Contract: never remove or rename these fields.
type limitedTokenListResponse struct {
	tokenListResponse

	// MaxLifetimeSeconds is the maximum lifetime, in seconds, that applies
	// to a token of this caller and this principal: a create or a rotation
	// that asks for a later expiry answers 400. Today it is the maximum of
	// the installation policy. When a narrower scope, such as a namespace,
	// sets a shorter maximum, this field answers the shorter one, so its
	// meaning does not change.
	MaxLifetimeSeconds int64 `json:"maxLifetimeSeconds"`
}

// tokenListResponse wraps one page of tokens. Total is the count of every
// token that matches the filters, on every page. NextCursor is empty on the
// last page.
type tokenListResponse struct {
	Tokens     []tokenResponse `json:"tokens"`
	NextCursor string          `json:"nextCursor"`
	Total      int64           `json:"total"`
}

// createTokenRequest is the body of a token create. A missing expiresAt
// takes the default lifetime.
type createTokenRequest struct {
	PrincipalID *string    `json:"principalID"`
	Name        *string    `json:"name"`
	ExpiresAt   *time.Time `json:"expiresAt"`
}

// principalTokenRequest is the body of a token create on the path of a
// principal. A missing expiresAt takes the default lifetime.
type principalTokenRequest struct {
	Name      *string    `json:"name"`
	ExpiresAt *time.Time `json:"expiresAt"`
}

// rotateTokenRequest is the body of a rotation. Each field is optional.
type rotateTokenRequest struct {
	OldTokenEndsAt *time.Time `json:"oldTokenEndsAt"`
	ExpiresAt      *time.Time `json:"expiresAt"`
}

// Route paths of the access tokens.
//
//nolint:gosec // G101 reads "token" in the names: the values are route paths, not credentials.
const (
	tokensPath        = "/api/server/tokens"
	tokenPath         = "/api/server/tokens/{id}"
	serviceTokensPath = "/api/server/service-tokens"
	serviceTokenPath  = "/api/server/service-tokens/{id}"
	ownTokensPath     = "/api/server/caller/tokens"
	ownTokenPath      = "/api/server/caller/tokens/{id}"
	// principalTokensPath holds the tokens of one service principal.
	principalTokensPath = "/api/server/principals/{principal}/tokens"
	principalTokenPath  = "/api/server/principals/{principal}/tokens/{id}"
)

// optionalWireTime formats t for the wire, or answers nil for the zero
// time.
func optionalWireTime(t time.Time) *string {
	if t.IsZero() {
		return nil
	}

	s := wireTime(t)

	return &s
}

func tokenOf(t accesstoken.Token, now time.Time) tokenResponse {
	var expiresInDays *int64
	if days, active := t.DaysLeft(now); active {
		expiresInDays = &days
	}

	return tokenResponse{
		ExpiresInDays: expiresInDays,
		LastUsedAt:    optionalWireTime(t.LastUsedAt),
		RevokedAt:     optionalWireTime(t.RevokedAt),
		DisabledAt:    optionalWireTime(t.DisabledAt),
		ID:            t.ID,
		PrincipalID:   t.PrincipalID,
		Name:          t.Name,
		Kind:          string(t.Kind),
		Status:        string(t.StatusAt(now)),
		CreatedAt:     wireTime(t.CreatedAt),
		ExpiresAt:     wireTime(t.ExpiresAt),
		ReplacedBy:    t.ReplacedBy,
		CreatedBy:     t.CreatedBy,
		ExpiryWarning: string(t.ExpiryWarningAt(now)),
	}
}

// tokenOp is one token operation. It answers its result, or an error with
// the result so far: a change names its record in both. p is the caller,
// and e is its audit entry, which a change hands to the Service.
type tokenOp func(
	w http.ResponseWriter, r *http.Request, svc *accesstoken.Service, p identity.Principal, e audit.Entry,
) (principalResult, error)

// serveTokenRead adapts a read to a route handler.
func (h *Handler) serveTokenRead(op tokenOp) RouteHandler {
	return func(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
		var none audit.Entry

		if res, err := h.runTokenOp(w, r, op, p, none); err == nil {
			h.writePrincipalResult(w, r, res)
		}
	}
}

// serveTokenChange adapts a change to a route handler. The Service writes
// the success entry in the transaction of the change. A refused change
// writes one failure entry here, best effort, with the missing actions of a
// reach refusal. An answer with a secret carries Cache-Control: no-store.
func (h *Handler) serveTokenChange(action identity.Action, op tokenOp) RouteHandler {
	return func(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
		r = r.WithContext(context.WithValue(r.Context(), freshnessKey{}, h.checkTokenFreshness))
		res, err := h.runTokenOp(w, r, op, p, callerEntry(r, p, action))

		if err != nil && res.object != "" && h.tokens != nil {
			h.appendServerChangeReason(r, p, serverChange{
				at: h.tokens.Now(), action: action, object: res.object, outcome: audit.OutcomeFailure,
			}, reachReason(err))
		}

		if err == nil {
			w.Header().Set("Cache-Control", "no-store")
			h.writePrincipalResult(w, r, res)
		}
	}
}

// runTokenOp runs op and writes its error. It answers the result in both
// cases, and the error of the operation.
func (h *Handler) runTokenOp(
	w http.ResponseWriter, r *http.Request, op tokenOp, p identity.Principal, e audit.Entry,
) (principalResult, error) {
	ctx := r.Context()

	if h.tokens == nil {
		h.writeError(ctx, w, errNoTokenStore)

		return principalResult{body: nil, object: "", status: 0}, errNoTokenStore
	}

	res, err := op(w, r, h.tokens, p, e)
	if err != nil {
		h.writeError(ctx, w, err)

		return res, err
	}

	return res, nil
}

// resolveCaller resolves the object of the own-token routes: the caller.
func resolveCaller(_ http.ResponseWriter, _ *http.Request) (identity.Object, error) {
	return identity.CallerObject(), nil
}

// tokenRoutes returns the access token rows of the route table.
func (h *Handler) tokenRoutes() []Route {
	read := []identity.Action{identity.ActionServerReadTokens}

	return append([]Route{
		{
			Method:       http.MethodGet,
			Path:         tokensPath,
			Scope:        ScopeAuthorized,
			Actions:      read,
			Resolve:      resolveServer,
			ActionsFor:   nil,
			OpaqueDenial: false,
			Serve:        h.serveTokenRead(listTokens),
		},
		{
			Method:       http.MethodGet,
			Path:         tokenPath,
			Scope:        ScopeAuthorized,
			Actions:      read,
			Resolve:      resolveServer,
			ActionsFor:   nil,
			OpaqueDenial: false,
			Serve:        h.serveTokenRead(getToken),
		},
		{
			// A service token needs principal:manage-tokens on its
			// principal, a personal token server:manage-tokens on the
			// installation.
			Method: http.MethodDelete, Path: tokenPath, Scope: ScopeAuthorized,
			Actions:      []identity.Action{identity.ActionServerManageTokens, identity.ActionPrincipalManageTokens},
			Resolve:      h.resolveTokenOwner,
			ActionsFor:   revokeActions,
			OpaqueDenial: true,
			Serve: h.serveTokenChange(
				identity.ActionServerManageTokens,
				opaqueTokenState(revokeToken(accesstoken.ScopePersonal)),
			),
		},
	}, h.serviceTokenRoutes()...)
}

// serviceTokenRoutes returns the rows of the tokens of service principals.
// Each declares principal:manage-tokens on the service principal of the
// token.
func (h *Handler) serviceTokenRoutes() []Route {
	service := []identity.Action{identity.ActionPrincipalManageTokens}
	serve := func(op tokenOp) RouteHandler {
		return h.serveTokenChange(identity.ActionPrincipalManageTokens, op)
	}

	return []Route{
		{
			Method:       http.MethodPost,
			Path:         serviceTokensPath,
			Scope:        ScopeAuthorized,
			Actions:      service,
			Resolve:      resolveServiceTokenCreate,
			ActionsFor:   nil,
			OpaqueDenial: false,
			Serve:        serve(createToken(accesstoken.ScopeService)),
		},
		{
			Method:       http.MethodPost,
			Path:         serviceTokenPath + "/rotate",
			Scope:        ScopeAuthorized,
			Actions:      service,
			Resolve:      h.resolveTokenOwner,
			ActionsFor:   nil,
			OpaqueDenial: true,
			Serve:        serve(freshFirst(opaqueTokenState(rotateToken(accesstoken.ScopeService)))),
		},
		{
			Method: http.MethodPost, Path: principalTokensPath, Scope: ScopeAuthorized, Actions: service,
			Resolve: resolvePrincipalPath, ActionsFor: nil, OpaqueDenial: false, Serve: serve(createPrincipalToken),
		},
		{
			Method:       http.MethodGet,
			Path:         principalTokensPath,
			Scope:        ScopeAuthorized,
			Actions:      service,
			Resolve:      resolvePrincipalPath,
			ActionsFor:   nil,
			OpaqueDenial: false,
			Serve:        h.serveTokenRead(listPrincipalTokens),
		},
		{
			Method: http.MethodPost, Path: principalTokenPath + "/rotate", Scope: ScopeAuthorized, Actions: service,
			Resolve: resolvePrincipalPath, ActionsFor: nil, OpaqueDenial: false,
			Serve: serve(ofPathPrincipal(rotateToken(accesstoken.ScopeService))),
		},
		{
			Method: http.MethodDelete, Path: principalTokenPath, Scope: ScopeAuthorized, Actions: service,
			Resolve: resolvePrincipalPath, ActionsFor: nil, OpaqueDenial: false,
			Serve: serve(ofPathPrincipal(revokeToken(accesstoken.ScopeService))),
		},
	}
}

// revokeActions returns the action of DELETE /api/server/tokens/{id} on
// obj: principal:manage-tokens on the principal of a service token, else
// server:manage-tokens.
func revokeActions(obj identity.Object) []identity.Action {
	if obj.IsPrincipal() {
		return []identity.Action{identity.ActionPrincipalManageTokens}
	}

	return []identity.Action{identity.ActionServerManageTokens}
}

// resolvePrincipalPath returns the principal that the path names. A
// malformed ID answers 400.
func resolvePrincipalPath(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	return identity.ParsePrincipalObject(r.PathValue("principal"))
}

// resolveTokenOwner returns the object of a change of the token in the
// path: the principal of a service token, else the installation. A token
// that does not exist, a malformed ID and a token of the wrong kind resolve
// to the installation, so the route check runs. On DELETE
// /api/server/tokens/{id} and POST /api/server/service-tokens/{id}/rotate,
// the handler then answers the opaque 403, so a caller cannot learn whether
// the token exists.
func (h *Handler) resolveTokenOwner(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	if h.tokens == nil {
		return identity.ServerObject(), nil
	}

	t, err := h.tokens.Get(r.Context(), r.PathValue("id"))
	if errors.Is(err, accesstoken.ErrNotFound) || errors.Is(err, accesstoken.ErrInvalid) {
		return identity.ServerObject(), nil
	}

	if err != nil {
		return identity.Object{}, fmt.Errorf("read access token: %w", err)
	}

	if t.Kind != accesstoken.KindService {
		return identity.ServerObject(), nil
	}

	return identity.ParsePrincipalObject(t.PrincipalID)
}

// resolveServiceTokenCreate returns the object of POST
// /api/server/service-tokens: the principal that the body names. It reads
// the body under the limit of a request body and restores r.Body, so the
// handler reads it again. A body with no principalID answers 400, before
// the route check and before any write. It does not read the principal, so
// a caller without the action gets 403 for every ID, and an unknown
// principal answers 404 from the Service after the route check.
func resolveServiceTokenCreate(w http.ResponseWriter, r *http.Request) (identity.Object, error) {
	body, err := readBody(w, r, errTokenBody)
	if err != nil {
		return identity.Object{}, err
	}

	r.Body = io.NopCloser(bytes.NewReader(body))

	var req createTokenRequest
	if decodeErr := decodeBody(body, &req, errTokenBody); decodeErr != nil {
		return identity.Object{}, decodeErr
	}

	if req.PrincipalID == nil {
		return identity.Object{}, errTokenBody
	}

	obj, err := identity.ParsePrincipalObject(*req.PrincipalID)
	if err != nil {
		return identity.Object{}, fmt.Errorf("%w: %q", accesstoken.ErrPrincipalNotFound, *req.PrincipalID)
	}

	return obj, nil
}

// createPrincipalToken serves POST /api/server/principals/{principal}/tokens:
// it issues a token for the service principal in the path and answers 201
// with the token and its secret. A principal that is not a service
// principal answers 400, and an unknown one 404.
func createPrincipalToken(
	w http.ResponseWriter, r *http.Request, svc *accesstoken.Service, p identity.Principal, e audit.Entry,
) (principalResult, error) {
	id := r.PathValue("principal")
	object := principal.Ref(id)

	if err := requireFresh(r); err != nil {
		return failed(object), err
	}

	var req principalTokenRequest
	if err := decodeStrict(w, r, &req, errPrincipalTokenBody); err != nil {
		return failed(object), err
	}

	if req.Name == nil {
		return failed(object), errPrincipalTokenBody
	}

	in := accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: id, Name: *req.Name}
	if req.ExpiresAt != nil {
		in.ExpiresAt = *req.ExpiresAt
	}

	t, secret, err := svc.Create(r.Context(), p, e, accesstoken.ScopeService, in)
	if err != nil {
		return failed(object), err
	}

	body := newTokenResponse{tokenResponse: tokenOf(t, svc.Now()), Secret: secret.Reveal()}

	return ok(http.StatusCreated, body, accesstoken.TokenRef(t.PrincipalID, t.ID)), nil
}

// ofPathPrincipal returns op behind a check that the token in the path
// belongs to the principal in the path: a token of another principal
// answers 404, so the route of one principal tells nothing about the
// tokens of another. These routes are for service principals only: a
// personal token answers 400.
func ofPathPrincipal(op tokenOp) tokenOp {
	return func(
		w http.ResponseWriter, r *http.Request, svc *accesstoken.Service, p identity.Principal, e audit.Entry,
	) (principalResult, error) {
		id := r.PathValue("id")

		t, err := svc.Get(r.Context(), id)
		switch {
		case err != nil:
		case t.PrincipalID != r.PathValue("principal"):
			err = fmt.Errorf("%w: %q", accesstoken.ErrNotFound, id)
		case t.Kind != accesstoken.KindService:
			err = fmt.Errorf("%w: its owner rotates and revokes a %s token through /api/server/caller/tokens/{id}, "+
				"and an administrator reads and revokes it through /api/server/tokens/{id}", accesstoken.ErrWrongKind,
				t.Kind)
		default:
		}

		if err != nil {
			return failed(principal.Ref(r.PathValue("principal"))), err
		}

		return op(w, r, svc, p, e)
	}
}

// ownTokenRoutes returns the rows of the own personal tokens of a person.
func (h *Handler) ownTokenRoutes() []Route {
	own := []identity.Action{identity.ActionTokenManageOwn}

	return []Route{
		{
			Method:       http.MethodGet,
			Path:         ownTokensPath,
			Scope:        ScopeAuthorized,
			Actions:      own,
			Resolve:      resolveCaller,
			ActionsFor:   nil,
			OpaqueDenial: false,
			Serve:        h.serveTokenRead(listOwnTokens),
		},
		{
			Method:     http.MethodPost,
			Path:       ownTokensPath,
			Scope:      ScopeAuthorized,
			Actions:    own,
			Resolve:    resolveCaller,
			ActionsFor: nil, OpaqueDenial: false,
			Serve: h.serveTokenChange(identity.ActionTokenManageOwn, createToken(accesstoken.ScopeOwn)),
		},
		{
			Method:     http.MethodDelete,
			Path:       ownTokenPath,
			Scope:      ScopeAuthorized,
			Actions:    own,
			Resolve:    resolveCaller,
			ActionsFor: nil, OpaqueDenial: false,
			Serve: h.serveTokenChange(identity.ActionTokenManageOwn, revokeToken(accesstoken.ScopeOwn)),
		},
		{
			Method: http.MethodPost, Path: ownTokenPath + "/rotate", Scope: ScopeAuthorized, Actions: own,
			Resolve: resolveCaller, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.serveTokenChange(identity.ActionTokenManageOwn, rotateToken(accesstoken.ScopeOwn)),
		},
	}
}

// createToken returns the op of a token create in scope: it issues a token
// and answers 201 with the token and its secret. For ScopeOwn the body
// names no principal: the token belongs to the person of the caller. An
// unknown principal answers 404. A principal whose kind does not fit the
// route, a bad name or a bad expiry answers 400. A caller without
// principal:manage-tokens on a service principal answers 403.
func createToken(scope accesstoken.Scope) tokenOp {
	return func(
		w http.ResponseWriter, r *http.Request, svc *accesstoken.Service, p identity.Principal, e audit.Entry,
	) (principalResult, error) {
		if err := requireFresh(r); err != nil {
			return failed("tokens"), err
		}

		var req createTokenRequest
		if err := decodeStrict(w, r, &req, errTokenBody); err != nil {
			return failed("tokens"), err
		}

		object := "tokens"
		if req.PrincipalID != nil {
			object = principal.Ref(*req.PrincipalID)
		}

		if req.Name == nil || (scope == accesstoken.ScopeOwn) != (req.PrincipalID == nil) {
			return failed(object), errTokenBody
		}

		in := accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: "", Name: *req.Name}
		if req.PrincipalID != nil {
			in.PrincipalID = *req.PrincipalID
		}

		if req.ExpiresAt != nil {
			in.ExpiresAt = *req.ExpiresAt
		}

		t, secret, err := svc.Create(r.Context(), p, e, scope, in)
		if err != nil {
			return failed(object), err
		}

		body := newTokenResponse{tokenResponse: tokenOf(t, svc.Now()), Secret: secret.Reveal()}

		return ok(http.StatusCreated, body, accesstoken.TokenRef(t.PrincipalID, t.ID)), nil
	}
}

// listTokens serves GET /api/server/tokens: one page, newest first.
func listTokens(
	_ http.ResponseWriter, r *http.Request, svc *accesstoken.Service, _ identity.Principal, _ audit.Entry,
) (principalResult, error) {
	req, err := tokenPageRequest(r)
	if err != nil {
		return failed(""), err
	}

	page, err := svc.List(r.Context(), req)
	if err != nil {
		return failed(""), err
	}

	return ok(http.StatusOK, tokenPageOf(page, svc.Now()), ""), nil
}

// listOwnTokens serves GET /api/server/caller/tokens: one page of the
// tokens of the person of the caller, newest first. The principal query
// parameter is ignored.
func listOwnTokens(
	_ http.ResponseWriter, r *http.Request, svc *accesstoken.Service, p identity.Principal, _ audit.Entry,
) (principalResult, error) {
	req, err := tokenPageRequest(r)
	if err != nil {
		return failed(""), err
	}

	page, err := svc.ListOwn(r.Context(), p, req)
	if err != nil {
		return failed(""), err
	}

	return limitedTokenPage(r, svc, page)
}

// listPrincipalTokens serves GET /api/server/principals/{principal}/tokens:
// one page of the tokens of the service principal in the path, newest
// first, with the query parameters of the other token lists. The principal
// query parameter is ignored. A principal that is not a service principal
// answers 400, and an unknown one 404.
func listPrincipalTokens(
	_ http.ResponseWriter, r *http.Request, svc *accesstoken.Service, p identity.Principal, _ audit.Entry,
) (principalResult, error) {
	req, err := tokenPageRequest(r)
	if err != nil {
		return failed(""), err
	}

	page, err := svc.ListOfPrincipal(r.Context(), p, r.PathValue("principal"), req)
	if err != nil {
		return failed(""), err
	}

	return limitedTokenPage(r, svc, page)
}

// limitedTokenPage answers page with the maximum lifetime of a new token.
func limitedTokenPage(r *http.Request, svc *accesstoken.Service, page accesstoken.Page) (principalResult, error) {
	maxLifetime, err := svc.MaxLifetime(r.Context())
	if err != nil {
		return failed(""), err
	}

	body := limitedTokenListResponse{
		tokenListResponse: tokenPageOf(page, svc.Now()), MaxLifetimeSeconds: int64(maxLifetime / time.Second),
	}

	return ok(http.StatusOK, body, ""), nil
}

// tokenPageOf maps one page of tokens to the wire at the time now.
func tokenPageOf(page accesstoken.Page, now time.Time) tokenListResponse {
	out := tokenListResponse{
		Tokens: make([]tokenResponse, 0, len(page.Items)), NextCursor: page.NextCursor, Total: page.Total,
	}

	for i := range page.Items {
		out.Tokens = append(out.Tokens, tokenOf(page.Items[i], now))
	}

	return out
}

// tokenPageRequest reads the query parameters of the list routes.
func tokenPageRequest(r *http.Request) (accesstoken.PageRequest, error) {
	q := queryOf(r)
	req := accesstoken.PageRequest{
		PrincipalID: q.Get("principal"), Cursor: q.Get("cursor"), Filter: accesstoken.FilterAll, Limit: 0,
	}

	filter, err := accesstoken.ParseFilter(q.Get("status"))
	if err != nil {
		return accesstoken.PageRequest{}, errTokenQuery
	}

	req.Filter = filter

	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return accesstoken.PageRequest{}, errTokenQuery
		}

		req.Limit = n
	}

	return req, nil
}

// getToken serves GET /api/server/tokens/{id}. An unknown ID answers 404.
func getToken(
	_ http.ResponseWriter, r *http.Request, svc *accesstoken.Service, _ identity.Principal, _ audit.Entry,
) (principalResult, error) {
	t, err := svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		return failed(""), err
	}

	return ok(http.StatusOK, tokenOf(t, svc.Now()), ""), nil
}

// revokeToken returns the op of a revoke in scope: it ends the token at
// once and answers 204. The record stays for the history. A token that was
// revoked answers 409. For ScopeOwn, a token of another principal answers
// 404.
func revokeToken(scope accesstoken.Scope) tokenOp {
	return func(
		_ http.ResponseWriter, r *http.Request, svc *accesstoken.Service, p identity.Principal, e audit.Entry,
	) (principalResult, error) {
		object := tokenObject(r, svc)

		if err := svc.Revoke(r.Context(), p, e, scope, r.PathValue("id")); err != nil {
			return failed(object), err
		}

		return ok(http.StatusNoContent, nil, object), nil
	}
}

// rotateToken returns the op of a rotation in scope: it issues a new token
// for the owner, sets an end time for the old one, and answers 201 with
// both (ADR 53, decision 8). A token that is not active answers 409, a
// token whose kind does not fit the route 400, and a failed escalation
// check 403.
func rotateToken(scope accesstoken.Scope) tokenOp {
	return func(
		w http.ResponseWriter, r *http.Request, svc *accesstoken.Service, p identity.Principal, e audit.Entry,
	) (principalResult, error) {
		object := tokenObject(r, svc)

		if err := requireFresh(r); err != nil {
			return failed(object), err
		}

		var req rotateTokenRequest
		if err := decodeStrict(w, r, &req, errRotateBody); err != nil {
			return failed(object), err
		}

		in := accesstoken.RotateInput{
			OldEndsAt: req.OldTokenEndsAt, ExpiresAt: time.Time{}, ID: r.PathValue("id"), Scope: scope,
		}
		if req.ExpiresAt != nil {
			in.ExpiresAt = *req.ExpiresAt
		}

		rot, secret, err := svc.Rotate(r.Context(), p, e, in)
		if err != nil {
			return failed(object), err
		}

		now := svc.Now()
		body := rotationResponse{
			Token:    newTokenResponse{tokenResponse: tokenOf(rot.New, now), Secret: secret.Reveal()},
			Previous: tokenOf(rot.Old, now),
		}

		return ok(http.StatusCreated, body, accesstoken.TokenRef(rot.Old.PrincipalID, rot.Old.ID)), nil
	}
}

// tokenObject returns the audit object of the token in the path:
// principal:<id>#token:<id> when the token exists, so a refused change
// names the principal too, else token:<id>.
func tokenObject(r *http.Request, svc *accesstoken.Service) string {
	id := r.PathValue("id")

	t, err := svc.Get(r.Context(), id)
	if err != nil {
		return accesstoken.Ref(id)
	}

	return accesstoken.TokenRef(t.PrincipalID, id)
}

// freshFirst runs the freshness check of a console session before op, so
// that a caller without a recent session gets the same answer for each token
// ID and learns nothing about which IDs exist. op keeps its own check.
func freshFirst(op tokenOp) tokenOp {
	return func(
		w http.ResponseWriter, r *http.Request, svc *accesstoken.Service, p identity.Principal, e audit.Entry,
	) (principalResult, error) {
		if err := requireFresh(r); err != nil {
			return failed(accesstoken.Ref(r.PathValue("id"))), err
		}

		return op(w, r, svc, p, e)
	}
}

// opaqueTokenState answers the opaque denial of the two routes whose object
// comes from the stored token for a token that does not exist, a malformed
// token ID and a token of the wrong kind. It keeps the full text for the log
// and unwraps only to identity.ErrDenied. Only the read of the token in the
// path decides about a malformed ID: an invalid request of op, for example
// an end time of a rotation in the past, keeps its own answer.
func opaqueTokenState(op tokenOp) tokenOp {
	return func(
		w http.ResponseWriter, r *http.Request, svc *accesstoken.Service, p identity.Principal, e audit.Entry,
	) (principalResult, error) {
		id := r.PathValue("id")

		// Defense in depth: the route check refuses a caller without the
		// action; this layer stops a caller that holds the action from
		// learning whether a token ID exists.
		if _, err := svc.Get(r.Context(), id); errors.Is(err, accesstoken.ErrNotFound) ||
			errors.Is(err, accesstoken.ErrInvalid) {
			return failed(accesstoken.Ref(id)), opaqueDenialError{err: err}
		}

		res, err := op(w, r, svc, p, e)
		if errors.Is(err, accesstoken.ErrNotFound) || errors.Is(err, accesstoken.ErrWrongKind) {
			return res, opaqueDenialError{err: err}
		}

		return res, err
	}
}

// classifyToken maps the access token errors to their status and message.
// A source over its failure budget answers 429 (internal/authgate). Next:
// a bad body, query, name, expiry or page, and a principal kind that gets no
// token, answer 400; an unknown token or principal 404; a sign-in that the
// caller needs, and a reach that is not inside the reach of the caller, 403;
// a token that ended 409; and a store with no token port 501. The domain
// wraps each sentinel with the ID or the value it acted on, never with
// driver text or a secret.
func classifyToken(err error) (failure, bool) {
	if f, ok := classifyTokenRequest(err); ok {
		return f, true
	}

	return classifyTokenState(err)
}

// classifyTokenRequest maps the errors of a refused credential, body, query
// or token to 429 and 400.
func classifyTokenRequest(err error) (failure, bool) {
	switch {
	case errors.Is(err, identity.ErrRateLimited):
		return failCoded(http.StatusTooManyRequests, "too many failed sign-ins from this address: try again later",
			errcode.AuthRateLimited), true
	case errors.Is(err, errTokenBody), errors.Is(err, errRotateBody), errors.Is(err, errPolicyBody),
		errors.Is(err, errBreakGlassGone), errors.Is(err, errPrincipalTokenBody):
		return failCoded(http.StatusBadRequest, err.Error(), errcode.RequestBodyInvalid), true
	case errors.Is(err, errTokenQuery):
		return failCoded(http.StatusBadRequest, err.Error(), errcode.RequestInvalidQuery), true
	case errors.Is(err, accesstoken.ErrInvalid):
		return failCoded(http.StatusBadRequest, err.Error(), errcode.TokenInvalid), true
	case errors.Is(err, accesstoken.ErrNoTokenForKind):
		return failCoded(http.StatusBadRequest, err.Error(), errcode.TokenKindHasNone), true
	}

	return fail(0, ""), false
}

// classifyTokenState maps the errors of an unknown, wrong, ended or taken
// token, of a reach that is not inside the reach of the caller, and of a
// store with no token port.
func classifyTokenState(err error) (failure, bool) {
	switch {
	case errors.Is(err, accesstoken.ErrNotFound):
		return failCoded(http.StatusNotFound, err.Error(), errcode.TokenNotFound), true
	case errors.Is(err, accesstoken.ErrPrincipalNotFound):
		return failCoded(http.StatusNotFound, err.Error(), errcode.PrincipalNotFound), true
	case errors.Is(err, accesstoken.ErrWrongKind):
		return failCoded(http.StatusBadRequest, err.Error(), errcode.TokenWrongKind), true
	case errors.Is(err, accesstoken.ErrNoOwnPrincipal):
		return failCoded(http.StatusForbidden, err.Error(), errcode.TokenCallerNotPerson), true
	case errors.Is(err, accesstoken.ErrSignInRequired):
		return failCoded(http.StatusForbidden, err.Error(), errcode.TokenRequiresSignIn), true
	case errors.Is(err, accesstoken.ErrReachExceeded):
		// Defense in depth: the server answers this refusal in keepDenied,
		// with the text that names the missing actions, and the code of
		// errcode.Forbidden. This case stays so that a caller that classifies
		// a token error directly cannot skip the 403 and the code.
		return failCoded(http.StatusForbidden, err.Error(), errcode.TokenReachExceeded), true
	case errors.Is(err, accesstoken.ErrEnded):
		return failCoded(http.StatusConflict, err.Error(), errcode.TokenEnded), true
	case errors.Is(err, accesstoken.ErrAlreadyExists):
		return failCoded(http.StatusConflict, err.Error(), errcode.TokenAlreadyExists), true
	case errors.Is(err, errNoTokenStore):
		return failCoded(http.StatusNotImplemented, err.Error(), errcode.TokenUnsupported), true
	}

	return fail(0, ""), false
}
