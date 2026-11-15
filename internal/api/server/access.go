package server

// Role and grant routes of the installation API (ADR 52, decisions 3, 5, 7
// and 15).
//
// Roles belong to the installation. Every signed-in principal reads the
// role catalog (role:read). server:manage-roles makes, changes and deletes
// custom roles.
//
// A grant lives on its object, so each object kind has its own routes and
// its own manage-access action, checked on that object:
//
//   - /api/server/grants: the installation, server:manage-access,
//   - /api/server/namespaces/{namespace}/grants: namespace:manage-access,
//   - /api/server/namespaces/{namespace}/collections/{collection}/grants:
//     collection:manage-access,
//   - /api/server/namespaces/{namespace}/unit-grants?tool=&unit=:
//     unit:manage-access. A unit name can hold '/', so the unit is a query.
//
// A grant can give only actions that the granter holds on the object
// (access.ErrEscalation, 403). A revoke ends a grant and keeps its row for
// the history, and a revoke through one object never ends a grant on
// another. DELETE /api/server/grants/{id} revokes any grant by its ID,
// whatever its object: it checks the manage-access action of the grant's
// own object, and a grant the caller may not see answers 404 like an
// unknown ID. The grant lists take limit and cursor, and answer total and
// nextCursor.
//
// Each change writes its audit entry in the transaction of the change (fail
// closed): in the namespace of a grant on a namespace, a collection or a
// unit, and in @server otherwise. A refused change writes a failure entry
// after the fact in the same scope, as well as possible.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	denials "opendustpan.org/opendustpan/internal/api/refusal"
	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// Body errors of the role and grant routes. classifyAccess maps each to 400.
var (
	errRoleBody = errors.New(
		`invalid role body: provide {"name":"...","description":"...","actions":["unit:read"]}`,
	)
	errRoleChangeBody = errors.New(
		`invalid role change body: provide {"name":"..."}, {"description":"..."} or {"actions":[...]}`,
	)
	errGrantBody = errors.New(
		`invalid grant body: provide {"subject":"principal:<id>|group:<id>#member","role":"..."}`,
	)
	errUnitQuery = errors.New(`invalid unit: provide the query parameters "tool" and "unit"`)
)

// errNoAccessStore reports a server whose store keeps no roles and grants.
// Both SQL adapters keep them, so only a custom store without the port
// answers it.
var errNoAccessStore = errors.New("the store of this server keeps no roles and grants")

// roleResponse is one role on the wire. A built-in role has its name as its
// ID, and its times are the Unix epoch.
//
// Contract: never remove or rename these fields. The console and the
// clients read only this API, and they update on their own schedule.
type roleResponse struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	CreatedAt   string   `json:"createdAt"`
	UpdatedAt   string   `json:"updatedAt"`
	Actions     []string `json:"actions"`
	BuiltIn     bool     `json:"builtIn"`
}

type roleListResponse struct {
	Roles []roleResponse `json:"roles"`
}

// grantResponse is one grant on the wire. Subject is the text form of the
// holder, "principal:<id>" or "group:<id>#member". Object is the wire form
// of the object: "@server", a namespace, "<namespace>/<collection>" or a
// unit reference.
//
// Contract: never remove or rename these fields.
type grantResponse struct {
	ID        string `json:"id"`
	Subject   string `json:"subject"`
	Role      string `json:"role"`
	Object    string `json:"object"`
	CreatedAt string `json:"createdAt"`
	CreatedBy string `json:"createdBy"`
}

// listedGrantResponse is one grant of a list on the wire, with its holder:
// HolderName is the display name of a principal (its subject when it has no
// display name) or the name of a group, and HolderKind is "person",
// "service", "workload" or "group". So a list shows who holds each grant
// with no read for each holder.
//
// Contract: never remove or rename these fields.
type listedGrantResponse struct {
	grantResponse

	HolderName string `json:"holderName"`
	HolderKind string `json:"holderKind"`
}

// grantListResponse is one page of grants. Total is the count of every
// current grant on the object that matches the filters. NextCursor is empty
// on the last page.
type grantListResponse struct {
	Grants     []listedGrantResponse `json:"grants"`
	NextCursor string                `json:"nextCursor"`
	Total      int64                 `json:"total"`
}

// roleRequest is the body of a role create, and of a role change, where a
// missing field keeps its value.
type roleRequest struct {
	Name        *string   `json:"name"`
	Description *string   `json:"description"`
	Actions     *[]string `json:"actions"`
}

// grantRequest is the body of a grant.
type grantRequest struct {
	Subject *string `json:"subject"`
	Role    *string `json:"role"`
}

func roleOf(r access.Role) roleResponse {
	actions := make([]string, 0, len(r.Actions))
	for _, a := range r.Actions {
		actions = append(actions, string(a))
	}

	return roleResponse{
		ID:          r.ID,
		Name:        r.Name,
		Description: r.Description,
		CreatedAt:   wireTime(r.CreatedAt),
		UpdatedAt:   wireTime(r.UpdatedAt),
		Actions:     actions,
		BuiltIn:     r.BuiltIn,
	}
}

func grantOf(g access.Grant) grantResponse {
	return grantResponse{
		ID:        g.ID,
		Subject:   g.Subject.String(),
		Role:      g.Role,
		Object:    g.Object.String(),
		CreatedAt: wireTime(g.CreatedAt),
		CreatedBy: g.CreatedBy,
	}
}

// accessOp is one role or grant operation. It answers its result, or an
// error with the result so far: a change names its record in both.
// The call holds the audit entry of the caller, which a change hands to the
// Service.
type accessOp func(w http.ResponseWriter, r *http.Request, svc *access.Service, c *call) (principalResult, error)

// serveAccessRead adapts a read to a route handler.
func (h *Handler) serveAccessRead(op accessOp) RouteHandler {
	return func(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
		var none audit.Entry

		if res, ok := h.runAccessOp(w, r, &call{p: p, obj: obj, e: none}, op); ok {
			h.writePrincipalResult(w, r, res)
		}
	}
}

// serveAccessChange adapts a change to a route handler. The Service writes
// the success entry in the transaction of the change. A refused change
// writes one failure entry here, as well as possible.
func (h *Handler) serveAccessChange(action identity.Action, op accessOp) RouteHandler {
	return func(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
		res, ok := h.runAccessOp(w, r, &call{p: p, obj: obj, e: callerEntry(r, p, action)}, op)

		if !ok && res.object != "" && h.access != nil {
			h.appendObjectChange(r, p, obj, serverChange{
				at: h.access.Now(), action: action, object: res.object, outcome: audit.OutcomeFailure,
			})
		}

		if ok {
			h.writePrincipalResult(w, r, res)
		}
	}
}

// serveFreshAccessChange is serveAccessChange for a change that gives rights:
// it needs a console session with a recent sign-in, and refuses an access
// token.
func (h *Handler) serveFreshAccessChange(action identity.Action, op accessOp) RouteHandler {
	return h.fresh(action, h.serveAccessChange(action, op))
}

// call is the caller, the object and the audit entry of one operation.
type call struct {
	p   identity.Principal
	obj identity.Object
	e   audit.Entry
}

// runAccessOp runs op and writes its error. It reports whether op
// succeeded, with the result in both cases.
func (h *Handler) runAccessOp(w http.ResponseWriter, r *http.Request, c *call, op accessOp) (principalResult, bool) {
	ctx := r.Context()

	if h.access == nil {
		h.writeError(ctx, w, errNoAccessStore)

		return failed(""), false
	}

	res, err := op(w, r, h.access, c)
	if err != nil {
		refused := denials.Denial{
			Action: identity.Action(c.e.Action), Object: c.obj, Reason: "", ReasonWithin: nil, Window: 0,
		}
		h.recordEscalation(ctx, r, c.p, refused, err)
		h.writeError(ctx, w, err)

		return res, false
	}

	return res, true
}

// reasonEscalation is the reason of the entry of a grant that gives more than
// the granter holds.
const reasonEscalation = "grant gives more than the granter holds"

// recordEscalation writes the audit entry of a change that the access service
// refused with access.ErrEscalation. The 403 alone leaves no trace, and the
// check of the manage action above passed, so no other layer writes an entry.
// A reach refusal names the missing actions with their objects; any other
// escalation keeps the fixed reason. It does nothing for any other error.
func (h *Handler) recordEscalation(
	ctx context.Context, r *http.Request, p identity.Principal, d denials.Denial, err error,
) {
	if !errors.Is(err, access.ErrEscalation) {
		return
	}

	d.Reason = reasonEscalation

	if reason := reachReason(err); reason != "" {
		d.Reason = reason

		if refusal, ok := errors.AsType[access.ReachRefusal](err); ok {
			missing := refusal.MissingActions()
			d.ReasonWithin = func(limit int) string {
				return access.MissingActionsReasonWithin(missing, limit)
			}
		}
	}

	h.guard.Record(ctx, r, p, d)
}

// Route paths of the roles and the grants.
const (
	rolesPath            = "/api/server/roles"
	rolePath             = "/api/server/roles/{id}"
	serverGrantsPath     = "/api/server/grants"
	namespaceGrantsPath  = "/api/server/namespaces/{namespace}/grants"
	collectionGrantsPath = "/api/server/namespaces/{namespace}/collections/{collection}/grants"
	unitGrantsPath       = "/api/server/namespaces/{namespace}/unit-grants"
	// principalGrantsPath holds the grants on one service principal. Their
	// action is server:manage-access, which only a grant on the
	// installation gives: a grant on a principal lets its holder act as
	// the principal.
	principalGrantsPath = "/api/server/principals/{principal}/grants"
	grantIDSuffix       = "/{id}"
)

// roleRoutes returns the role rows of the route table.
func (h *Handler) roleRoutes() []Route {
	read := []identity.Action{identity.ActionRoleRead}
	manage := []identity.Action{identity.ActionServerManageRoles}

	return []Route{
		{
			Method:       http.MethodGet,
			Path:         rolesPath,
			Scope:        ScopeAuthorized,
			Actions:      read,
			Resolve:      resolveServer,
			ActionsFor:   nil,
			OpaqueDenial: false,
			Serve:        h.serveAccessRead(listRoles),
		},
		{
			Method:       http.MethodGet,
			Path:         rolePath,
			Scope:        ScopeAuthorized,
			Actions:      read,
			Resolve:      resolveServer,
			ActionsFor:   nil,
			OpaqueDenial: false,
			Serve:        h.serveAccessRead(getRole),
		},
		{
			Method:     http.MethodPost,
			Path:       rolesPath,
			Scope:      ScopeAuthorized,
			Actions:    manage,
			Resolve:    resolveServer,
			ActionsFor: nil, OpaqueDenial: false,
			Serve: h.serveFreshAccessChange(identity.ActionServerManageRoles, createRole),
		},
		{
			Method:     http.MethodPatch,
			Path:       rolePath,
			Scope:      ScopeAuthorized,
			Actions:    manage,
			Resolve:    resolveServer,
			ActionsFor: nil, OpaqueDenial: false,
			Serve: h.serveFreshAccessChange(identity.ActionServerManageRoles, changeRole),
		},
		{
			Method:     http.MethodDelete,
			Path:       rolePath,
			Scope:      ScopeAuthorized,
			Actions:    manage,
			Resolve:    resolveServer,
			ActionsFor: nil, OpaqueDenial: false,
			Serve: h.serveAccessChange(identity.ActionServerManageRoles, deleteRole),
		},
	}
}

// grantRoutes returns the grant rows of the route table: a list, a grant
// and a revoke on each object kind. DELETE /api/server/grants/{id} is the
// exception: it revokes any grant by its ID, whatever its object, so its
// resolver returns the grant's own object and its route check authorizes
// the manage-access action of that object.
func (h *Handler) grantRoutes() []Route {
	kinds := []struct {
		resolve ObjectResolver
		path    string
		action  identity.Action
		// fresh makes the grant need a fresh authentication.
		fresh bool
	}{
		{resolveServer, serverGrantsPath, identity.ActionServerManageAccess, true},
		{resolveNamespacePath, namespaceGrantsPath, identity.ActionNamespaceManageAccess, true},
		{resolveCollectionPath, collectionGrantsPath, identity.ActionCollectionManageAccess, true},
		{resolveUnitQuery, unitGrantsPath, identity.ActionUnitManageAccess, false},
		{resolvePrincipalPath, principalGrantsPath, identity.ActionServerManageAccess, true},
	}

	const routesPerKind = 3 // a list, a grant and a revoke

	out := make([]Route, 0, routesPerKind*len(kinds))

	for _, k := range kinds {
		actions := []identity.Action{k.action}

		grant := h.serveAccessChange(k.action, createGrant)
		if k.fresh {
			grant = h.serveFreshAccessChange(k.action, createGrant)
		}

		out = append(out,
			authorizedRoute(http.MethodGet, k.path, actions, k.resolve, h.serveAccessRead(listGrants)),
			authorizedRoute(http.MethodPost, k.path, actions, k.resolve, grant),
		)

		if k.path == serverGrantsPath {
			// DELETE on the installation path revokes any grant by ID.
			continue
		}

		out = append(out, authorizedRoute(
			http.MethodDelete, k.path+grantIDSuffix, append([]identity.Action(nil), actions...),
			k.resolve, h.serveAccessChange(k.action, revokeGrant),
		))
	}

	out = append(out, Route{
		Method: http.MethodDelete, Path: serverGrantsPath + grantIDSuffix, Scope: ScopeAuthorized,
		Actions: []identity.Action{
			identity.ActionServerManageAccess,
			identity.ActionNamespaceManageAccess,
			identity.ActionCollectionManageAccess,
			identity.ActionUnitManageAccess,
		},
		Resolve:      h.resolveGrantObject,
		ActionsFor:   grantManageActionsFor,
		OpaqueDenial: false,
		Serve:        h.serveRevokeGrantByID,
	})

	return out
}

// resolveCollectionPath returns the collection that the path names.
func resolveCollectionPath(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	ns, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		return identity.Object{}, err
	}

	obj, err := identity.ParseObject(ns.String() + "/" + r.PathValue("collection"))
	if err != nil {
		return identity.Object{}, err
	}

	if !obj.IsCollection() {
		return identity.Object{}, identity.ErrInvalidObject
	}

	return obj, nil
}

// resolveUnitQuery returns the unit that the namespace in the path and the
// query parameters tool and unit name.
func resolveUnitQuery(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	ns, err := namespace.ParseName(r.PathValue("namespace"))
	if err != nil {
		return identity.Object{}, err
	}

	q := queryOf(r)

	tool, name := q.Get("tool"), q.Get("unit")
	if tool == "" || name == "" {
		return identity.Object{}, errUnitQuery
	}

	return identity.UnitObject(tool, ns, name)
}

// listRoles serves GET /api/server/roles: the built-in roles in catalog
// order, then the custom roles by name.
func listRoles(
	_ http.ResponseWriter, r *http.Request, svc *access.Service, _ *call,
) (principalResult, error) {
	roles, err := svc.ListRoles(r.Context())
	if err != nil {
		return failed(""), err
	}

	out := roleListResponse{Roles: make([]roleResponse, 0, len(roles))}
	for i := range roles {
		out.Roles = append(out.Roles, roleOf(roles[i]))
	}

	return ok(http.StatusOK, out, ""), nil
}

// getRole serves GET /api/server/roles/{id}. An unknown ID answers 404.
func getRole(
	_ http.ResponseWriter, r *http.Request, svc *access.Service, _ *call,
) (principalResult, error) {
	role, err := svc.GetRole(r.Context(), r.PathValue("id"))
	if err != nil {
		return failed(""), err
	}

	return ok(http.StatusOK, roleOf(role), ""), nil
}

// actionsOf maps the action names of a body to actions.
func actionsOf(names []string) []identity.Action {
	out := make([]identity.Action, 0, len(names))
	for _, n := range names {
		out = append(out, identity.Action(n))
	}

	return out
}

// createRole serves POST /api/server/roles: it answers 201 with the new
// custom role. A name that another role holds without regard to case
// answers 409. A bad name, an unknown action or a bad body answers 400.
func createRole(
	w http.ResponseWriter, r *http.Request, svc *access.Service, c *call,
) (principalResult, error) {
	var req roleRequest
	if err := decodeStrict(w, r, &req, errRoleBody); err != nil {
		return failed("roles"), err
	}

	if req.Name == nil || req.Actions == nil {
		return failed("roles"), errRoleBody
	}

	in := access.RoleInput{Name: *req.Name, Description: "", Actions: actionsOf(*req.Actions)}
	if req.Description != nil {
		in.Description = *req.Description
	}

	role, err := svc.CreateRole(r.Context(), c.e, c.p, in)
	if err != nil {
		return failed("roles"), err
	}

	return ok(http.StatusCreated, roleOf(role), access.RoleRef(role.ID)), nil
}

// changeRole serves PATCH /api/server/roles/{id}: it changes the name, the
// description or the actions of a custom role, and answers 200 with the
// stored role. A built-in role answers 409.
func changeRole(
	w http.ResponseWriter, r *http.Request, svc *access.Service, c *call,
) (principalResult, error) {
	id := r.PathValue("id")
	object := access.RoleRef(id)

	var req roleRequest

	err := decodeChange(w, r, &req, errRoleChangeBody, func() bool {
		return req.Name == nil && req.Description == nil && req.Actions == nil
	})
	if err != nil {
		return failed(object), err
	}

	current, err := svc.GetRole(r.Context(), id)
	if err != nil {
		return failed(object), err
	}

	in := access.RoleInput{Name: current.Name, Description: current.Description, Actions: current.Actions}

	if req.Name != nil {
		in.Name = *req.Name
	}

	if req.Description != nil {
		in.Description = *req.Description
	}

	if req.Actions != nil {
		in.Actions = actionsOf(*req.Actions)
	}

	role, err := svc.UpdateRole(r.Context(), c.e, c.p, id, in)
	if err != nil {
		return failed(object), err
	}

	return ok(http.StatusOK, roleOf(role), object), nil
}

// deleteRole serves DELETE /api/server/roles/{id}: it answers 204. A
// built-in role, or a custom role that a grant holds, answers 409.
func deleteRole(
	_ http.ResponseWriter, r *http.Request, svc *access.Service, c *call,
) (principalResult, error) {
	object := access.RoleRef(r.PathValue("id"))

	if err := svc.DeleteRole(r.Context(), c.e, r.PathValue("id")); err != nil {
		return failed(object), err
	}

	return ok(http.StatusNoContent, nil, object), nil
}

// listGrants serves GET on a grants path: one page of the current grants on
// the object, ordered by subject, then role, each with its holder. The
// query value holder keeps the grants of one subject
// ("principal:<id>" or "group:<id>#member"), and role keeps the grants of
// one role.
func listGrants(
	_ http.ResponseWriter, r *http.Request, svc *access.Service, c *call,
) (principalResult, error) {
	req, err := pageRequest(r)
	if err != nil {
		return failed(""), err
	}

	q := queryOf(r)

	page, err := svc.ListGrants(r.Context(), c.obj, access.PageRequest{
		Cursor: req.Cursor, Holder: q.Get("holder"), Role: q.Get("role"), Limit: req.Limit,
	})
	if err != nil {
		return failed(""), err
	}

	out := grantListResponse{
		Grants: make([]listedGrantResponse, 0, len(page.Grants)), NextCursor: page.NextCursor, Total: page.Total,
	}
	for i := range page.Grants {
		l := &page.Grants[i]
		out.Grants = append(out.Grants, listedGrantResponse{
			grantResponse: grantOf(l.Grant), HolderName: l.Holder.Name, HolderKind: l.Holder.Kind,
		})
	}

	return ok(http.StatusOK, out, ""), nil
}

// createGrant serves POST on a grants path: it gives the role of the body
// to the subject of the body on the object, and answers 201 with the grant.
// A grant that gives an action the caller does not hold on the object
// answers 403. An unknown subject, role or namespace answers 404, and a
// grant that exists 409.
func createGrant(
	w http.ResponseWriter, r *http.Request, svc *access.Service, c *call,
) (principalResult, error) {
	object := "grants " + c.obj.String()

	var req grantRequest
	if err := decodeStrict(w, r, &req, errGrantBody); err != nil {
		return failed(object), err
	}

	if req.Subject == nil || req.Role == nil {
		return failed(object), errGrantBody
	}

	subject, err := access.ParseSubject(*req.Subject)
	if err != nil {
		return failed(object), err
	}

	g, err := svc.Grant(r.Context(), c.e, c.p, access.GrantInput{Object: c.obj, Role: *req.Role, Subject: subject})
	if err != nil {
		return failed(strings.Join([]string{object, subject.String(), roleText(*req.Role)}, " ")), err
	}

	return ok(http.StatusCreated, grantOf(g), access.GrantRef(g)), nil
}

// roleText returns role when it can be a role ID, and "invalid" otherwise,
// so request text never reaches the audit log.
func roleText(role string) string {
	if ident.Valid(role, maxRoleIDLength) {
		return role
	}

	return "invalid"
}

// maxRoleIDLength bounds a role ID in an audit object, in bytes.
const maxRoleIDLength = 64

// revokeGrant serves DELETE on a grant path: it ends the grant and answers
// 204. The row stays for the history. A grant on another object answers
// 404.
func revokeGrant(
	_ http.ResponseWriter, r *http.Request, svc *access.Service, c *call,
) (principalResult, error) {
	g, err := svc.Revoke(r.Context(), c.e, c.p, c.obj, r.PathValue("id"))
	if err != nil {
		return failed(access.GrantIDRef(r.PathValue("id"))), err
	}

	return ok(http.StatusNoContent, nil, access.GrantRef(g)), nil
}

// grantActions holds the manage-access action and the read action of a
// grant's object.
type grantActions struct {
	manage identity.Action
	read   identity.Action
}

// grantAccessActions returns the manage-access action and the read action
// of the object of a grant. The read action is what lets a caller see the
// grant: server:read-access on the installation, else the read action of
// the namespace, the collection or the unit.
func grantAccessActions(obj identity.Object) (grantActions, bool) {
	kind, isGrant := access.KindOf(obj)
	if !isGrant {
		return grantActions{manage: "", read: ""}, false
	}

	switch kind {
	case access.ObjectInstallation, access.ObjectPrincipal:
		return grantActions{
			manage: identity.ActionServerManageAccess,
			read:   identity.ActionServerReadAccess,
		}, true
	case access.ObjectNamespace:
		return grantActions{
			manage: identity.ActionNamespaceManageAccess,
			read:   identity.ActionNamespaceRead,
		}, true
	case access.ObjectCollection:
		return grantActions{
			manage: identity.ActionCollectionManageAccess,
			read:   identity.ActionCollectionRead,
		}, true
	case access.ObjectUnit:
		return grantActions{
			manage: identity.ActionUnitManageAccess,
			read:   identity.ActionUnitRead,
		}, true
	default:
		return grantActions{manage: "", read: ""}, false
	}
}

// grantManageActionsFor returns the manage-access action of the resolved
// object of DELETE /api/server/grants/{id}: server:manage-access on the
// installation and on a service principal, else the manage-access action of
// the namespace, the collection or the unit.
func grantManageActionsFor(obj identity.Object) []identity.Action {
	if actions, known := grantAccessActions(obj); known {
		return []identity.Action{actions.manage}
	}

	return []identity.Action{identity.ActionServerManageAccess}
}

// resolveGrantObject returns the object of the grant in the path, for the
// route check of DELETE /api/server/grants/{id}. An unknown grant answers
// 404. A grant the caller may not see answers 404 with the same body,
// never 403: the caller must hold the read action of the grant's object.
// A request without the caller that enforce stores answers 401, so the
// visibility check never runs without a caller. The route check authorizes
// the manage-access action of the returned object.
func (h *Handler) resolveGrantObject(_ http.ResponseWriter, r *http.Request) (identity.Object, error) {
	if h.access == nil {
		return identity.ServerObject(), nil
	}

	id := r.PathValue("id")

	g, err := h.access.GetGrant(r.Context(), id)
	if errors.Is(err, access.ErrNotFound) {
		return identity.Object{}, fmt.Errorf("revoke grant %q: %w", id, access.ErrNotFound)
	}

	if err != nil {
		return identity.Object{}, fmt.Errorf("revoke grant %q: %w", id, err)
	}

	actions, known := grantAccessActions(g.Object)
	if !known {
		return identity.Object{}, fmt.Errorf("revoke grant %q: %w", id, access.ErrNotFound)
	}

	p, ok := principalFromRequest(r)
	if !ok {
		return identity.Object{}, fmt.Errorf("revoke grant %q: %w", id, identity.ErrUnauthenticated)
	}

	if authErr := h.guard.Authorize(r.Context(), r, p, actions.read, g.Object); authErr != nil {
		if errors.Is(authErr, identity.ErrDenied) {
			return identity.Object{}, fmt.Errorf("revoke grant %q: %w", id, access.ErrNotFound)
		}

		return identity.Object{}, fmt.Errorf("authorize %s: %w", actions.read, authErr)
	}

	return g.Object, nil
}

// serveRevokeGrantByID serves DELETE /api/server/grants/{id}: it ends any
// grant by its ID, whatever its object, and answers 204. The row stays for
// the history. The wrapper already checked visibility and authorized the
// manage-access action of the grant's object, which the resolver returned.
// The Service refuses a grant that gives more than the caller holds. A
// refused revoke writes one failure entry in the scope of the grant, as the
// other grant changes do.
func (h *Handler) serveRevokeGrantByID(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	if h.access == nil {
		h.writeError(ctx, w, errNoAccessStore)

		return
	}

	id := r.PathValue("id")

	actions, known := grantAccessActions(obj)
	if !known {
		h.writeError(ctx, w, fmt.Errorf("revoke grant %q: %w", id, access.ErrNotFound))

		return
	}

	revoked, err := h.access.RevokeByID(ctx, callerEntry(r, p, actions.manage), p, id)
	if err != nil {
		h.recordEscalation(ctx, r, p, denials.Denial{
			Action: actions.manage, Object: obj, Reason: "", ReasonWithin: nil, Window: 0,
		}, err)
		h.writeError(ctx, w, err)
		h.appendObjectChange(r, p, obj, serverChange{
			at: h.access.Now(), action: actions.manage, object: access.GrantIDRef(id),
			outcome: audit.OutcomeFailure,
		})

		return
	}

	h.writePrincipalResult(w, r, ok(http.StatusNoContent, nil, access.GrantRef(revoked)))
}

// classifyAccess maps the role and grant errors to their status and
// message: a bad body or role answers 400, an escalation 403, an unknown
// role or grant 404, a taken name or grant, a built-in role, a role in use,
// the end of the last site administrator and a unit that moved to another
// collection during the request 409, and a store with no access or review port 501.
func classifyAccess(err error) (failure, bool) {
	switch {
	case errors.Is(err, errRoleBody), errors.Is(err, errRoleChangeBody), errors.Is(err, errGrantBody),
		errors.Is(err, errUnitQuery), errors.Is(err, errReviewQuery), errors.Is(err, errCallerAccessQuery),
		errors.Is(err, errExpandQuery),
		errors.Is(err, access.ErrInvalid):
		return fail(http.StatusBadRequest, err.Error()), true
	case errors.Is(err, access.ErrEscalation):
		return fail(http.StatusForbidden, err.Error()), true
	case errors.Is(err, access.ErrNotFound):
		return fail(http.StatusNotFound, err.Error()), true
	case errors.Is(err, access.ErrAlreadyExists), errors.Is(err, access.ErrBuiltIn), errors.Is(err, access.ErrInUse),
		errors.Is(err, access.ErrUnitMoved), errors.Is(err, access.ErrLastSiteAdministrator):
		return fail(http.StatusConflict, err.Error()), true
	case errors.Is(err, errNoAccessStore), errors.Is(err, access.ErrNoReview),
		errors.Is(err, access.ErrCollectionReview):
		return fail(http.StatusNotImplemented, err.Error()), true
	default:
		return fail(0, ""), false
	}
}
