package inventory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/requestid"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/collection"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// maxCollectionBodyBytes bounds the body of a collection change. The body holds
// one name or one collection name, so this is ample.
const maxCollectionBodyBytes = 1024

// errCollectionBody names the body that a collection write needs. Its text is
// the error of the 400 answer.
var errCollectionBody = errors.New(`invalid collection body: provide {"name":"<collection name>"}`)

// errMoveBody names the body that a unit move needs.
var errMoveBody = errors.New(`invalid move body: provide {"collection":"<collection name>"}`)

// errMissingUnit reports a move request without the unit query value.
var errMissingUnit = errors.New(
	"missing unit query parameter: provide the unit reference, for example pulumi:acme/web/prod",
)

// collectionRoutes returns the collection rows of the route table (ADR 51,
// decision 11; ADR 17). A read needs collection:read, a write names its own
// action, and a unit move names unit:move on the unit. The move handler checks
// the rest (see moveUnit): it changes who can reach the unit, so it is an access
// change (ADR 61, decision 4).
func (h *Handler) collectionRoutes() []Route {
	return []Route{
		{
			Method:  http.MethodGet,
			Path:    "/api/inventory/namespaces/{namespace}/collections",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionCollectionRead},
			Resolve: resolveCollectionNamespace,
			Serve:   h.listCollections,
		},
		{
			Method:  http.MethodPost,
			Path:    "/api/inventory/namespaces/{namespace}/collections",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionCollectionCreate},
			Resolve: resolveCollectionNamespace,
			Serve:   h.createCollection,
		},
		{
			Method:  http.MethodGet,
			Path:    "/api/inventory/namespaces/{namespace}/collections/{collection}",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionCollectionRead},
			Resolve: resolveCollectionPath,
			Serve:   h.readCollection,
		},
		{
			Method:  http.MethodDelete,
			Path:    "/api/inventory/namespaces/{namespace}/collections/{collection}",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionCollectionDelete},
			Resolve: resolveCollectionPath,
			Serve:   h.deleteCollection,
		},
		{
			Method:  http.MethodPut,
			Path:    "/api/inventory/unit-collection",
			Scope:   ScopeAuthorized,
			Actions: []identity.Action{identity.ActionUnitMove},
			Resolve: resolveUnitQuery,
			Serve:   h.moveUnit,
		},
	}
}

// collectionEntry is one collection as the console reads it. Name is the name
// as written; a lookup ignores the case.
//
// Contract: never remove or rename these fields.
type collectionEntry struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	CreatedAt int64  `json:"createdAt"`
}

// collectionsResponse is the answered document of the collection list.
//
// Contract: never remove or rename the collections field.
type collectionsResponse struct {
	Collections []collectionEntry `json:"collections"`
}

// moveBody is the body of a unit move. Its field names the target collection.
type moveBody struct {
	Collection string `json:"collection"`
}

// moveResponse is the answered document of a unit move: the unit, the
// collection it is in now and the collection it was in, empty when it had none.
type moveResponse struct {
	Unit               string `json:"unit"`
	Collection         string `json:"collection"`
	PreviousCollection string `json:"previousCollection"`
}

// resolveCollectionNamespace returns the namespace named in the path.
func resolveCollectionNamespace(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	name, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		return identity.Object{}, err
	}

	return identity.NamespaceObject(name), nil
}

// resolveCollectionPath returns the collection named in the path, in the
// namespace of the path. A bad name fails before authorization runs.
func resolveCollectionPath(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	ns, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		return identity.Object{}, err
	}

	name, err := collection.ParseName(r.PathValue("collection"))
	if err != nil {
		return identity.Object{}, err
	}

	return identity.CollectionObject(ns, name.String()), nil
}

// resolveUnitQuery returns the unit named in the unit query value. The value is
// required, so a move without it fails with a 400 before authorization runs.
func resolveUnitQuery(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	raw := r.URL.Query().Get("unit")
	if raw == "" {
		return identity.Object{}, errMissingUnit
	}

	obj, err := identity.ParseObject(raw)
	if err != nil {
		return identity.Object{}, fmt.Errorf("unit reference %q: %w", raw, err)
	}

	if !obj.IsUnit() {
		return identity.Object{}, fmt.Errorf("unit reference %q: %w", raw, identity.ErrInvalidObject)
	}

	return obj, nil
}

// listCollections serves GET /api/inventory/namespaces/{namespace}/collections.
// An unknown namespace answers 404.
func (h *Handler) listCollections(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	if _, err := h.namespaces.Get(ctx, obj.Namespace()); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	cs, err := h.collections.List(ctx, obj.Namespace())
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	out := make([]collectionEntry, 0, len(cs))
	for _, c := range cs {
		out = append(out, collectionOf(c))
	}

	h.writeJSON(ctx, w, http.StatusOK, collectionsResponse{Collections: out})
}

// createCollection serves POST /api/inventory/namespaces/{namespace}/collections.
// It answers 201 with the collection. A name that is taken answers 409. The
// write and its audit entry are one transaction in the store.
func (h *Handler) createCollection(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	name, err := readCollectionName(w, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	ref := collection.Ref{Namespace: obj.Namespace(), Name: name}

	c, err := h.collections.Create(ctx, ref, auditEntry(r, p, obj, identity.ActionCollectionCreate, ""))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusCreated, collectionOf(c))
}

// readCollection serves GET /api/inventory/namespaces/{namespace}/collections/{collection}.
// A name that the namespace does not hold answers 404.
func (h *Handler) readCollection(
	w http.ResponseWriter, r *http.Request, _ identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	c, err := h.collections.Get(ctx, collectionRef(obj))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, collectionOf(c))
}

// deleteCollection serves DELETE /api/inventory/namespaces/{namespace}/collections/{collection}.
// It answers 204. A collection that still holds a live unit answers 409 and is
// kept. The memberships of deleted units go with it, in the same transaction.
func (h *Handler) deleteCollection(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	ref := collectionRef(obj)
	if err := h.collections.Delete(ctx, ref, auditEntry(r, p, obj, identity.ActionCollectionDelete, "")); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// moveUnit serves PUT /api/inventory/unit-collection?unit=<reference>. The body
// names the target collection. The route checked unit:move on the unit. A move
// changes who can reach the unit, so this handler also checks, and each check
// must allow the move:
//   - unit:manage-access on the unit,
//   - collection:create-unit on the target collection,
//   - collection:manage-access on the target collection,
//   - collection:manage-access on the collection the unit is in now, if any.
//
// The move names the collection that the unit had when it was read. The store
// refuses the move with 409 when that collection changed, so a stale read
// cannot move a unit that someone else moved. The audit entry names the unit and
// the two collections: "from=<name|none>;to=<name>".
func (h *Handler) moveUnit(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	in, err := readMoveBody(w, r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	target, err := collection.ParseName(in.Collection)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	unit := collection.UnitRef{Namespace: obj.Namespace(), Tool: obj.Tool(), Name: obj.UnitName()}
	to := collection.Ref{Namespace: obj.Namespace(), Name: target}

	from, err := h.collections.UnitCollection(ctx, unit)

	switch {
	case errors.Is(err, collection.ErrNoCollection):
		from = collection.Ref{Namespace: "", Name: ""}
	case err != nil:
		h.writeError(ctx, w, err)

		return
	default:
	}

	fromName := from.Name

	// The target is read once here: the check and the write both use its
	// stable ID, so a collection that is deleted and created again in between
	// is refused by the write.
	targetCol, err := h.collections.Get(ctx, to)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if authErr := h.authorizeMove(ctx, p, obj, from, to); authErr != nil {
		h.writeError(ctx, w, authErr)

		return
	}

	version := "from=" + nameOrNone(fromName) + ";to=" + to.Name.String()

	err = h.collections.SetUnit(ctx, collection.Move{Unit: unit, From: fromName, To: to, ToID: targetCol.ID},
		auditEntry(r, p, obj, identity.ActionUnitMove, version))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, moveResponse{
		Unit:               unit.String(),
		Collection:         to.Name.String(),
		PreviousCollection: fromName.String(),
	})
}

// moveCheck is one action that a move needs, on one object.
type moveCheck struct {
	action identity.Action
	object identity.Object
}

// MoveChecker decides whether a caller may move a unit into a collection. A
// refusal wraps identity.ErrDenied, and a missing unit or collection wraps
// the error of the store.
type MoveChecker interface {
	CheckMove(ctx context.Context, caller identity.Principal, unit, target identity.Object) error
}

// authorizeMove checks the access of a move from the collection from, when the
// unit had one, into the collection to. Each check must allow the move, and
// the grants that the target collection gives must be ones the caller holds on
// the unit.
func (h *Handler) authorizeMove(
	ctx context.Context, p identity.Principal, obj identity.Object, from, to collection.Ref,
) error {
	toObj := identity.CollectionObject(to.Namespace, to.Name.String())
	checks := []moveCheck{
		{identity.ActionUnitManageAccess, obj},
		{identity.ActionCollectionCreateUnit, toObj},
		{identity.ActionCollectionManageAccess, toObj},
	}

	if from.Name != "" {
		checks = append(checks, moveCheck{
			identity.ActionCollectionManageAccess,
			identity.CollectionObject(from.Namespace, from.Name.String()),
		})
	}

	for i := range checks {
		if err := h.authorize(ctx, p, checks[i].action, checks[i].object); err != nil {
			return err
		}
	}

	if h.moves == nil {
		return fmt.Errorf("move %s: %w: no move checker is configured", obj.String(), identity.ErrDenied)
	}

	return h.moves.CheckMove(ctx, p, obj, toObj)
}

// nameOrNone returns the collection name, or "none" when the unit had none.
func nameOrNone(n collection.Name) string {
	if n == "" {
		return "none"
	}

	return n.String()
}

// authorize checks one action on one object with the authorizer of the handler.
// A denied check wraps identity.ErrDenied.
func (h *Handler) authorize(
	ctx context.Context, p identity.Principal, action identity.Action, obj identity.Object,
) error {
	if err := h.authz.Authorize(ctx, p, action, obj); err != nil {
		return fmt.Errorf("authorize %s: %w", action, err)
	}

	return nil
}

// auditEntry returns the audit entry of one collection change or unit move. The
// store appends it in the same transaction as the change, and sets its time
// from the service clock (ADR 60, decision 4). version carries the move's
// source and target; it is empty for the other changes.
func auditEntry(
	r *http.Request, p identity.Principal, obj identity.Object, action identity.Action, version string,
) audit.Entry {
	return audit.Entry{
		At:         time.Time{},
		Namespace:  obj.Namespace(),
		Subject:    p.Subject,
		IDPSubject: "",
		TokenID:    "",
		Action:     string(action),
		Object:     obj.String(),
		Version:    version,
		ValuePath:  "",
		ValueCount: nil,
		SourceIP:   sourceIP(r),
		UserAgent:  truncateUserAgent(r.UserAgent()),
		Channel:    audit.NormalizeChannel(r.Header.Get(channelHeader)),
		Kind:       audit.KindChange,
		Outcome:    audit.OutcomeSuccess,
		RequestID:  requestid.FromContext(r.Context()),
		PrevHash:   "",
		Hash:       "",
		Sequence:   0,
		Changes:    nil,
		ObjectID:   "",
	}
}

// readCollectionName decodes a create body. It refuses an unknown field, a body
// that is not one object, and a body without a name.
func readCollectionName(w http.ResponseWriter, r *http.Request) (collection.Name, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCollectionBodyBytes))
	if err != nil {
		return "", errCollectionBody
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	var in struct {
		Name *string `json:"name"`
	}

	if err := dec.Decode(&in); err != nil {
		return "", errCollectionBody
	}

	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return "", errCollectionBody
	}

	if in.Name == nil {
		return "", errCollectionBody
	}

	return collection.ParseName(*in.Name)
}

// readMoveBody decodes a move body. It refuses an unknown field, a body that is
// not one object, and a body without a collection.
func readMoveBody(w http.ResponseWriter, r *http.Request) (moveBody, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxCollectionBodyBytes))
	if err != nil {
		return moveBody{}, errMoveBody
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	var in struct {
		Collection *string `json:"collection"`
	}

	if err := dec.Decode(&in); err != nil {
		return moveBody{}, errMoveBody
	}

	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return moveBody{}, errMoveBody
	}

	if in.Collection == nil {
		return moveBody{}, errMoveBody
	}

	return moveBody{Collection: *in.Collection}, nil
}

// collectionRef returns the collection that a collection object names.
func collectionRef(obj identity.Object) collection.Ref {
	return collection.Ref{Namespace: obj.Namespace(), Name: collection.Name(obj.CollectionName())}
}

// collectionOf maps a domain collection to its wire shape.
func collectionOf(c collection.Collection) collectionEntry {
	return collectionEntry{
		Namespace: c.Ref.Namespace.String(),
		Name:      c.Ref.Name.String(),
		CreatedAt: c.CreatedAt.Unix(),
	}
}

// sourceIP returns the remote address that the server sees, without the port.
func sourceIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}
