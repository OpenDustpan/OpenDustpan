package runnerapi

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
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
	// PathWorkspaceClaimRenew renews the lease of one claim of the local
	// wrapper, by its stable claim ID.
	PathWorkspaceClaimRenew = "/runner/v1/workspaces/{id}/claims/{claimID}/renew"
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
		{
			Method:  http.MethodPost,
			Path:    PathWorkspaceClaimRenew,
			Scope:   ScopeJobToken,
			Actions: []identity.Action{identity.ActionUnitClaimRegister},
			Serve:   h.renewClaim,
		},
	}
}

// claimTarget is the workspace of a claims call: its stable ID, its unit
// object for authorization and token binding, and the fence of its
// namespace.
type claimTarget struct {
	id    workspace.ID
	unit  identity.Object
	fence namespace.Fence
	// deleted is the error of a soft-deleted workspace, or nil. The handler
	// answers it after the check of the action.
	deleted error
}

// errClaimJobMismatch reports a job token used for another job.
var errClaimJobMismatch = errors.New("the job token names another job")

// errClaimUnitMismatch reports a job token used on another unit.
var errClaimUnitMismatch = errors.New("the job token names another unit")

// errClaimTooLarge reports a register body whose address set is too large
// for one call: more than maxClaimAddresses addresses, or an address of
// more than maxClaimAddressLength bytes. It has its own error so the
// answer names the bound instead of reporting an empty set.
var errClaimTooLarge = errors.New("the claim address set is too large")

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
	// LeaseEndsAt is when the lease of a claim with no job ends. A claim of a
	// job has none.
	LeaseEndsAt *time.Time `json:"leaseEndsAt,omitempty"`
	// RenewedAt is when the wrapper last renewed the lease. A claim that no
	// one renewed has none.
	RenewedAt *time.Time `json:"renewedAt,omitempty"`
}

// claimListResponse lists the claims of a workspace in accept order.
type claimListResponse struct {
	Claims []claimResponse `json:"claims"`
}

// registerClaim serves POST on the claims path: a runner job or the local
// wrapper registers its non-replace claim before the apply.
func (h *Handler) registerClaim(w http.ResponseWriter, r *http.Request, c *call) {
	ctx := r.Context()

	// The credential comes first: a caller with no valid credential learns
	// nothing about the workspace, not even whether it exists.
	caller, err := h.authenticateClaimCaller(ctx, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	var req claimRegisterRequest

	if decodeErr := decodeRegisterBody(c.body, &req); decodeErr != nil {
		h.writeError(ctx, w, decodeErr)

		return
	}

	target, err := h.claimWorkspace(ctx, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if bindErr := caller.bindList(target); bindErr != nil {
		h.writeError(ctx, w, bindErr)

		return
	}

	wsID, unit, fence := target.id, target.unit, target.fence

	if authErr := h.authorizeClaim(ctx, &caller, identity.ActionUnitClaimRegister, &target); authErr != nil {
		h.writeError(ctx, w, authErr)

		return
	}

	// The job comes after the action: a caller without the action learns
	// nothing about the workspace from a refusal of its job.
	if jobErr := caller.bindJob(req.JobID); jobErr != nil {
		h.writeError(ctx, w, jobErr)

		return
	}

	claim, err := h.claims.Register(ctx, &workspace.RegisterClaimRequest{
		WorkspaceID: wsID,
		Base:        workspace.StateVersionID(req.Base),
		Addresses:   req.Addresses,
		Replace:     req.Replace,
		Principal:   caller.principal.Subject,
		JobID:       req.JobID,
		Trigger:     req.Trigger,
		ViaJobToken: caller.viaJobToken,
		Audit:       h.claimEntry(r, fence.Namespace, caller.principal, unit),
	}, fence)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusCreated, newClaimResponse(claim))
}

// renewClaim serves POST on the renew path: the local wrapper extends the
// lease of its claim. The guards run in this order: the credential (401), the
// workspace (404, 410), the action (404, as for a workspace that does not exist), then the claim of the principal
// (404) and its state (409). A job token is refused with the answer for a
// claim that is not known: the claim of a job has no lease and ends with its
// job.
func (h *Handler) renewClaim(w http.ResponseWriter, r *http.Request, _ *call) {
	ctx := r.Context()

	caller, err := h.authenticateClaimCaller(ctx, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	target, err := h.claimWorkspace(ctx, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// Defense in depth: the registry also refuses a claim of a job, and the
	// principal check of the store also refuses a job token. This check stays
	// so that a job token never reaches the registry on this route.
	if caller.viaJobToken {
		h.writeError(ctx, w, fmt.Errorf("%w: a job token renews no claim", workspace.ErrClaimNotFound))

		return
	}

	if authErr := h.authorizeClaim(ctx, &caller, identity.ActionUnitClaimRegister, &target); authErr != nil {
		h.writeError(ctx, w, authErr)

		return
	}

	claim, err := h.claims.Renew(ctx, target.id, r.PathValue("claimID"), caller.principal.Subject, target.fence)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, newClaimResponse(claim))
}

// decodeRegisterBody decodes a register body and bounds it: an unknown
// field, an oversize set and an oversize address are refused before they
// reach the registry, so a forged request cannot grow memory with one
// call.
//
// Defense in depth: the registry and the stores check the same bounds. These
// checks stay so that the handler refuses a hostile body with its own
// message before it builds a request, and they do not rely on the other
// layers.
func decodeRegisterBody(raw []byte, req *claimRegisterRequest) error {
	if err := decodeBody(raw, req); err != nil {
		return err
	}

	if len(req.Addresses) > maxClaimAddresses {
		return fmt.Errorf(
			"%w: the claim holds %d addresses, at most %d",
			errClaimTooLarge,
			len(req.Addresses),
			maxClaimAddresses,
		)
	}

	for _, a := range req.Addresses {
		if len(a) > maxClaimAddressLength {
			return fmt.Errorf(
				"%w: an address holds more than %d bytes",
				errClaimTooLarge,
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

	caller, err := h.authenticateClaimCaller(ctx, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	target, err := h.claimWorkspace(ctx, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if bindErr := caller.bindList(target); bindErr != nil {
		h.writeError(ctx, w, bindErr)

		return
	}

	wsID := target.id

	if authErr := h.authorizeClaim(ctx, &caller, identity.ActionUnitRead, &target); authErr != nil {
		h.writeError(ctx, w, authErr)

		return
	}

	claims, err := h.claims.List(ctx, wsID)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

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

	// A soft-deleted workspace comes back with its reference and an error that
	// matches ErrClaimDeleted. The caller checks the right of its own caller
	// first and answers 410 after that, so that a caller without the right gets
	// the same 404 for a deleted and for a missing workspace.
	got, err := h.claims.Workspace(ctx, wsID)

	var deletedErr error

	switch {
	case errors.Is(err, workspace.ErrClaimDeleted):
		deletedErr = err
	case err != nil:
		return claimTarget{}, err
	default:
	}

	unit, err := identity.UnitObject("opentofu", got.Ref.Namespace, got.Ref.Name.String())
	if err != nil {
		return claimTarget{}, fmt.Errorf("unit of workspace %s: %w", wsID, err)
	}

	// The object names the stable ID of the workspace, so a grant by ID and
	// the binding of a job token compare the same value.
	unit = unit.WithUnitID(string(got.ID))

	ns, err := h.namespaces.Get(ctx, got.Ref.Namespace)
	if err != nil {
		return claimTarget{}, fmt.Errorf(
			"%w: namespace of workspace %s",
			workspace.ErrClaimNotFound,
			wsID,
		)
	}

	return claimTarget{id: wsID, unit: unit, fence: ns.Fence(), deleted: deletedErr}, nil
}

// claimCaller is a caller whose credential the handler authenticated.
type claimCaller struct {
	principal identity.Principal
	// viaJobToken is true for a bearer job token, false for an access token.
	viaJobToken bool
	// tokenUnit is the unit that a job token names. It is zero for an
	// access token.
	tokenUnit identity.Object
}

// authenticateClaimCaller authenticates the credential of a claims call and
// nothing else: it reads no workspace. A bearer secret goes to the job token
// service. A token secret goes to the access token authenticator, for the
// local wrapper, which acts as the person who signed in and registers no job
// ID.
func (h *Handler) authenticateClaimCaller(ctx context.Context, r *http.Request) (claimCaller, error) {
	raw := r.Header.Get("Authorization")

	if secret, ok := strings.CutPrefix(raw, "Bearer "); ok && secret != "" {
		return h.authenticateJobToken(ctx, secret)
	}

	if secret, ok := strings.CutPrefix(raw, "token "); ok && secret != "" {
		if h.authenticator == nil {
			return claimCaller{}, fmt.Errorf(
				"%w: the server has no access token authenticator",
				identity.ErrUnauthenticated,
			)
		}

		principal, err := h.authenticator.AuthenticateToken(ctx, secret)
		if err != nil {
			return claimCaller{}, err
		}

		return claimCaller{principal: principal, viaJobToken: false, tokenUnit: identity.Object{}}, nil
	}

	return claimCaller{}, fmt.Errorf(
		"%w: the request names no bearer job token or access token",
		identity.ErrUnauthenticated,
	)
}

// authenticateJobToken authenticates a bearer job token. The token service
// binds the token to its job and its apply claim: the token of an ended
// claim is refused.
func (h *Handler) authenticateJobToken(ctx context.Context, secret string) (claimCaller, error) {
	if h.tokens == nil {
		return claimCaller{}, fmt.Errorf(
			"%w: the server issues no job tokens",
			identity.ErrUnauthenticated,
		)
	}

	principal, tokenUnit, err := h.tokens.Authenticate(ctx, secret)
	if err != nil {
		return claimCaller{}, err
	}

	return claimCaller{principal: principal, viaJobToken: true, tokenUnit: tokenUnit}, nil
}

// bindList checks the caller of a list call against the workspace: a job
// token must name the stable ID of the workspace, and a token of another
// workspace answers as a workspace that does not exist.
//
// Defense in depth: the token service binds the token to its job and unit.
// This check stays so that a new caller cannot use a token on another
// workspace, and it does not rely on the other layers.
func (c claimCaller) bindList(target claimTarget) error {
	if c.viaJobToken && c.tokenUnit.UnitID() != string(target.id) {
		return fmt.Errorf("%w: %w", workspace.ErrClaimNotFound, errClaimUnitMismatch)
	}

	return nil
}

// bindJob checks the job of a register call against the caller. A job token
// must name its own job: an empty or another job is refused, so no claim is
// stored without a job that ends it. An access token registers no job claim.
// The caller has passed bindList and the check of the action first.
//
// Defense in depth: the token service binds the token to its job and unit,
// and the registry refuses a job-token claim with no job. These checks stay
// so that a new caller cannot use a token on another job, and they do not rely
// on the other layers.
func (c claimCaller) bindJob(jobID string) error {
	if !c.viaJobToken {
		if jobID != "" {
			return fmt.Errorf("%w: an access token registers no job claim", errClaimJobMismatch)
		}

		return nil
	}

	if jobID == "" || c.principal.JobID != jobID {
		return fmt.Errorf("%w: the token names job %q", errClaimJobMismatch, c.principal.JobID)
	}

	return nil
}

// authorizeClaim checks that the caller may do action on the unit of the
// target, and then answers for a soft-deleted workspace. A caller that lacks the
// action gets the answer for a workspace that does not exist (404), whether the
// workspace is missing, deleted or live: a 403 would tell a caller with a valid
// credential that the workspace exists, and a 410 would tell it that the
// workspace existed. Only a caller with the action learns that the workspace is
// gone (410). A failure of the authorizer is not a denial and is not hidden.
func (h *Handler) authorizeClaim(
	ctx context.Context, caller *claimCaller, action identity.Action, target *claimTarget,
) error {
	err := h.authorizer.Authorize(ctx, caller.principal, action, target.unit)

	switch {
	case errors.Is(err, identity.ErrDenied):
		return fmt.Errorf("%w: the caller lacks %s on the workspace", workspace.ErrClaimNotFound, action)
	case err != nil:
		return fmt.Errorf("authorize %s: %w", action, err)
	default:
		return target.deleted
	}
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
		LeaseEndsAt: nil,
		RenewedAt:   nil,
	}

	if !claim.EndedAt.IsZero() {
		ended := claim.EndedAt

		out.EndedAt = &ended
	}

	if !claim.LeaseEndsAt.IsZero() {
		lease := claim.LeaseEndsAt

		out.LeaseEndsAt = &lease
	}

	if !claim.RenewedAt.IsZero() {
		renewed := claim.RenewedAt

		out.RenewedAt = &renewed
	}

	return out
}

// claimEntry returns the audit entry of an accepted claim: who registered it
// and on which unit. The registry sets the time, the claim ID and the job ID.
// The entry holds IDs and names, never a token.
func (h *Handler) claimEntry(
	r *http.Request, ns namespace.Name, principal identity.Principal, unit identity.Object,
) audit.Entry {
	e := h.entry(r, ns, principal.Subject, runner.AuditRule{
		Action: identity.ActionUnitClaimRegister, Kind: audit.KindChange,
	}, h.now())
	e.Object = unit.String()
	e.TokenID = principal.TokenID

	return e
}
