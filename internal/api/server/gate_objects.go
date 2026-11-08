package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/api/apidesc"
	denials "opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/gate/cedargo"
	"opendustpan.org/opendustpan/pkg/identity"
)

// The paths that hold the gates and the access limits. The scope is part of
// the path: a namespace, one of its collections, or one of its units. A unit
// is named by its tool and the name that the tool gives it, after the segment
// by-name, so that the path never matches the routes of a unit by its stable
// ID. The name is percent-encoded, so the Pulumi stack web/prod of acme is
// /api/dustpan/v1/namespaces/acme/units/by-name/pulumi/web%2Fprod.
const (
	gateNamespaceBase  = "/api/dustpan/v1/namespaces/{namespace}"
	gateCollectionBase = gateNamespaceBase + "/collections/{collection}"
	gateUnitBase       = gateNamespaceBase + "/units/by-name/{tool}/{name}"
)

// Tags of the operations of the gates and the access limits.
const (
	tagGates        = "gates"
	tagAccessLimits = "access-limits"
)

// routesPerObject is the number of routes of one kind of object at one scope
// level: list, get, set and delete.
const routesPerObject = 4

// gateObjectScope is one scope level of the routes of the gates and the access
// limits.
type gateObjectScope struct {
	// base is the path of the scope.
	base string
	// infix is the part of the operation ID that names the scope level.
	infix string
	// kind is the kind of the object of the actions. All three levels
	// authorize on the namespace: one action covers the namespace, its
	// collections and its units.
	kind string
}

// gateObjectResource is one kind of object that a scope holds by ID.
type gateObjectResource struct {
	// segment is the path segment after the scope.
	segment string
	// plural and singular are the words of the operation IDs.
	plural, singular string
	tag              string
	// listBody and itemBody are the bodies of the description.
	listBody, itemBody apidesc.Body
	// reasonQuery is true when the delete takes the query parameter reason.
	reasonQuery bool
}

// gateObjectScopes returns the three scope levels, narrowest last.
func gateObjectScopes() []gateObjectScope {
	return []gateObjectScope{
		{base: gateNamespaceBase, infix: "", kind: apidesc.KindNamespace},
		{base: gateCollectionBase, infix: "Collection", kind: apidesc.KindNamespace},
		{base: gateUnitBase, infix: "Unit", kind: apidesc.KindNamespace},
	}
}

// gateObjectResources returns the gates and the access limits.
func gateObjectResources() []gateObjectResource {
	return []gateObjectResource{
		{
			segment: "gates", plural: "Gates", singular: "Gate", tag: tagGates,
			listBody: gateListSchema, itemBody: gateSchema, reasonQuery: true,
		},
		{
			segment: "access-limits", plural: "AccessLimits", singular: "AccessLimit", tag: tagAccessLimits,
			listBody: accessLimitListSchema, itemBody: accessLimitSchema, reasonQuery: false,
		},
	}
}

// gateObjectRoutes returns the rows of the gates and the access limits of the
// three scope levels. A read needs namespace:read-gates, and a write needs
// namespace:manage-gates and a fresh sign-in.
func (h *Handler) gateObjectRoutes(
	row func(method, path string, actions []identity.Action, serve RouteHandler) Route,
	read, manage []identity.Action,
) []Route {
	scopes, resources := gateObjectScopes(), gateObjectResources()
	routes := make([]Route, 0, len(scopes)*len(resources)*routesPerObject)

	fresh := func(serve RouteHandler) RouteHandler { return h.fresh(identity.ActionNamespaceManageGates, serve) }

	// The handlers are bound here and not in gateObjectResources: the labels
	// read the resources, and the handlers read the labels through queryOf.
	handlers := map[string][4]RouteHandler{
		"gates": {h.listGates, h.readGate, h.writeGate, h.removeGate},
		"access-limits": {
			h.listAccessLimits, h.readAccessLimit, h.writeAccessLimit, h.removeAccessLimit,
		},
	}

	for i := range scopes {
		for j := range resources {
			sc, res := &scopes[i], &resources[j]
			listPath := sc.base + "/" + res.segment
			item := listPath + "/{id}"
			serve := handlers[res.segment]

			routes = append(routes,
				row(http.MethodGet, listPath, read, serve[0]),
				row(http.MethodGet, item, read, serve[1]),
				row(http.MethodPut, item, manage, fresh(serve[2])),
				row(http.MethodDelete, item, manage, fresh(serve[3])),
			)
		}
	}

	return routes
}

// gateObjectLabels returns the labels of the gate and access limit operations,
// by the pattern of the route.
func gateObjectLabels() map[string]apidesc.Label {
	reason := apidesc.StringParam("reason", "Why a final gate is deleted. A final gate needs a reason to be deleted.")
	labels := map[string]apidesc.Label{}

	scopes, resources := gateObjectScopes(), gateObjectResources()

	for i := range scopes {
		for j := range resources {
			sc, res := &scopes[i], &resources[j]
			listPath := sc.base + "/" + res.segment
			item := listPath + "/{id}"
			name := sc.infix + res.singular
			plural := sc.infix + res.plural

			remove := apidesc.Name("delete"+name, res.tag).WithIfMatch().On(sc.kind).
				WithBodies(apidesc.NoBody, apidesc.NoBody)
			if res.reasonQuery {
				remove = remove.WithQuery(reason)
			}

			labels[apidesc.Pattern(http.MethodGet, listPath)] = apidesc.Name("list"+plural, res.tag).
				On(sc.kind).WithBodies(apidesc.NoBody, res.listBody)
			labels[apidesc.Pattern(http.MethodGet, item)] = apidesc.Name("get"+name, res.tag).WithETag().
				On(sc.kind).WithBodies(apidesc.NoBody, res.itemBody)
			labels[apidesc.Pattern(http.MethodPut, item)] = apidesc.Name("set"+name, res.tag).WithIfMatch().
				WithETag().On(sc.kind).WithBodies(res.itemBody, res.itemBody)
			labels[apidesc.Pattern(http.MethodDelete, item)] = remove
		}
	}

	return labels
}

// The bodies of the gate and access limit operations.
//
//nolint:gochecknoglobals // Fixed values; Go has no constant structs.
var (
	gateSchema     = apidesc.Typed[gateBody]("Gate", apidesc.Named[checkedWithBody]("GateCheckedWith"))
	gateListSchema = apidesc.Typed[gateListBody](
		"ListGatesResponse", apidesc.Named[gateBody]("Gate"), apidesc.Named[checkedWithBody]("GateCheckedWith"),
	)
	accessLimitSchema     = apidesc.Typed[accessLimitBody]("AccessLimit")
	accessLimitListSchema = apidesc.Typed[accessLimitListBody](
		"ListAccessLimitsResponse", apidesc.Named[accessLimitBody]("AccessLimit"),
	)
)

// checkedWithBody names the engine and the fact schema that a gate passed at
// its last save.
//
// Contract: never remove or rename these fields. The console reads only this
// API, and it updates on its own schedule.
type checkedWithBody struct {
	Engine string `json:"engine"`
	Schema string `json:"schema"`
}

// gateBody is a gate on the wire. ID, Version and CheckedWith are set on an
// answer and refused or ignored on a request: the path names the ID, the
// If-Match header carries the expected version, and the server sets
// CheckedWith. Reason is read on a request only: the reason of a change that
// makes a final gate weaker. An empty mode means evaluate, and an empty
// onError means fail.
//
// Contract: never remove or rename these fields. The console reads only this
// API, and it updates on its own schedule.
type gateBody struct {
	ID            string           `json:"id,omitempty"`
	DecisionPoint string           `json:"decisionPoint"`
	Condition     string           `json:"condition"`
	Unless        string           `json:"unless"`
	Outcome       string           `json:"outcome"`
	Message       string           `json:"message"`
	Mode          string           `json:"mode"`
	OnError       string           `json:"onError"`
	Final         bool             `json:"final"`
	Reason        string           `json:"reason,omitempty"`
	CheckedWith   *checkedWithBody `json:"checkedWith,omitempty"`
	Version       int64            `json:"version,omitempty"`
}

// gateListBody is the answer of the list route of the gates.
type gateListBody struct {
	Gates []gateBody `json:"gates"`
}

// accessLimitBody is an access limit on the wire. ID and Version are set on an
// answer: the path names the ID, and the If-Match header carries the expected
// version.
//
// Contract: never remove or rename these fields. The console reads only this
// API, and it updates on its own schedule.
type accessLimitBody struct {
	ID      string `json:"id,omitempty"`
	Policy  string `json:"policy"`
	Version int64  `json:"version,omitempty"`
}

// accessLimitListBody is the answer of the list route of the access limits.
type accessLimitListBody struct {
	AccessLimits []accessLimitBody `json:"accessLimits"`
}

func gateOf(g gate.Gate) gateBody {
	var checked *checkedWithBody
	if g.CheckedWith.Engine != "" {
		checked = &checkedWithBody{Engine: g.CheckedWith.Engine, Schema: g.CheckedWith.Schema.String()}
	}

	return gateBody{
		ID: string(g.ID), DecisionPoint: string(g.DecisionPoint), Condition: g.Condition, Unless: g.Unless,
		Outcome: string(g.Outcome), Message: g.Message, Mode: string(g.Mode), OnError: string(g.OnError),
		Final: g.Final, Reason: "", CheckedWith: checked, Version: g.Version,
	}
}

func accessLimitOf(l gate.AccessLimit) accessLimitBody {
	return accessLimitBody{ID: string(l.ID), Policy: l.Policy, Version: l.Version}
}

// gateScopeOf returns the scope that the path names: the namespace of obj, or
// one of its collections, or one of its units. A name that is not valid
// answers 400 before any store call.
func gateScopeOf(r *http.Request, obj identity.Object) (gate.Scope, error) {
	ns := obj.Namespace()

	if c := r.PathValue("collection"); c != "" {
		name, err := collection.ParseName(c)
		if err != nil {
			return gate.Scope{}, fmt.Errorf("%w: collection %q", identity.ErrInvalidObject, c)
		}

		return gate.CollectionScope(ns, name), nil
	}

	if tool := r.PathValue("tool"); tool != "" {
		scope, err := gate.UnitScope(ns, tool, r.PathValue("name"))
		if err != nil {
			return gate.Scope{}, fmt.Errorf("%w: unit %q of the tool %q", identity.ErrInvalidObject,
				r.PathValue("name"), tool)
		}

		return scope, nil
	}

	return gate.NamespaceScope(ns), nil
}

// objectIDOf reads the ID of a gate or an access limit from the path. An ID
// that does not match the grammar answers 422. A request never names the ID of
// a gate that only the server makes.
func objectIDOf(r *http.Request) (gate.ID, error) {
	return gate.ParseID(r.PathValue("id"))
}

// listGates serves GET {scope}/gates. It lists the gates stored at the scope,
// not the gates of a wider scope.
func (h *Handler) listGates(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	h.serveObjectList(w, r, obj, func(ctx context.Context, scope gate.Scope) (any, error) {
		gates, err := h.gates.Gates(ctx, scope)
		if err != nil {
			return nil, err
		}

		out := gateListBody{Gates: make([]gateBody, 0, len(gates))}
		for i := range gates {
			out.Gates = append(out.Gates, gateOf(gates[i]))
		}

		return out, nil
	})
}

// serveObjectList answers a list route: it reads the scope of the path, asks
// list for the body of the answer, and writes it.
func (h *Handler) serveObjectList(
	w http.ResponseWriter, r *http.Request, obj identity.Object,
	list func(context.Context, gate.Scope) (any, error),
) {
	ctx := r.Context()

	scope, err := gateScopeOf(r, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	out, err := list(ctx, scope)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, out)
}

// readGate serves GET {scope}/gates/{id}.
func (h *Handler) readGate(w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object) {
	ctx := r.Context()

	scope, id, err := h.gateObjectKey(r, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	g, err := h.gates.Gate(ctx, scope, id)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.Header().Set("ETag", etag(g.Version))
	h.writeJSON(ctx, w, http.StatusOK, gateOf(g))
}

// gateObjectKey reads the scope and the ID of the path.
func (h *Handler) gateObjectKey(r *http.Request, obj identity.Object) (gate.Scope, gate.ID, error) {
	scope, err := gateScopeOf(r, obj)
	if err != nil {
		return gate.Scope{}, "", err
	}

	id, err := objectIDOf(r)
	if err != nil {
		return gate.Scope{}, "", err
	}

	return scope, id, nil
}

// writeGate serves PUT {scope}/gates/{id}. It answers 200 with the stored gate.
// With If-Match, the write replaces that version only. Without it, the write
// replaces the stored gate, or creates it. The body names no ID: the path names
// it.
func (h *Handler) writeGate(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	scope, id, err := h.gateObjectKey(r, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	var in gateBody
	if decodeErr := decodeGateBody(w, r, &in); decodeErr != nil {
		h.writeError(ctx, w, decodeErr)

		return
	}

	if in.ID != "" {
		h.writeError(ctx, w, errInvalidBody)

		return
	}

	g := gateFrom(scope, id, in)

	prev, err := h.expectedVersion(r, func() (int64, error) {
		cur, getErr := h.gates.Gate(ctx, scope, id)

		return cur.Version, getErr
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	stored, err := h.gates.PutGate(ctx, g, prev, in.Reason, gateEntry(r, p))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.Header().Set("ETag", etag(stored.Version))
	h.writeJSON(ctx, w, http.StatusOK, gateOf(stored))
}

// gateFrom builds the gate of a request body. The values are not checked
// here: the service checks each one, and the engine checks the policy.
func gateFrom(scope gate.Scope, id gate.ID, in gateBody) gate.Gate {
	mode := gate.Mode(in.Mode)
	if in.Mode == "" {
		mode = gate.ModeEvaluate
	}

	onError := gate.OnError(in.OnError)
	if in.OnError == "" {
		onError = gate.OnErrorFail
	}

	return gate.Gate{
		Scope: scope, ID: id, Version: 0, DecisionPoint: gate.DecisionPoint(in.DecisionPoint),
		Condition: in.Condition, Unless: in.Unless, Outcome: gate.Outcome(in.Outcome), Message: in.Message,
		Mode: mode, OnError: onError, Final: in.Final, CheckedWith: gate.CheckedWith{
			Engine: "", Schema: gate.SchemaVersion{Subject: "", Major: 0, Minor: 0},
		}, State: "", Reads: nil,
	}
}

// removeGate serves DELETE {scope}/gates/{id}. It answers 204. The query
// parameter reason holds the reason that a final gate needs to be deleted.
func (h *Handler) removeGate(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
	ctx := r.Context()

	scope, id, err := h.gateObjectKey(r, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	version, err := h.existingVersion(r, func() (int64, error) {
		cur, getErr := h.gates.Gate(ctx, scope, id)

		return cur.Version, getErr
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	ref := gate.ObjectRef{Scope: scope, ID: id, Version: version}
	if err = h.gates.DeleteGate(ctx, ref, queryOf(r).Get("reason"), gateEntry(r, p)); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// listAccessLimits serves GET {scope}/access-limits. It lists the access limits
// stored at the scope, not those of a wider scope.
func (h *Handler) listAccessLimits(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	h.serveObjectList(w, r, obj, func(ctx context.Context, scope gate.Scope) (any, error) {
		limits, err := h.gates.AccessLimits(ctx, scope)
		if err != nil {
			return nil, err
		}

		out := accessLimitListBody{AccessLimits: make([]accessLimitBody, 0, len(limits))}
		for i := range limits {
			out.AccessLimits = append(out.AccessLimits, accessLimitOf(limits[i]))
		}

		return out, nil
	})
}

// readAccessLimit serves GET {scope}/access-limits/{id}.
func (h *Handler) readAccessLimit(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	scope, id, err := h.gateObjectKey(r, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	l, err := h.gates.AccessLimit(ctx, scope, id)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.Header().Set("ETag", etag(l.Version))
	h.writeJSON(ctx, w, http.StatusOK, accessLimitOf(l))
}

// writeAccessLimit serves PUT {scope}/access-limits/{id}. The version rules
// follow writeGate.
func (h *Handler) writeAccessLimit(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	scope, id, err := h.gateObjectKey(r, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	var in accessLimitBody
	if decodeErr := decodeGateBody(w, r, &in); decodeErr != nil {
		h.writeError(ctx, w, decodeErr)

		return
	}

	if in.ID != "" {
		h.writeError(ctx, w, errInvalidBody)

		return
	}

	// A text that LimitGroupDirections refuses is not decided here: it goes
	// to PutAccessLimit below, whose check refuses each such text too, with
	// the membership rule or a diagnostic.
	if groups, groupsErr := cedargo.LimitGroupDirections(in.Policy); groupsErr == nil && len(groups) > 0 {
		if reachErr := h.limitGroupReach(ctx, p); reachErr != nil {
			h.guard.Record(ctx, r, p, denials.Denial{
				Action: identity.ActionNamespaceManageGates,
				Object: obj,
				Reason: "access limit names a group that the author cannot manage",
				Window: 0,
			})
			h.writeError(ctx, w, reachErr)

			return
		}
	}

	prev, err := h.expectedVersion(r, func() (int64, error) {
		cur, getErr := h.gates.AccessLimit(ctx, scope, id)

		return cur.Version, getErr
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	l := gate.AccessLimit{Scope: scope, ID: id, Version: 0, Policy: in.Policy}

	stored, err := h.gates.PutAccessLimit(ctx, l, prev, gateEntry(r, p))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.Header().Set("ETag", etag(stored.Version))
	h.writeJSON(ctx, w, http.StatusOK, accessLimitOf(stored))
}

// limitGroupReach checks that p may manage the groups of the installation,
// which an access limit that names a group can freeze. It returns nil for the
// bootstrap administrator and for a caller whom the access decision allows to
// manage the groups of the installation.
func (h *Handler) limitGroupReach(ctx context.Context, p identity.Principal) error {
	if p.Admin {
		return nil
	}

	err := h.authz.Authorize(ctx, p, identity.ActionServerManageGroups, identity.ServerObject())
	switch {
	case err == nil:
		return nil
	case errors.Is(err, identity.ErrDenied):
		return gate.ErrLimitGroupReach
	default:
		return fmt.Errorf("%w: %w", gate.ErrLimitGroupReach, err)
	}
}

// removeAccessLimit serves DELETE {scope}/access-limits/{id}. It answers 204.
func (h *Handler) removeAccessLimit(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	scope, id, err := h.gateObjectKey(r, obj)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	version, err := h.existingVersion(r, func() (int64, error) {
		cur, getErr := h.gates.AccessLimit(ctx, scope, id)

		return cur.Version, getErr
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	ref := gate.ObjectRef{Scope: scope, ID: id, Version: version}
	if err = h.gates.DeleteAccessLimit(ctx, ref, gateEntry(r, p)); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}
