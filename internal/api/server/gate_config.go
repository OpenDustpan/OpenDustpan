package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
)

// GateConfig is the gate configuration that the installation API serves:
// the gates, the access limits, the approval defaults and the approval rules
// of a scope. gate.ConfigService is the implementation. It is defined here,
// in the package that consumes it.
type GateConfig interface {
	ApprovalConfig
	GateObjects
	AccessLimitObjects
}

// ApprovalConfig is the part of GateConfig for the approval defaults and the
// approval rules of a namespace.
type ApprovalConfig interface {
	// ApprovalDefault returns the approval default of scope, or gate.ErrNotFound.
	ApprovalDefault(ctx context.Context, scope gate.Scope) (gate.ApprovalDefault, error)
	// PutApprovalDefault stores d, and returns it with its new version.
	PutApprovalDefault(
		ctx context.Context,
		d gate.ApprovalDefault,
		prev int64,
		e audit.Entry,
	) (gate.ApprovalDefault, error)
	// DeleteApprovalDefault removes the approval default of scope at version prev.
	DeleteApprovalDefault(ctx context.Context, scope gate.Scope, prev int64, e audit.Entry) error
	// ApprovalRules returns the approval rules stored at scope, ordered by ID.
	ApprovalRules(ctx context.Context, scope gate.Scope) ([]gate.ApprovalRule, error)
	// ApprovalRule returns one approval rule of scope, or gate.ErrNotFound.
	ApprovalRule(ctx context.Context, scope gate.Scope, id gate.ID) (gate.ApprovalRule, error)
	// PutApprovalRule stores r, and returns it with its new version.
	PutApprovalRule(ctx context.Context, r gate.ApprovalRule, prev int64, e audit.Entry) (gate.ApprovalRule, error)
	// DeleteApprovalRule removes the approval rule ref at its version.
	DeleteApprovalRule(ctx context.Context, ref gate.ObjectRef, e audit.Entry) error
}

// GateObjects is the part of GateConfig for the gates of a scope.
type GateObjects interface {
	// Gates returns the gates stored at scope, ordered by ID.
	Gates(ctx context.Context, scope gate.Scope) ([]gate.Gate, error)
	// Gate returns one gate of scope, or gate.ErrNotFound.
	Gate(ctx context.Context, scope gate.Scope, id gate.ID) (gate.Gate, error)
	// PutGate checks and stores g, and returns it with its new version.
	// reason is the reason of a change that makes a final gate weaker.
	PutGate(ctx context.Context, g gate.Gate, prev int64, reason string, e audit.Entry) (gate.Gate, error)
	// DeleteGate removes the gate ref at its version.
	DeleteGate(ctx context.Context, ref gate.ObjectRef, reason string, e audit.Entry) error
}

// AccessLimitObjects is the part of GateConfig for the access limits of a scope.
type AccessLimitObjects interface {
	// AccessLimits returns the access limits stored at scope, ordered by ID.
	AccessLimits(ctx context.Context, scope gate.Scope) ([]gate.AccessLimit, error)
	// AccessLimit returns one access limit of scope, or gate.ErrNotFound.
	AccessLimit(ctx context.Context, scope gate.Scope, id gate.ID) (gate.AccessLimit, error)
	// PutAccessLimit checks and stores l, and returns it with its new version.
	PutAccessLimit(ctx context.Context, l gate.AccessLimit, prev int64, e audit.Entry) (gate.AccessLimit, error)
	// DeleteAccessLimit removes the access limit ref at its version.
	DeleteAccessLimit(ctx context.Context, ref gate.ObjectRef, e audit.Entry) error
}

// maxGateConfigBodyBytes bounds the body of an approval default or an
// approval rule. The approver lists are small, so this is ample.
const maxGateConfigBodyBytes = 64 * 1024

// The routes of the approval defaults and the approval rules of a namespace.
const (
	approvalDefaultPath   = "/api/dustpan/v1/namespaces/{namespace}/approval-default"
	approvalRulesPath     = "/api/dustpan/v1/namespaces/{namespace}/approval-rules"
	approvalRuleItemPath  = "/api/dustpan/v1/namespaces/{namespace}/approval-rules/{id}"
	gateConfigNotFoundMsg = "no such gate, access limit, approval rule or approval default"
	// gateUnitNotFoundMsg is the answer to a write for a name that no live
	// stack or workspace has in the namespace.
	gateUnitNotFoundMsg = "no stack or workspace has this name in the namespace: create it first"
)

// errIfMatchInvalid answers an If-Match header that is not one version in
// quotes. The header is part of the request, so the answer has its own code.
var errIfMatchInvalid = &codedError{
	status: http.StatusBadRequest, code: errcode.RequestIfMatchInvalid, args: nil,
	message: `If-Match must be one stored version in quotes, for example "1"`,
}

// approversBody is the approvers of a default or a rule on the wire.
//
// Contract: never remove or rename these fields. The console reads only this
// API, and it updates on its own schedule.
type approversBody struct {
	Groups     []string `json:"groups"`
	Users      []string `json:"users"`
	UnitOwners bool     `json:"unitOwners"`
}

// approvalDefaultBody is an approval default on the wire. Version is set on
// an answer and ignored on a request: the If-Match header carries the
// expected version.
type approvalDefaultBody struct {
	Count            int           `json:"count"`
	Approvers        approversBody `json:"approvers"`
	FreshAuthSeconds *int          `json:"freshAuthSeconds"`
	Version          int64         `json:"version,omitempty"`
}

// approvalRuleBody is an approval rule on the wire.
type approvalRuleBody struct {
	ID               string        `json:"id,omitempty"`
	Count            int           `json:"count"`
	Approvers        approversBody `json:"approvers"`
	AuthorMayApprove bool          `json:"authorMayApprove"`
	FreshAuthSeconds *int          `json:"freshAuthSeconds"`
	TimeoutSeconds   *int64        `json:"timeoutSeconds"`
	Version          int64         `json:"version,omitempty"`
}

// approvalRuleListBody is the answer of the list route.
type approvalRuleListBody struct {
	Rules []approvalRuleBody `json:"rules"`
}

func (b approversBody) domain() gate.Approvers {
	return gate.Approvers{Groups: nonNil(b.Groups), GroupIDs: nil, Users: nonNil(b.Users), UnitOwners: b.UnitOwners}
}

func approversOf(a gate.Approvers) approversBody {
	return approversBody{Groups: nonNil(a.Groups), Users: nonNil(a.Users), UnitOwners: a.UnitOwners}
}

// nonNil returns s, or an empty list for nil, so the JSON form is [] and never null.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}

	return s
}

// freshOf reads the optional fresh authentication window of a request.
func freshOf(seconds *int) (gate.FreshAuth, error) {
	if seconds == nil {
		return gate.NoFreshAuth(), nil
	}

	return gate.ParseFreshAuth(*seconds)
}

// freshBody returns the wire form of f: null for none, else the seconds.
func freshBody(f gate.FreshAuth) *int {
	if !f.Set {
		return nil
	}

	seconds := f.Seconds

	return &seconds
}

func approvalDefaultOf(d gate.ApprovalDefault) approvalDefaultBody {
	return approvalDefaultBody{
		Count: d.Count, Approvers: approversOf(d.Approvers), FreshAuthSeconds: freshBody(d.FreshAuth),
		Version: d.Version,
	}
}

func approvalRuleOf(r gate.ApprovalRule) approvalRuleBody {
	var timeout *int64

	if r.Timeout != 0 {
		seconds := int64(r.Timeout / time.Second)
		timeout = &seconds
	}

	return approvalRuleBody{
		ID: string(r.ID), Count: r.Count, Approvers: approversOf(r.Approvers),
		AuthorMayApprove: r.AuthorMayApprove, FreshAuthSeconds: freshBody(r.FreshAuth), TimeoutSeconds: timeout,
		Version: r.Version,
	}
}

// decodeGateBody reads one JSON object from r into v. It refuses an unknown
// field, trailing data, and a body above maxGateConfigBodyBytes.
func decodeGateBody(w http.ResponseWriter, r *http.Request, v any) error {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxGateConfigBodyBytes))
	if err != nil {
		return errInvalidBody
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(v); err != nil {
		return errInvalidBody
	}

	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errInvalidBody
	}

	return nil
}

// ifMatch returns the version of the If-Match header, and whether the
// request sent one. The header holds one quoted version, as the ETag of a
// read gives it. A value that is not one version is errIfMatchInvalid.
func ifMatch(r *http.Request) (int64, bool, error) {
	raw := apidesc.Header(r, "If-Match")
	if raw == "" {
		return 0, false, nil
	}

	v, err := strconv.ParseInt(strings.Trim(raw, `"`), 10, 64)
	if err != nil || v < 1 {
		return 0, false, errIfMatchInvalid
	}

	return v, true, nil
}

func etag(version int64) string {
	return `"` + strconv.FormatInt(version, 10) + `"`
}

// gateEntry returns the audit entry of the caller p for a gate configuration
// write. The service sets the namespace, the time, the object and the action.
func gateEntry(r *http.Request, p identity.Principal) audit.Entry {
	entry := callerEntry(r, p, identity.ActionNamespaceManageGates)
	entry.Kind = audit.KindChange
	entry.Outcome = audit.OutcomeSuccess

	return entry
}

// gateConfigRoutes returns the rows of the approval defaults and the
// approval rules of a namespace, and of the gates and the access limits of a
// namespace, of one of its collections and of one of its units. Nil
// GateConfig means the store keeps no gate configuration: no route is
// mounted. A read needs namespace:read-gates and a write needs
// namespace:manage-gates, both on the namespace of the path, also for a
// collection or a unit of it. A write also needs a fresh sign-in of a
// console session.
func (h *Handler) gateConfigRoutes() []Route {
	if h.gates == nil {
		return nil
	}

	read := []identity.Action{identity.ActionNamespaceReadGates}
	manage := []identity.Action{identity.ActionNamespaceManageGates}

	row := func(method, path string, actions []identity.Action, serve RouteHandler) Route {
		return Route{
			Method: method, Path: path, Scope: ScopeAuthorized, Actions: actions,
			Resolve: resolveNamespacePath, ActionsFor: nil, OpaqueDenial: false, Serve: serve,
		}
	}

	routes := h.gateObjectRoutes(row, read, manage)

	return append(routes, []Route{
		row(http.MethodGet, approvalDefaultPath, read, h.readApprovalDefault),
		row(
			http.MethodPut,
			approvalDefaultPath,
			manage,
			h.fresh(identity.ActionNamespaceManageGates, h.writeApprovalDefault),
		),
		row(
			http.MethodDelete,
			approvalDefaultPath,
			manage,
			h.fresh(identity.ActionNamespaceManageGates, h.removeApprovalDefault),
		),
		row(http.MethodGet, approvalRulesPath, read, h.listApprovalRules),
		row(http.MethodGet, approvalRuleItemPath, read, h.readApprovalRule),
		row(
			http.MethodPut,
			approvalRuleItemPath,
			manage,
			h.fresh(identity.ActionNamespaceManageGates, h.writeApprovalRule),
		),
		row(
			http.MethodDelete,
			approvalRuleItemPath,
			manage,
			h.fresh(identity.ActionNamespaceManageGates, h.removeApprovalRule),
		),
	}...)
}

// readApprovalDefault serves GET /api/dustpan/v1/namespaces/{namespace}/approval-default.
func (h *Handler) readApprovalDefault(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	d, err := h.gates.ApprovalDefault(ctx, gate.NamespaceScope(obj.Namespace()))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.Header().Set("ETag", etag(d.Version))
	h.writeJSON(ctx, w, http.StatusOK, approvalDefaultOf(d))
}

// writeApprovalDefault serves PUT /api/dustpan/v1/namespaces/{namespace}/approval-default.
// It answers 200 with the stored default. With If-Match, the write
// replaces that version only. Without it, the write replaces the stored
// default, or creates it.
func (h *Handler) writeApprovalDefault(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	var in approvalDefaultBody
	if err := decodeGateBody(w, r, &in); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	fresh, err := freshOf(in.FreshAuthSeconds)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	scope := gate.NamespaceScope(obj.Namespace())

	prev, err := h.expectedVersion(r, func() (int64, error) {
		d, getErr := h.gates.ApprovalDefault(ctx, scope)

		return d.Version, getErr
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	d := gate.ApprovalDefault{
		Scope: scope, Version: 0, Count: in.Count, FreshAuth: fresh, Approvers: in.Approvers.domain(),
		Unless: "", Reads: nil,
	}

	stored, err := h.gates.PutApprovalDefault(ctx, d, prev, gateEntry(r, p))
	if err != nil {
		h.writeError(ctx, w, h.approversAnswer(ctx, p, d.Approvers, err))

		return
	}

	w.Header().Set("ETag", etag(stored.Version))
	h.writeJSON(ctx, w, http.StatusOK, approvalDefaultOf(stored))
}

// removeApprovalDefault serves DELETE /api/dustpan/v1/namespaces/{namespace}/approval-default.
// It answers 204, also when the namespace sets no default. With If-Match, it
// removes that version only.
func (h *Handler) removeApprovalDefault(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()
	scope := gate.NamespaceScope(obj.Namespace())

	prev, err := h.existingVersion(r, func() (int64, error) {
		d, getErr := h.gates.ApprovalDefault(ctx, scope)

		return d.Version, getErr
	})
	if errors.Is(err, gate.ErrNotFound) {
		w.WriteHeader(http.StatusNoContent)

		return
	}

	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	err = h.gates.DeleteApprovalDefault(ctx, scope, prev, gateEntry(r, p))
	if errors.Is(err, gate.ErrNotFound) {
		// The default went away between the read and the delete. The goal of
		// the request holds, so the answer is the same as for no default.
		w.WriteHeader(http.StatusNoContent)

		return
	}

	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// listApprovalRules serves GET /api/dustpan/v1/namespaces/{namespace}/approval-rules.
// It lists the rules stored at the namespace, not the rules of a wider scope.
func (h *Handler) listApprovalRules(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	rules, err := h.gates.ApprovalRules(ctx, gate.NamespaceScope(obj.Namespace()))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	out := approvalRuleListBody{Rules: make([]approvalRuleBody, 0, len(rules))}
	for i := range rules {
		out.Rules = append(out.Rules, approvalRuleOf(rules[i]))
	}

	h.writeJSON(ctx, w, http.StatusOK, out)
}

// ruleIDOf reads the rule ID of the path. An ID that does not match the grammar
// answers 422.
func ruleIDOf(r *http.Request) (gate.ID, error) {
	return gate.ParseRuleID(r.PathValue("id"))
}

// readApprovalRule serves GET /api/dustpan/v1/namespaces/{namespace}/approval-rules/{id}.
func (h *Handler) readApprovalRule(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	id, err := ruleIDOf(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	rule, err := h.gates.ApprovalRule(ctx, gate.NamespaceScope(obj.Namespace()), id)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.Header().Set("ETag", etag(rule.Version))
	h.writeJSON(ctx, w, http.StatusOK, approvalRuleOf(rule))
}

// writeApprovalRule serves PUT /api/dustpan/v1/namespaces/{namespace}/approval-rules/{id}.
// The version rules follow writeApprovalDefault. The body names no ID: the
// path names it.
func (h *Handler) writeApprovalRule(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	id, err := ruleIDOf(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	var in approvalRuleBody
	if decodeErr := decodeGateBody(w, r, &in); decodeErr != nil {
		h.writeError(ctx, w, decodeErr)

		return
	}

	if in.ID != "" {
		h.writeError(ctx, w, errInvalidBody)

		return
	}

	rule, err := approvalRuleFrom(gate.NamespaceScope(obj.Namespace()), id, in)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	prev, err := h.expectedVersion(r, func() (int64, error) {
		cur, getErr := h.gates.ApprovalRule(ctx, rule.Scope, id)

		return cur.Version, getErr
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	stored, err := h.gates.PutApprovalRule(ctx, rule, prev, gateEntry(r, p))
	if err != nil {
		h.writeError(ctx, w, h.approversAnswer(ctx, p, rule.Approvers, err))

		return
	}

	w.Header().Set("ETag", etag(stored.Version))
	h.writeJSON(ctx, w, http.StatusOK, approvalRuleOf(stored))
}

// approvalRuleFrom builds the rule of a request body.
func approvalRuleFrom(scope gate.Scope, id gate.ID, in approvalRuleBody) (gate.ApprovalRule, error) {
	fresh, err := freshOf(in.FreshAuthSeconds)
	if err != nil {
		return gate.ApprovalRule{}, err
	}

	var timeout time.Duration

	if in.TimeoutSeconds != nil {
		timeout = time.Duration(*in.TimeoutSeconds) * time.Second
		if timeout < gate.MinApprovalTimeout || timeout > gate.MaxApprovalTimeout {
			return gate.ApprovalRule{}, fmt.Errorf(
				"%w: timeoutSeconds %d: give 3600 to 2592000",
				gate.ErrInvalid,
				*in.TimeoutSeconds,
			)
		}
	}

	return gate.ApprovalRule{
		Scope: scope, ID: id, Version: 0, Approvers: in.Approvers.domain(), Count: in.Count,
		AuthorMayApprove: in.AuthorMayApprove, FreshAuth: fresh, Timeout: timeout,
	}, nil
}

// removeApprovalRule serves DELETE /api/dustpan/v1/namespaces/{namespace}/approval-rules/{id}.
// It answers 204. A rule that a stored gate names answers 422.
func (h *Handler) removeApprovalRule(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	id, err := ruleIDOf(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	scope := gate.NamespaceScope(obj.Namespace())

	version, err := h.existingVersion(r, func() (int64, error) {
		cur, getErr := h.gates.ApprovalRule(ctx, scope, id)

		return cur.Version, getErr
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	ref := gate.ObjectRef{Scope: scope, ID: id, Version: version}
	if err := h.gates.DeleteApprovalRule(ctx, ref, gateEntry(r, p)); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// expectedVersion returns the version that a put replaces: the If-Match
// version, else the stored version, else 0 for an object that does not exist.
func (h *Handler) expectedVersion(r *http.Request, stored func() (int64, error)) (int64, error) {
	v, sent, err := ifMatch(r)
	if err != nil || sent {
		return v, err
	}

	cur, err := stored()

	switch {
	case err == nil:
		return cur, nil
	case errors.Is(err, gate.ErrNotFound):
		return 0, nil
	default:
		return 0, err
	}
}

// existingVersion returns the version that a delete removes: the If-Match
// version, else the stored version. A missing object returns gate.ErrNotFound.
func (h *Handler) existingVersion(r *http.Request, stored func() (int64, error)) (int64, error) {
	v, sent, err := ifMatch(r)
	if err != nil || sent {
		return v, err
	}

	return stored()
}

// classifyGateConfig maps the gate configuration errors to their status and
// message. The domain text of an invalid or conflicting write names the
// field or the object and holds no driver detail. A stale fence is a
// namespace error, which classifyNamespace maps.
func classifyGateConfig(err error) (failure, bool) {
	switch {
	case errors.Is(err, gate.ErrUnitNotFound):
		return failCoded(http.StatusNotFound, gateUnitNotFoundMsg, errcode.UnitNotFound), true
	case errors.Is(err, gate.ErrNotFound):
		return failCoded(http.StatusNotFound, gateConfigNotFoundMsg, errcode.GateNotFound), true
	case errors.Is(err, gate.ErrConflict):
		return failCoded(http.StatusConflict, "the stored version changed: read it again and retry",
			errcode.GateVersionConflict), true
	case errors.Is(err, gate.ErrFinal):
		return failCoded(http.StatusConflict, err.Error(), errcode.GateFinal), true
	case errors.Is(err, errApproversUnmet):
		return failCoded(http.StatusUnprocessableEntity, errApproversUnmet.Error(), errcode.GateApproversUnmet), true
	case errors.Is(err, gate.ErrApprovers):
		return classifyApprovers(err)
	case errors.Is(err, gate.ErrLimit):
		return failCoded(http.StatusUnprocessableEntity, err.Error(), errcode.GateLimit), true
	case errors.Is(err, gate.ErrLimitMembership):
		return failCoded(http.StatusBadRequest, gate.ErrLimitMembership.Error(), errcode.GateLimitMembership), true
	case errors.Is(err, gate.ErrInvalid):
		return failCoded(http.StatusUnprocessableEntity, err.Error(), errcode.GateInvalid), true
	case errors.Is(err, gate.ErrUnavailable):
		return failCoded(http.StatusServiceUnavailable, "the gate configuration is not available",
			errcode.GateUnavailable), true
	default:
		return fail(0, ""), false
	}
}

// errApproversUnmet is the answer to a caller who may not read the principals
// when no one can meet an approver list because of a group or the count. It
// names no group and no number of people.
var errApproversUnmet = errors.New(
	"no one can meet the approvers and the count: lower the count, or ask an administrator to check the groups",
)

// approversAnswer returns the error that the caller p gets for err, the error
// of a write of the approver list a. A caller who holds server:read-principals
// gets err. For any other caller, a group that does not exist, and a count
// above the people who can approve when a names a group, give
// errApproversUnmet, so that a role of a namespace cannot learn which groups of
// the installation exist or how many members they have. A failed check gives
// errApproversUnmet too. A count above the users that a names tells nothing
// that the caller did not send, so it passes unchanged.
func (h *Handler) approversAnswer(ctx context.Context, p identity.Principal, a gate.Approvers, err error) error {
	reason := approvers(err).Reason

	hide := reason == gate.ApproversGroupMissing || (reason == gate.ApproversCountAbove && len(a.Groups) > 0)
	if !hide {
		return err
	}

	if h.authz.Authorize(ctx, p, identity.ActionServerReadPrincipals, identity.ServerObject()) == nil {
		return err
	}

	return errApproversUnmet
}

// approvers returns the *gate.ApproversError in the chain of err, or an
// error with no reason when the chain has none.
func approvers(err error) *gate.ApproversError {
	if e, ok := errors.AsType[*gate.ApproversError](err); ok {
		return e
	}

	return &gate.ApproversError{Reason: 0, Count: 0, People: 0}
}

// classifyApprovers maps an error that matches gate.ErrApprovers to its status
// and its code. The message names the reason.
func classifyApprovers(err error) (failure, bool) {
	switch approvers(err).Reason {
	case gate.ApproversGroupInvalid:
		return failCoded(http.StatusUnprocessableEntity, err.Error(), errcode.GateApproversGroupInvalid), true
	case gate.ApproversGroupMissing:
		return failCoded(http.StatusUnprocessableEntity, err.Error(), errcode.GateApproversGroupMissing), true
	case gate.ApproversUserInvalid:
		return failCoded(http.StatusUnprocessableEntity, err.Error(), errcode.GateApproversUserInvalid), true
	case gate.ApproversCountAbove:
		return failCoded(http.StatusUnprocessableEntity, err.Error(), errcode.GateApproversCountAbove,
			errcode.Quantity("people", approvers(err).People)), true
	default:
		// No reason: only a bug makes this error. Fail closed.
		return failCoded(http.StatusInternalServerError, "internal error", errcode.ServerInternal), true
	}
}
