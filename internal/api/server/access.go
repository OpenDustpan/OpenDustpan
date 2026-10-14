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
// another. The grant lists take limit and cursor, and answer total and
// nextCursor.
//
// Each change writes its audit entry in the transaction of the change (fail
// closed): in the namespace of a grant on a namespace, a collection or a
// unit, and in @server otherwise. A refused change writes a failure entry
// after the fact in the same scope, as well as possible.

import (
	"errors"
	"net/http"
	"strings"

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
		h.writeError(ctx, w, err)

		return res, false
	}

	return res, true
}

// Route paths of the roles and the grants.
const (
	rolesPath            = "/api/server/roles"
	rolePath             = "/api/server/roles/{id}"
	serverGrantsPath     = "/api/server/grants"
	namespaceGrantsPath  = "/api/server/namespaces/{namespace}/grants"
	collectionGrantsPath = "/api/server/namespaces/{namespace}/collections/{collection}/grants"
	unitGrantsPath       = "/api/server/namespaces/{namespace}/unit-grants"
	grantIDSuffix        = "/{id}"
)

// roleRoutes returns the role rows of the route table.
func (h *Handler) roleRoutes() []Route {
	read := []identity.Action{identity.ActionRoleRead}
	manage := []identity.Action{identity.ActionServerManageRoles}

	return []Route{
		{
			Method: http.MethodGet, Path: rolesPath, Scope: ScopeAuthorized,
			Actions: read, Resolve: resolveServer, Serve: h.serveAccessRead(listRoles),
		},
		{
			Method: http.MethodGet, Path: rolePath, Scope: ScopeAuthorized,
			Actions: read, Resolve: resolveServer, Serve: h.serveAccessRead(getRole),
		},
		{
			Method: http.MethodPost, Path: rolesPath, Scope: ScopeAuthorized, Actions: manage,
			Resolve: resolveServer, Serve: h.serveAccessChange(identity.ActionServerManageRoles, createRole),
		},
		{
			Method: http.MethodPatch, Path: rolePath, Scope: ScopeAuthorized, Actions: manage,
			Resolve: resolveServer, Serve: h.serveAccessChange(identity.ActionServerManageRoles, changeRole),
		},
		{
			Method: http.MethodDelete, Path: rolePath, Scope: ScopeAuthorized, Actions: manage,
			Resolve: resolveServer, Serve: h.serveAccessChange(identity.ActionServerManageRoles, deleteRole),
		},
	}
}

// grantRoutes returns the grant rows of the route table: a list, a grant
// and a revoke on each object kind.
func (h *Handler) grantRoutes() []Route {
	kinds := []struct {
		resolve ObjectResolver
		path    string
		action  identity.Action
	}{
		{resolveServer, serverGrantsPath, identity.ActionServerManageAccess},
		{resolveNamespacePath, namespaceGrantsPath, identity.ActionNamespaceManageAccess},
		{resolveCollectionPath, collectionGrantsPath, identity.ActionCollectionManageAccess},
		{resolveUnitQuery, unitGrantsPath, identity.ActionUnitManageAccess},
	}

	const routesPerKind = 3 // a list, a grant and a revoke

	out := make([]Route, 0, routesPerKind*len(kinds))

	for _, k := range kinds {
		actions := []identity.Action{k.action}

		out = append(out,
			Route{
				Method: http.MethodGet, Path: k.path, Scope: ScopeAuthorized,
				Actions: actions, Resolve: k.resolve, Serve: h.serveAccessRead(listGrants),
			},
			Route{
				Method: http.MethodPost, Path: k.path, Scope: ScopeAuthorized,
				Actions: actions, Resolve: k.resolve, Serve: h.serveAccessChange(k.action, createGrant),
			},
			Route{
				Method: http.MethodDelete, Path: k.path + grantIDSuffix, Scope: ScopeAuthorized,
				Actions: append([]identity.Action(nil), actions...), Resolve: k.resolve,
				Serve: h.serveAccessChange(k.action, revokeGrant),
			},
		)
	}

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

	q := r.URL.Query()

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

	q := r.URL.Query()

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

// classifyAccess maps the role and grant errors to their status and
// message: a bad body or role answers 400, an escalation 403, an unknown
// role or grant 404, a taken name or grant, a built-in role and a role in
// use 409, and a store with no access or review port 501.
func classifyAccess(err error) (int, string, bool) {
	switch {
	case errors.Is(err, errRoleBody), errors.Is(err, errRoleChangeBody), errors.Is(err, errGrantBody),
		errors.Is(err, errUnitQuery), errors.Is(err, errReviewQuery), errors.Is(err, errCallerAccessQuery),
		errors.Is(err, errExpandQuery),
		errors.Is(err, access.ErrInvalid):
		return http.StatusBadRequest, err.Error(), true
	case errors.Is(err, access.ErrEscalation):
		return http.StatusForbidden, err.Error(), true
	case errors.Is(err, access.ErrNotFound):
		return http.StatusNotFound, err.Error(), true
	case errors.Is(err, access.ErrAlreadyExists), errors.Is(err, access.ErrBuiltIn), errors.Is(err, access.ErrInUse):
		return http.StatusConflict, err.Error(), true
	case errors.Is(err, errNoAccessStore), errors.Is(err, access.ErrNoReview),
		errors.Is(err, access.ErrCollectionReview):
		return http.StatusNotImplemented, err.Error(), true
	default:
		return 0, "", false
	}
}
