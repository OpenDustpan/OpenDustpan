package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
)

// maxIPAllowlistBodyBytes bounds the body of an IP allowlist change. A list
// holds at most ipallow.MaxEntries entries, so this is ample.
const maxIPAllowlistBodyBytes = 16 * 1024

// errIPAllowlistBody names the body that an IP allowlist change needs. Its
// text is the error of the 400 answer.
var errIPAllowlistBody = errors.New(`invalid IP allowlist body: provide {"cidrs":["10.0.0.0/8", ...]}`)

// ipAllowlistPath is the route pattern of the read, the change and the
// opt-out of the IP allowlist of one namespace.
const ipAllowlistPath = "/api/server/namespaces/{namespace}/ip-allowlist"

// Audit actions of the IP allowlist changes. The service stamps the time and
// the namespace; the entry below carries the caller, the object and the
// outcome. They are audit values only, never authorization actions.
const (
	ipAllowlistSetAction    identity.Action = "namespace:set-ip-allowlist"
	ipAllowlistRemoveAction identity.Action = "namespace:remove-ip-allowlist"
)

// ipAllowlistResponse is one IP allowlist on the wire: whether the feature is
// on for the installation, whether the namespace opted in with its own list,
// and the canonical CIDR text. CIDRs is empty, never null, when the namespace
// has no list.
//
// Contract: never remove or rename these fields. The console reads only this
// API, and it updates on its own schedule.
type ipAllowlistResponse struct {
	Enabled bool     `json:"enabled"`
	OptedIn bool     `json:"optedIn"`
	CIDRs   []string `json:"cidrs"`
}

// ipAllowlistInput is the body of a change. The pointer tells a missing or
// null field from an empty list, so a change must name "cidrs".
type ipAllowlistInput struct {
	CIDRs *[]string `json:"cidrs"`
}

// decodeIPAllowlist reads a change body. It refuses an unknown field, a body
// that is not one object, and a body that lacks "cidrs" or names it null.
func decodeIPAllowlist(body []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	var in ipAllowlistInput

	if err := dec.Decode(&in); err != nil {
		return nil, errIPAllowlistBody
	}

	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errIPAllowlistBody
	}

	if in.CIDRs == nil {
		return nil, errIPAllowlistBody
	}

	return *in.CIDRs, nil
}

// ipAllowlistOf returns the body that shows whether the feature is on and the
// list l of the namespace.
func ipAllowlistOf(enabled bool, l ipallow.List) ipAllowlistResponse {
	return ipAllowlistResponse{Enabled: enabled, OptedIn: !l.IsEmpty(), CIDRs: l.Strings()}
}

// ipAllowlistEntry returns the audit entry of the caller p for an IP
// allowlist change on obj: who acted, from where and through which channel.
// The service stamps the time and the namespace.
func ipAllowlistEntry(
	r *http.Request, p identity.Principal, obj identity.Object, action identity.Action,
) audit.Entry {
	entry := callerEntry(r, p, action)
	entry.Namespace = obj.Namespace()
	entry.Object = obj.String()
	entry.Kind = audit.KindChange
	entry.Outcome = audit.OutcomeSuccess

	return entry
}

// ipAllowlistRoutes returns the IP allowlist rows of the route table
// (ADR 17): a read, a change and an opt-out of the list of one namespace.
// Nil service means no allowlist store is configured: no route is mounted.
// Every route needs namespace:manage-ip-allowlist: the list shows the
// network layout of the operator, so only a manager may read it.
func (h *Handler) ipAllowlistRoutes() []Route {
	if h.ipAllowlists == nil {
		return nil
	}

	return []Route{
		{
			Method:  http.MethodGet,
			Path:    ipAllowlistPath,
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionNamespaceManageIPAllowlist},
			Resolve: resolveNamespacePath, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.readIPAllowlist,
		},
		{
			Method:  http.MethodPut,
			Path:    ipAllowlistPath,
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionNamespaceManageIPAllowlist},
			Resolve: resolveNamespacePath, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.fresh(ipAllowlistSetAction, h.writeIPAllowlist),
		},
		{
			Method:  http.MethodDelete,
			Path:    ipAllowlistPath,
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionNamespaceManageIPAllowlist},
			Resolve: resolveNamespacePath, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.fresh(ipAllowlistRemoveAction, h.removeIPAllowlist),
		},
	}
}

// readIPAllowlist serves GET /api/server/namespaces/{namespace}/ip-allowlist.
// It answers 200 with the feature switch and the list. A namespace without a
// rule answers optedIn false with an empty list.
func (h *Handler) readIPAllowlist(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	l, err := h.ipAllowlists.Get(ctx, obj.Namespace())
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, ipAllowlistOf(h.ipAllowlists.Enabled(), l))
}

// writeIPAllowlist serves PUT /api/server/namespaces/{namespace}/ip-allowlist.
// It answers 200 with the list as stored. A body that is empty, null, lacks
// "cidrs", holds an unknown field or trailing data answers 400 and changes no
// list. An empty list answers 400: DELETE opts out.
func (h *Handler) writeIPAllowlist(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	body, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, maxIPAllowlistBodyBytes))
	if readErr != nil {
		h.writeError(ctx, w, errInvalidBody)

		return
	}

	items, decodeErr := decodeIPAllowlist(body)
	if decodeErr != nil {
		h.writeError(ctx, w, decodeErr)

		return
	}

	if len(items) == 0 {
		h.writeError(ctx, w, fmt.Errorf("%w: no addresses, use DELETE to opt out", ipallow.ErrInvalidList))

		return
	}

	l, setErr := h.ipAllowlists.Set(ctx, obj.Namespace(), items, ipAllowlistEntry(r, p, obj, ipAllowlistSetAction))
	if setErr != nil {
		h.writeError(ctx, w, setErr)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, ipAllowlistOf(h.ipAllowlists.Enabled(), l))
}

// removeIPAllowlist serves DELETE /api/server/namespaces/{namespace}/ip-allowlist.
// It removes the rule and answers 204. A namespace without a rule answers 204
// too and writes no audit entry. It works while the feature is off.
func (h *Handler) removeIPAllowlist(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	l, getErr := h.ipAllowlists.Get(ctx, obj.Namespace())
	if getErr != nil {
		h.writeError(ctx, w, getErr)

		return
	}

	if l.IsEmpty() {
		w.WriteHeader(http.StatusNoContent)

		return
	}

	removeErr := h.ipAllowlists.Remove(ctx, obj.Namespace(), ipAllowlistEntry(r, p, obj, ipAllowlistRemoveAction))
	if removeErr != nil {
		h.writeError(ctx, w, removeErr)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// classifyIPAllowlist maps the IP allowlist errors to their status and
// message: a bad body or list answers 400 with the error text, and a change
// while the feature is off answers 409 that names the setting. A lost audit
// entry fails closed with 500 through the default branch of classify.
func classifyIPAllowlist(err error) (failure, bool) {
	switch {
	case errors.Is(err, errIPAllowlistBody):
		return fail(http.StatusBadRequest, errIPAllowlistBody.Error()), true
	case errors.Is(err, ipallow.ErrInvalidList):
		return fail(http.StatusBadRequest, err.Error()), true
	case errors.Is(err, ipallow.ErrDisabled):
		return fail(http.StatusConflict,
			"IP allowlists are off for this installation: start the server with --ip-allowlists"), true
	default:
		return fail(0, ""), false
	}
}
