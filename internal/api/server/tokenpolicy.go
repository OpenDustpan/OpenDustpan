package server

// Token life cycle policy routes of the installation API (ADR 53, decisions
// 2, 4, 5 and 6). The policy belongs to the installation, so every route
// acts on the object @server:
//
//   - server:read-tokens reads the policy,
//   - server:manage-token-policy changes the policy and runs the inactivity
//     disable at once. The server also runs it on its own schedule.
//
// Periods are whole seconds on the wire. An inactivity period of 0 turns the
// disable off.

import (
	"errors"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// errPolicyBody reports a policy change body that the route refuses.
var errPolicyBody = errors.New(
	`invalid token policy body: provide one or more of {"maxLifetimeSeconds":N,"inactivitySeconds":N,` +
		`"disableTokens":true,"disableAccounts":true,"disableBreakGlass":false}`,
)

// errBreakGlassGone reports a policy change that turns on the deprecated
// break-glass disable. A break-glass account is always exempt, with no
// setting.
var errBreakGlassGone = errors.New(
	`invalid token policy body: the disableBreakGlass setting is no longer supported`,
)

// tokenPolicyResponse is the policy on the wire. A null updatedAt means that
// no administrator changed the defaults.
//
// Contract: never remove or rename these fields. DisableBreakGlass is
// deprecated and always false: a break-glass account is always exempt from
// the inactivity disable.
type tokenPolicyResponse struct {
	UpdatedAt          *string `json:"updatedAt"`
	MaxLifetimeSeconds int64   `json:"maxLifetimeSeconds"`
	InactivitySeconds  int64   `json:"inactivitySeconds"`
	DisableTokens      bool    `json:"disableTokens"`
	DisableAccounts    bool    `json:"disableAccounts"`
	// DisableBreakGlass is deprecated and always false.
	DisableBreakGlass bool `json:"disableBreakGlass"`
	// ExchangeLifetimeSeconds is the lifetime of the access token that a
	// workload gets in a token exchange. The maximum lifetime bounds it too.
	ExchangeLifetimeSeconds int64 `json:"exchangeLifetimeSeconds"`
}

// tokenPolicyRequest is the body of a policy change. A missing field keeps
// its value. DisableBreakGlass is deprecated: only false is accepted.
type tokenPolicyRequest struct {
	MaxLifetimeSeconds *int64 `json:"maxLifetimeSeconds"`
	InactivitySeconds  *int64 `json:"inactivitySeconds"`
	DisableTokens      *bool  `json:"disableTokens"`
	DisableAccounts    *bool  `json:"disableAccounts"`
	// DisableBreakGlass is deprecated: only false is accepted.
	DisableBreakGlass       *bool  `json:"disableBreakGlass"`
	ExchangeLifetimeSeconds *int64 `json:"exchangeLifetimeSeconds"`
}

// sweepResponse names the tokens and the principals that a sweep disabled.
//
// Contract: never remove or rename these fields.
type sweepResponse struct {
	Tokens     []string `json:"tokens"`
	Principals []string `json:"principals"`
}

// Route paths of the token policy.
//
//nolint:gosec // G101 reads "token" in the names: the values are route paths, not credentials.
const (
	tokenPolicyPath      = "/api/server/token-policy"
	tokenPolicySweepPath = "/api/server/token-policy/sweep"
)

func tokenPolicyOf(p accesstoken.Policy) tokenPolicyResponse {
	return tokenPolicyResponse{
		UpdatedAt:          optionalWireTime(p.UpdatedAt),
		MaxLifetimeSeconds: int64(p.MaxLifetime / time.Second),
		InactivitySeconds:  int64(p.Inactivity / time.Second),
		DisableTokens:      p.DisableTokens,
		DisableAccounts:    p.DisableAccounts,
		DisableBreakGlass:  false,

		ExchangeLifetimeSeconds: int64(p.ExchangeLifetime / time.Second),
	}
}

// tokenPolicyRoutes returns the token policy rows of the route table.
func (h *Handler) tokenPolicyRoutes() []Route {
	manage := []identity.Action{identity.ActionServerManageTokenPolicy}

	return []Route{
		{
			Method: http.MethodGet,
			Path:   tokenPolicyPath,
			Scope:  ScopeAuthorized,
			Actions: []identity.Action{
				identity.ActionServerReadTokens,
			},
			Resolve:      resolveServer,
			ActionsFor:   nil,
			OpaqueDenial: false,
			Serve:        h.serveTokenRead(getTokenPolicy),
		},
		{
			Method:  http.MethodPatch,
			Path:    tokenPolicyPath,
			Scope:   ScopeAuthorized,
			Actions: manage,
			Resolve: resolveServer, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.serveTokenChange(identity.ActionServerManageTokenPolicy, changeTokenPolicy),
		},
		{
			Method:  http.MethodPost,
			Path:    tokenPolicySweepPath,
			Scope:   ScopeAuthorized,
			Actions: manage,
			Resolve: resolveServer, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.serveTokenChange(identity.ActionServerManageTokenPolicy, sweepInactive),
		},
	}
}

// getTokenPolicy serves GET /api/server/token-policy.
func getTokenPolicy(
	_ http.ResponseWriter, r *http.Request, svc *accesstoken.Service, _ identity.Principal, _ audit.Entry,
) (principalResult, error) {
	p, err := svc.Policy(r.Context())
	if err != nil {
		return failed(""), err
	}

	return ok(http.StatusOK, tokenPolicyOf(p), ""), nil
}

// changeTokenPolicy serves PATCH /api/server/token-policy: it changes the
// fields of the body and answers 200 with the stored policy. A body that
// sets no field, or a period out of range, answers 400. The deprecated
// disableBreakGlass field accepts only false: true answers 400.
func changeTokenPolicy(
	w http.ResponseWriter, r *http.Request, svc *accesstoken.Service, _ identity.Principal, e audit.Entry,
) (principalResult, error) {
	// A longer lifetime is a weaker setting, so the change needs a fresh sign-in.
	if err := requireFresh(r); err != nil {
		return failed(accesstoken.PolicyRef), err
	}

	var req tokenPolicyRequest

	empty := func() bool {
		return req.MaxLifetimeSeconds == nil && req.InactivitySeconds == nil && req.DisableTokens == nil &&
			req.DisableAccounts == nil && req.DisableBreakGlass == nil && req.ExchangeLifetimeSeconds == nil
	}

	if err := decodeChange(w, r, &req, errPolicyBody, empty); err != nil {
		return failed(accesstoken.PolicyRef), err
	}

	if req.DisableBreakGlass != nil && *req.DisableBreakGlass {
		return failed(accesstoken.PolicyRef), errBreakGlassGone
	}

	d, err := policyDurationsOf(&req)
	if err != nil {
		return failed(accesstoken.PolicyRef), err
	}

	p, err := svc.SetPolicy(r.Context(), e, accesstoken.PolicyChange{
		ExchangeLifetime:  d.exchangeLifetime,
		MaxLifetime:       d.maxLifetime,
		Inactivity:        d.inactivity,
		DisableTokens:     req.DisableTokens,
		DisableAccounts:   req.DisableAccounts,
		DisableBreakGlass: req.DisableBreakGlass,
	})
	if err != nil {
		return failed(accesstoken.PolicyRef), err
	}

	return ok(http.StatusOK, tokenPolicyOf(p), accesstoken.PolicyRef), nil
}

// secondsOf converts a count of seconds of a body to a duration, or answers
// nil for nil. A count below 0 or above the bound of a policy period answers
// errPolicyBody, before the multiplication can overflow.
func secondsOf(seconds *int64) (*time.Duration, error) {
	if seconds == nil {
		return nil, nil //nolint:nilnil // A missing field keeps its value.
	}

	if *seconds < 0 || *seconds > int64(accesstoken.MaxPolicyDuration/time.Second) {
		return nil, errPolicyBody
	}

	d := time.Duration(*seconds) * time.Second

	return &d, nil
}

// sweepInactive serves POST /api/server/token-policy/sweep: it runs the
// inactivity disable once and answers 200 with what it disabled. Each
// disable writes its audit entry.
func sweepInactive(
	_ http.ResponseWriter, r *http.Request, svc *accesstoken.Service, _ identity.Principal, e audit.Entry,
) (principalResult, error) {
	res, err := svc.SweepInactive(r.Context(), e)
	if err != nil {
		return failed(accesstoken.PolicyRef), err
	}

	body := sweepResponse{Tokens: make([]string, 0, len(res.Tokens)), Principals: res.Principals}
	for _, t := range res.Tokens {
		body.Tokens = append(body.Tokens, t.ID)
	}

	if body.Principals == nil {
		body.Principals = []string{}
	}

	return ok(http.StatusOK, body, accesstoken.PolicyRef), nil
}

// policyDurations are the periods of a policy change; nil keeps a period.
type policyDurations struct {
	maxLifetime      *time.Duration
	inactivity       *time.Duration
	exchangeLifetime *time.Duration
}

// policyDurationsOf reads the periods of req, in seconds.
func policyDurationsOf(req *tokenPolicyRequest) (policyDurations, error) {
	var (
		out policyDurations
		err error
	)

	if out.maxLifetime, err = secondsOf(req.MaxLifetimeSeconds); err != nil {
		return policyDurations{maxLifetime: nil, inactivity: nil, exchangeLifetime: nil}, err
	}

	if out.inactivity, err = secondsOf(req.InactivitySeconds); err != nil {
		return policyDurations{maxLifetime: nil, inactivity: nil, exchangeLifetime: nil}, err
	}

	if out.exchangeLifetime, err = secondsOf(req.ExchangeLifetimeSeconds); err != nil {
		return policyDurations{maxLifetime: nil, inactivity: nil, exchangeLifetime: nil}, err
	}

	return out, nil
}
