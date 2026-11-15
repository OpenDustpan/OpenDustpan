package server

// Session policy routes of the installation API. The policy belongs to the
// installation, so every route acts on the object @server:
//
//   - server:read-tokens reads the policy, as it reads the token policy,
//   - server:manage-sign-in changes the policy.
//
// Limits and the token freshness window are whole seconds on the wire. A
// new setting applies at the next check of a session.

import (
	"errors"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/session"
)

// errSessionPolicyBody reports a session policy change body that the route
// refuses.
var errSessionPolicyBody = errors.New(
	`invalid session policy body: provide one or more of {"idleSeconds":N,"maxLengthSeconds":N,` +
		`"tokenFreshnessSeconds":N}`,
)

// sessionPolicyPath is the route path of the session policy.
const sessionPolicyPath = "/api/server/session-policy"

// sessionPolicyResponse is the policy on the wire. A null updatedAt means
// that no administrator changed the defaults. tokenFreshnessSeconds is how
// old the sign-in of a console session can be when the session creates or
// rotates an access token, 0 for no check; tokenFreshnessDefault is true
// while no administrator set it, so the server default applies.
//
// Contract: never remove or rename these fields.
type sessionPolicyResponse struct {
	UpdatedAt             *string `json:"updatedAt"`
	IdleSeconds           int64   `json:"idleSeconds"`
	MaxLengthSeconds      int64   `json:"maxLengthSeconds"`
	TokenFreshnessSeconds int64   `json:"tokenFreshnessSeconds"`
	TokenFreshnessDefault bool    `json:"tokenFreshnessDefault"`
}

// sessionPolicyRequest is the body of a policy change. A missing field
// keeps its value.
type sessionPolicyRequest struct {
	IdleSeconds           *int64 `json:"idleSeconds"`
	MaxLengthSeconds      *int64 `json:"maxLengthSeconds"`
	TokenFreshnessSeconds *int64 `json:"tokenFreshnessSeconds"`
}

func sessionPolicyOf(p session.Policy) sessionPolicyResponse {
	return sessionPolicyResponse{
		UpdatedAt:        optionalWireTime(p.UpdatedAt),
		IdleSeconds:      int64(p.Limits.Idle / time.Second),
		MaxLengthSeconds: int64(p.Limits.MaxLength / time.Second),

		TokenFreshnessSeconds: int64(p.TokenFreshness / time.Second),
		TokenFreshnessDefault: p.TokenFreshnessDefault,
	}
}

// sessionPolicyRoutes returns the session policy rows of the route table.
// Nil service means the store keeps no session policy: no route is
// mounted.
func (h *Handler) sessionPolicyRoutes() []Route {
	if h.sessions == nil {
		return nil
	}

	return []Route{
		{
			Method: http.MethodGet,
			Path:   sessionPolicyPath,
			Scope:  ScopeAuthorized,
			Actions: []identity.Action{
				identity.ActionServerReadTokens,
			},
			Resolve:      resolveServer,
			ActionsFor:   nil,
			OpaqueDenial: false,
			Serve:        h.getSessionPolicy,
		},
		{
			Method: http.MethodPatch,
			Path:   sessionPolicyPath,
			Scope:  ScopeAuthorized,
			Actions: []identity.Action{
				identity.ActionServerManageSignIn,
			},
			Resolve:      resolveServer,
			ActionsFor:   nil,
			OpaqueDenial: false,
			Serve:        h.changeSessionPolicy,
		},
	}
}

// getSessionPolicy serves GET /api/server/session-policy.
func (h *Handler) getSessionPolicy(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
	ctx := r.Context()

	p, err := h.sessions.Policy(ctx)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, sessionPolicyOf(p))
}

// changeSessionPolicy serves PATCH /api/server/session-policy: it changes
// the limits of the body and answers 200 with the stored policy. A body
// that sets no field, or limits out of range, answers 400 and writes a
// failure entry. The service writes the success entry in the transaction
// of the change.
func (h *Handler) changeSessionPolicy(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	stored, err := h.setSessionPolicy(w, r, p)
	if err != nil {
		h.appendServerChange(r, p, serverChange{
			at: h.sessions.Now(), action: identity.ActionServerManageSignIn, object: session.PolicyRef,
			outcome: audit.OutcomeFailure,
		})
		h.writeError(ctx, w, err)

		return
	}

	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(ctx, w, http.StatusOK, sessionPolicyOf(stored))
}

// setSessionPolicy decodes the change body of r and applies it. A change
// from a console session needs a sign-in within the token freshness
// window, as a token change does: an old session could otherwise turn the
// window off and then create a token.
func (h *Handler) setSessionPolicy(
	w http.ResponseWriter, r *http.Request, p identity.Principal,
) (session.Policy, error) {
	if err := h.checkTokenFreshness(r); err != nil {
		return session.Policy{}, err
	}

	var req sessionPolicyRequest

	empty := func() bool {
		return req.IdleSeconds == nil && req.MaxLengthSeconds == nil && req.TokenFreshnessSeconds == nil
	}

	if err := decodeChange(w, r, &req, errSessionPolicyBody, empty); err != nil {
		return session.Policy{}, err
	}

	idle, err := limitSeconds(req.IdleSeconds)
	if err != nil {
		return session.Policy{}, err
	}

	maxLength, err := limitSeconds(req.MaxLengthSeconds)
	if err != nil {
		return session.Policy{}, err
	}

	freshness, err := limitSeconds(req.TokenFreshnessSeconds)
	if err != nil {
		return session.Policy{}, err
	}

	return h.sessions.SetPolicy(r.Context(), callerEntry(r, p, identity.ActionServerManageSignIn),
		session.PolicyChange{Idle: idle, MaxLength: maxLength, TokenFreshness: freshness})
}

// limitSeconds converts a count of seconds of a body to a duration, or
// answers nil for nil. A count below 0 or above session.MaxLengthBound
// answers errSessionPolicyBody, before the multiplication can overflow.
func limitSeconds(seconds *int64) (*time.Duration, error) {
	if seconds == nil {
		return nil, nil //nolint:nilnil // A missing field keeps its value.
	}

	if *seconds < 0 || *seconds > int64(session.MaxLengthBound/time.Second) {
		return nil, errSessionPolicyBody
	}

	d := time.Duration(*seconds) * time.Second

	return &d, nil
}

// classifySessionPolicy maps the session policy errors to their status and
// message: a bad body or limits out of range answer 400 with the error
// text.
func classifySessionPolicy(err error) (failure, bool) {
	switch {
	case errors.Is(err, errSessionPolicyBody), errors.Is(err, session.ErrInvalid):
		return fail(http.StatusBadRequest, err.Error()), true
	default:
		return fail(0, ""), false
	}
}
