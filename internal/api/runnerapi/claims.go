package runnerapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// The paths of the apply claim routes. They live under the runner protocol
// prefix, because the callers are runner jobs and the local wrapper, and
// the credential is the job token of the phase: a TFE-compatible request
// names its apply claim through the job token.
const (
	// PathWorkspaceClaims registers and lists the apply claims of one
	// workspace, by its stable workspace ID.
	PathWorkspaceClaims = "/runner/v1/workspaces/{id}/claims"
)

// maxClaimAddresses bounds the address set of one register call. A longer
// set is refused before it reaches the registry, so a forged request cannot
// grow memory with one call.
const maxClaimAddresses = 10_000

// maxClaimAddressLength bounds one address of a register call, in bytes.
const maxClaimAddressLength = 1_024

// claimRoutes returns the apply claim routes. They are registered only when
// the handler has a claim registry; a server without one answers 404 on
// both paths from the mux.
func (h *Handler) claimRoutes() []Route {
	if h.claims == nil {
		return nil
	}

	return []Route{
		{
			Method:  http.MethodPost,
			Path:    PathWorkspaceClaims,
			Scope:   ScopeJobToken,
			Actions: []identity.Action{identity.ActionUnitClaimRegister},
			Serve:   h.registerClaim,
		},
		{
			Method:  http.MethodGet,
			Path:    PathWorkspaceClaims,
			Scope:   ScopeJobToken,
			Actions: []identity.Action{identity.ActionUnitRead},
			Serve:   h.listClaims,
		},
	}
}

// errClaimForbidden reports a caller without the action on the workspace.
// The answer names the missing action on the workspace.
var errClaimForbidden = errors.New("the caller lacks the action on the workspace")

// claimTarget is the workspace of a claims call: its stable ID, its unit
// object for authorization and token binding, and the fence of its
// namespace.
type claimTarget struct {
	id    workspace.ID
	unit  identity.Object
	fence namespace.Fence
}

// errClaimJobMismatch reports a job token used for another job.
var errClaimJobMismatch = errors.New("the job token names another job")

// errClaimUnitMismatch reports a job token used on another unit.
var errClaimUnitMismatch = errors.New("the job token names another unit")

// claimRegisterRequest is the body of a register call. Unknown fields are
// refused, so a misspelled base or address set never passes quietly.
type claimRegisterRequest struct {
	// Base is the state version ID that the plan used.
	Base string `json:"base"`
	// Addresses is the address set of the plan.
	Addresses []string `json:"addresses"`
	// Replace reports that the plan holds a replace action.
	Replace bool `json:"replace,omitempty"`
	// JobID is the job that registers the claim, or empty for the local
	// wrapper, which acts as the person who signed in.
	JobID string `json:"jobID,omitempty"`
	// Trigger names what started the job, or empty.
	Trigger string `json:"trigger,omitempty"`
}

// claimResponse is one claim in an answer.
type claimResponse struct {
	ID          string     `json:"id"`
	WorkspaceID string     `json:"workspaceID"`
	Base        string     `json:"base"`
	Addresses   []string   `json:"addresses"`
	Principal   string     `json:"principal"`
	JobID       string     `json:"jobID,omitempty"`
	Trigger     string     `json:"trigger,omitempty"`
	State       string     `json:"state"`
	StartedAt   time.Time  `json:"startedAt"`
	EndedAt     *time.Time `json:"endedAt,omitempty"`
}

// claimListResponse lists the claims of a workspace in accept order.
type claimListResponse struct {
	Claims []claimResponse `json:"claims"`
}

// registerClaim serves POST on the claims path: a runner job or the local
// wrapper registers its non-replace claim before the apply.
func (h *Handler) registerClaim(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	target, err := h.claimWorkspace(ctx, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	wsID, unit, fence := target.id, target.unit, target.fence

	var req claimRegisterRequest

	if decodeErr := decodeRegisterBody(c.body, &req); decodeErr != nil {
		h.writeError(ctx, w, decodeErr)

		return
	}

	principal, err := h.claimPrincipal(ctx, r, req.JobID, unit)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if authErr := h.authorizer.Authorize(ctx, principal, identity.ActionUnitClaimRegister, unit); authErr != nil {
		h.writeError(ctx, w, fmt.Errorf(
			"%w: unit:claim-register on %s: %w",
			errClaimForbidden,
			unit,
			authErr,
		))

		return
	}

	claim, err := h.claims.Register(ctx, workspace.RegisterClaimRequest{
		WorkspaceID: wsID,
		Base:        workspace.StateVersionID(req.Base),
		Addresses:   req.Addresses,
		Replace:     req.Replace,
		Principal:   principal.Subject,
		JobID:       req.JobID,
		Trigger:     req.Trigger,
	}, fence)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusCreated, newClaimResponse(claim))
}

// decodeRegisterBody decodes a register body and bounds it: an unknown
// field, an oversize set and an oversize address are refused before they
// reach the registry, so a forged request cannot grow memory with one
// call.
func decodeRegisterBody(raw []byte, req *claimRegisterRequest) error {
	if err := decodeBody(raw, req); err != nil {
		return err
	}

	if len(req.Addresses) > maxClaimAddresses {
		return fmt.Errorf(
			"%w: the claim holds %d addresses, at most %d",
			workspace.ErrClaimEmptySet,
			len(req.Addresses),
			maxClaimAddresses,
		)
	}

	for _, a := range req.Addresses {
		if len(a) > maxClaimAddressLength {
			return fmt.Errorf(
				"%w: an address holds more than %d bytes",
				workspace.ErrClaimEmptySet,
				maxClaimAddressLength,
			)
		}
	}

	return nil
}

// listClaims serves GET on the claims path: each claim with its state,
// base, address set, principal, job, start time and end time.
func (h *Handler) listClaims(w http.ResponseWriter, r *http.Request, _ *call) {
	ctx := r.Context()

	target, err := h.claimWorkspace(ctx, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	wsID, unit := target.id, target.unit

	principal, principalErr := h.claimPrincipal(ctx, r, "", unit)
	if principalErr != nil {
		h.writeError(ctx, w, principalErr)

		return
	}

	if authErr := h.authorizer.Authorize(ctx, principal, identity.ActionUnitRead, unit); authErr != nil {
		h.writeError(ctx, w, fmt.Errorf(
			"%w: unit:read on %s: %w",
			errClaimForbidden,
			unit,
			authErr,
		))

		return
	}

	claims := h.claims.List(ctx, wsID)
	out := claimListResponse{Claims: make([]claimResponse, 0, len(claims))}

	for i := range claims {
		out.Claims = append(out.Claims, newClaimResponse(claims[i]))
	}

	h.writeJSON(ctx, w, http.StatusOK, out)
}

// claimWorkspace resolves the workspace of a claims call: its stable ID from
// the path, its unit object for authorization and token binding, and the
// fence of its namespace.
func (h *Handler) claimWorkspace(
	ctx context.Context, r *http.Request,
) (claimTarget, error) {
	wsID := workspace.ID(r.PathValue("id"))

	if wsID == "" {
		return claimTarget{}, fmt.Errorf(
			"%w: no workspace ID",
			workspace.ErrClaimNotFound,
		)
	}

	got, err := h.claims.Workspace(ctx, wsID)
	if err != nil {
		return claimTarget{}, err
	}

	unit, err := identity.UnitObject("opentofu", got.Ref.Namespace, got.Ref.Name.String())
	if err != nil {
		return claimTarget{}, fmt.Errorf("unit of workspace %s: %w", wsID, err)
	}

	ns, err := h.namespaces.Get(ctx, got.Ref.Namespace)
	if err != nil {
		return claimTarget{}, fmt.Errorf(
			"%w: namespace of workspace %s",
			workspace.ErrClaimNotFound,
			wsID,
		)
	}

	return claimTarget{id: wsID, unit: unit, fence: ns.Fence()}, nil
}

// claimPrincipal authenticates the credential of a claims call. A bearer
// secret goes to the job token service: the token is bound to its job and
// its apply claim, so a request with the token of another job, or with the
// token of an ended claim, is refused. A token secret goes to the access
// token authenticator, for the local wrapper, which acts as the person who
// signed in and registers no job ID.
//
// wantJob is the job of a register call, or empty for a list call, which
// takes either credential.
func (h *Handler) claimPrincipal(
	ctx context.Context, r *http.Request, wantJob string, unit identity.Object,
) (identity.Principal, error) {
	raw := r.Header.Get("Authorization")

	if secret, ok := strings.CutPrefix(raw, "Bearer "); ok && secret != "" {
		return h.jobTokenPrincipal(ctx, secret, wantJob, unit)
	}

	if secret, ok := strings.CutPrefix(raw, "token "); ok && secret != "" {
		if h.authenticator == nil {
			return identity.Principal{}, fmt.Errorf(
				"%w: the server has no access token authenticator",
				identity.ErrUnauthenticated,
			)
		}

		principal, err := h.authenticator.AuthenticateToken(ctx, secret)
		if err != nil {
			return identity.Principal{}, err
		}

		if wantJob != "" {
			return identity.Principal{}, fmt.Errorf(
				"%w: an access token registers no job claim",
				errClaimJobMismatch,
			)
		}

		return principal, nil
	}

	return identity.Principal{}, fmt.Errorf(
		"%w: the request names no bearer job token or access token",
		identity.ErrUnauthenticated,
	)
}

// jobTokenPrincipal authenticates a bearer job token and binds it to the
// call: the token names the job of a register call and the unit of the
// workspace. A token of another job, of another unit, or of an ended
// claim is refused.
func (h *Handler) jobTokenPrincipal(
	ctx context.Context, secret, wantJob string, unit identity.Object,
) (identity.Principal, error) {
	if h.tokens == nil {
		return identity.Principal{}, fmt.Errorf(
			"%w: the server issues no job tokens",
			identity.ErrUnauthenticated,
		)
	}

	principal, tokenUnit, err := h.tokens.Authenticate(ctx, secret)
	if err != nil {
		return identity.Principal{}, err
	}

	// Defense in depth: the token service binds the token to its job and
	// unit; these checks stay so that a new caller cannot use a token of
	// another job or unit on this route.
	if wantJob != "" && principal.JobID != wantJob {
		return identity.Principal{}, fmt.Errorf(
			"%w: the token names job %q",
			errClaimJobMismatch,
			principal.JobID,
		)
	}

	if tokenUnit.Tool() != unit.Tool() || tokenUnit.Namespace() != unit.Namespace() ||
		tokenUnit.UnitName() != unit.UnitName() {
		return identity.Principal{}, fmt.Errorf(
			"%w: the token names unit %s",
			errClaimUnitMismatch,
			tokenUnit,
		)
	}

	return principal, nil
}

// newClaimResponse renders a stored claim for the wire.
func newClaimResponse(claim workspace.Claim) claimResponse {
	out := claimResponse{
		ID:          claim.ID,
		WorkspaceID: string(claim.WorkspaceID),
		Base:        string(claim.Base),
		Addresses:   append([]string(nil), claim.Addresses...),
		Principal:   claim.Principal,
		JobID:       claim.JobID,
		Trigger:     claim.Trigger,
		State:       string(claim.State),
		StartedAt:   claim.StartedAt,
		EndedAt:     nil,
	}

	if !claim.EndedAt.IsZero() {
		ended := claim.EndedAt

		out.EndedAt = &ended
	}

	return out
}
