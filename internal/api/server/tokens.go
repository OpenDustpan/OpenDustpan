package server

// Access token routes of the installation API (ADR 53). Tokens belong to
// principals of the installation, so every route acts on the object
// @server:
//
//   - server:read-tokens lists and reads tokens, never their secrets,
//   - server:manage-tokens creates, revokes and rotates tokens.
//
// A create and a rotation answer the secret of the new token once, with
// Cache-Control: no-store. No other answer holds a secret, and the server
// keeps only its digest.
//
// Each change writes its audit entry in the transaction of the change (fail
// closed). A refused change writes a failure entry after the fact, best
// effort, as the principal routes do.
//
// The list route answers one page, newest first: the query parameters limit
// (1 to 1000; 100 when the request names none), cursor (the nextCursor of
// the page before), principal (the ID of one principal) and include
// (ended, to also list revoked and expired tokens).

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Body and query errors of the token routes. classify maps each to 400.
var (
	errTokenBody = errors.New(
		`invalid access token body: provide {"principalID":"prn-...","name":"...","expiresAt":"<RFC 3339 time>"}`,
	)
	errRotateBody = errors.New(
		`invalid rotation body: provide {} or {"oldTokenEndsAt":"<RFC 3339 time>","expiresAt":"<RFC 3339 time>"}`,
	)
	errTokenQuery = errors.New("invalid token list query: include is empty or ended, limit is a number from 1 to 1000")
)

// errNoTokenStore reports a server whose store keeps no access tokens.
var errNoTokenStore = errors.New("the store of this server keeps no access tokens")

// tokenResponse is one access token on the wire, without its secret. Status
// is active, expired or revoked at the time of the answer. A null
// lastUsedAt means that no request used the token. ReplacedBy names the
// token that a rotation issued for this one, or is empty.
//
// Contract: never remove or rename these fields. The console and the
// clients read only this API, and they update on their own schedule.
type tokenResponse struct {
	LastUsedAt  *string `json:"lastUsedAt"`
	RevokedAt   *string `json:"revokedAt"`
	ID          string  `json:"id"`
	PrincipalID string  `json:"principalID"`
	Name        string  `json:"name"`
	Kind        string  `json:"kind"`
	Status      string  `json:"status"`
	CreatedAt   string  `json:"createdAt"`
	ExpiresAt   string  `json:"expiresAt"`
	ReplacedBy  string  `json:"replacedBy"`
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

// rotateTokenRequest is the body of a rotation. Each field is optional.
type rotateTokenRequest struct {
	OldTokenEndsAt *time.Time `json:"oldTokenEndsAt"`
	ExpiresAt      *time.Time `json:"expiresAt"`
}

// Route paths of the access tokens.
//
//nolint:gosec // G101 reads "token" in the names: the values are route paths, not credentials.
const (
	tokensPath = "/api/server/tokens"
	tokenPath  = "/api/server/tokens/{id}"
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
	return tokenResponse{
		LastUsedAt:  optionalWireTime(t.LastUsedAt),
		RevokedAt:   optionalWireTime(t.RevokedAt),
		ID:          t.ID,
		PrincipalID: t.PrincipalID,
		Name:        t.Name,
		Kind:        string(t.Kind),
		Status:      string(t.StatusAt(now)),
		CreatedAt:   wireTime(t.CreatedAt),
		ExpiresAt:   wireTime(t.ExpiresAt),
		ReplacedBy:  t.ReplacedBy,
	}
}

// tokenOp is one token operation. It answers its result, or an error with
// the result so far: a change names its record in both. e is the audit
// entry of the caller, which a change hands to the Service.
type tokenOp func(
	w http.ResponseWriter, r *http.Request, svc *accesstoken.Service, e audit.Entry,
) (principalResult, error)

// serveTokenRead adapts a read to a route handler.
func (h *Handler) serveTokenRead(op tokenOp) RouteHandler {
	return func(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
		var none audit.Entry

		if res, ok := h.runTokenOp(w, r, op, none); ok {
			h.writePrincipalResult(w, r, res)
		}
	}
}

// serveTokenChange adapts a change to a route handler. The Service writes
// the success entry in the transaction of the change. A refused change
// writes one failure entry here, best effort. An answer with a secret
// carries Cache-Control: no-store.
func (h *Handler) serveTokenChange(action identity.Action, op tokenOp) RouteHandler {
	return func(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
		res, ok := h.runTokenOp(w, r, op, callerEntry(r, p, action))

		if !ok && res.object != "" && h.tokens != nil {
			h.appendServerChange(r, p, serverChange{
				at: h.tokens.Now(), action: action, object: res.object, outcome: audit.OutcomeFailure,
			})
		}

		if ok {
			w.Header().Set("Cache-Control", "no-store")
			h.writePrincipalResult(w, r, res)
		}
	}
}

// runTokenOp runs op and writes its error. It reports whether op succeeded,
// with the result in both cases.
func (h *Handler) runTokenOp(
	w http.ResponseWriter,
	r *http.Request,
	op tokenOp,
	e audit.Entry,
) (principalResult, bool) {
	ctx := r.Context()

	if h.tokens == nil {
		h.writeError(ctx, w, errNoTokenStore)

		return principalResult{body: nil, object: "", status: 0}, false
	}

	res, err := op(w, r, h.tokens, e)
	if err != nil {
		h.writeError(ctx, w, err)

		return res, false
	}

	return res, true
}

// tokenRoutes returns the access token rows of the route table.
func (h *Handler) tokenRoutes() []Route {
	read := []identity.Action{identity.ActionServerReadTokens}
	manage := []identity.Action{identity.ActionServerManageTokens}

	return []Route{
		{
			Method:  http.MethodPost,
			Path:    tokensPath,
			Scope:   ScopeAuthorized,
			Actions: manage,
			Resolve: resolveServer,
			Serve:   h.serveTokenChange(identity.ActionServerManageTokens, createToken),
		},
		{
			Method: http.MethodGet, Path: tokensPath, Scope: ScopeAuthorized,
			Actions: read, Resolve: resolveServer, Serve: h.serveTokenRead(listTokens),
		},
		{
			Method: http.MethodGet, Path: tokenPath, Scope: ScopeAuthorized,
			Actions: read, Resolve: resolveServer, Serve: h.serveTokenRead(getToken),
		},
		{
			Method:  http.MethodDelete,
			Path:    tokenPath,
			Scope:   ScopeAuthorized,
			Actions: manage,
			Resolve: resolveServer,
			Serve:   h.serveTokenChange(identity.ActionServerManageTokens, revokeToken),
		},
		{
			Method:  http.MethodPost,
			Path:    tokenPath + "/rotate",
			Scope:   ScopeAuthorized,
			Actions: manage,
			Resolve: resolveServer,
			Serve:   h.serveTokenChange(identity.ActionServerManageTokens, rotateToken),
		},
	}
}

// createToken serves POST /api/server/tokens: it issues a token for the
// principal of the body and answers 201 with the token and its secret. An
// unknown principal answers 404. A workload, a bad name or a bad expiry
// answers 400.
func createToken(
	w http.ResponseWriter, r *http.Request, svc *accesstoken.Service, e audit.Entry,
) (principalResult, error) {
	var req createTokenRequest
	if err := decodeStrict(w, r, &req, errTokenBody); err != nil {
		return failed("tokens"), err
	}

	if req.PrincipalID == nil || req.Name == nil {
		return failed("tokens"), errTokenBody
	}

	in := accesstoken.Input{ExpiresAt: time.Time{}, PrincipalID: *req.PrincipalID, Name: *req.Name}
	if req.ExpiresAt != nil {
		in.ExpiresAt = *req.ExpiresAt
	}

	t, secret, err := svc.Create(r.Context(), e, in)
	if err != nil {
		return failed("tokens"), err
	}

	body := newTokenResponse{tokenResponse: tokenOf(t, svc.Now()), Secret: secret.Reveal()}

	return ok(http.StatusCreated, body, accesstoken.Ref(t.ID)), nil
}

// listTokens serves GET /api/server/tokens: one page, newest first.
func listTokens(
	_ http.ResponseWriter, r *http.Request, svc *accesstoken.Service, _ audit.Entry,
) (principalResult, error) {
	req, err := tokenPageRequest(r)
	if err != nil {
		return failed(""), err
	}

	page, err := svc.List(r.Context(), req)
	if err != nil {
		return failed(""), err
	}

	now := svc.Now()
	out := tokenListResponse{
		Tokens: make([]tokenResponse, 0, len(page.Items)), NextCursor: page.NextCursor, Total: page.Total,
	}

	for i := range page.Items {
		out.Tokens = append(out.Tokens, tokenOf(page.Items[i], now))
	}

	return ok(http.StatusOK, out, ""), nil
}

// tokenPageRequest reads the query parameters of the list route.
func tokenPageRequest(r *http.Request) (accesstoken.PageRequest, error) {
	q := r.URL.Query()
	req := accesstoken.PageRequest{
		PrincipalID: q.Get("principal"), Cursor: q.Get("cursor"), Limit: 0, IncludeEnded: false,
	}

	switch q.Get("include") {
	case "":
	case "ended":
		req.IncludeEnded = true
	default:
		return accesstoken.PageRequest{}, errTokenQuery
	}

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
	_ http.ResponseWriter, r *http.Request, svc *accesstoken.Service, _ audit.Entry,
) (principalResult, error) {
	t, err := svc.Get(r.Context(), r.PathValue("id"))
	if err != nil {
		return failed(""), err
	}

	return ok(http.StatusOK, tokenOf(t, svc.Now()), ""), nil
}

// revokeToken serves DELETE /api/server/tokens/{id}: it ends the token at
// once and answers 204. The record stays for the history. A token that was
// revoked answers 409.
func revokeToken(
	_ http.ResponseWriter, r *http.Request, svc *accesstoken.Service, e audit.Entry,
) (principalResult, error) {
	object := accesstoken.Ref(r.PathValue("id"))

	if err := svc.Revoke(r.Context(), e, r.PathValue("id")); err != nil {
		return failed(object), err
	}

	return ok(http.StatusNoContent, nil, object), nil
}

// rotateToken serves POST /api/server/tokens/{id}/rotate: it issues a new
// token for the owner, sets an end time for the old one, and answers 201
// with both (ADR 53, decision 8). A token that is not active answers 409.
func rotateToken(
	w http.ResponseWriter, r *http.Request, svc *accesstoken.Service, e audit.Entry,
) (principalResult, error) {
	object := accesstoken.Ref(r.PathValue("id"))

	var req rotateTokenRequest
	if err := decodeStrict(w, r, &req, errRotateBody); err != nil {
		return failed(object), err
	}

	in := accesstoken.RotateInput{OldEndsAt: req.OldTokenEndsAt, ExpiresAt: time.Time{}}
	if req.ExpiresAt != nil {
		in.ExpiresAt = *req.ExpiresAt
	}

	rot, secret, err := svc.Rotate(r.Context(), e, r.PathValue("id"), in)
	if err != nil {
		return failed(object), err
	}

	now := svc.Now()
	body := rotationResponse{
		Token:    newTokenResponse{tokenResponse: tokenOf(rot.New, now), Secret: secret.Reveal()},
		Previous: tokenOf(rot.Old, now),
	}

	return ok(http.StatusCreated, body, object), nil
}

// classifyToken maps the access token errors to their status and message:
// a bad body, query, name, expiry or page, and a principal kind that gets no
// token, answer 400; an unknown token or principal 404; a token that ended
// 409; and a store with no token port 501. The domain wraps each sentinel
// with the ID or the value it acted on, never with driver text or a secret.
func classifyToken(err error) (int, string, bool) {
	switch {
	case errors.Is(err, errTokenBody), errors.Is(err, errRotateBody), errors.Is(err, errTokenQuery),
		errors.Is(err, accesstoken.ErrInvalid), errors.Is(err, accesstoken.ErrNoTokenForKind):
		return http.StatusBadRequest, err.Error(), true
	case errors.Is(err, accesstoken.ErrNotFound), errors.Is(err, accesstoken.ErrPrincipalNotFound):
		return http.StatusNotFound, err.Error(), true
	case errors.Is(err, accesstoken.ErrEnded), errors.Is(err, accesstoken.ErrAlreadyExists):
		return http.StatusConflict, err.Error(), true
	case errors.Is(err, errNoTokenStore):
		return http.StatusNotImplemented, err.Error(), true
	default:
		return 0, "", false
	}
}
