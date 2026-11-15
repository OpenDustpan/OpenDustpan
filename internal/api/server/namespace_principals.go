package server

// Routes of the service principals of a namespace. A principal of a
// namespace can only hold grants inside its namespace, and it is a member
// of no group. Each route acts on the namespace in the path:
//
//   - namespace:read-access reads the principals of the namespace, because
//     the principals and their names are access data, as grants are,
//   - namespace:manage-principals creates, changes and deletes them.
//
// Each change writes its audit entry in the audit log of the namespace, in
// the transaction of the change. A principal of another namespace, or of
// the installation, answers 404 on these paths.

import (
	"errors"
	"net/http"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// Route paths of the principals of a namespace.
const (
	namespacePrincipalsPath = "/api/server/namespaces/{namespace}/principals"
	namespacePrincipalPath  = "/api/server/namespaces/{namespace}/principals/{id}"
)

// errNamespacePrincipalBody is the body error of a create. classify maps it
// to 400.
var errNamespacePrincipalBody = errors.New(
	`invalid principal body: provide {"subject":"...","displayName":"..."}; a principal of a namespace is a service`,
)

// createNamespacePrincipalRequest is the body of a create.
type createNamespacePrincipalRequest struct {
	Subject     *string `json:"subject"`
	DisplayName *string `json:"displayName"`
}

// namespacePrincipalOp is one operation on the principals of the namespace
// in c.obj.
type namespacePrincipalOp func(
	w http.ResponseWriter, r *http.Request, svc *principal.Service, c *call,
) (principalResult, error)

// namespacePrincipalRoutes returns the rows of the principals of a
// namespace.
func (h *Handler) namespacePrincipalRoutes() []Route {
	read := []identity.Action{identity.ActionNamespaceReadAccess}
	manage := []identity.Action{identity.ActionNamespaceManagePrincipals}
	change := func(op namespacePrincipalOp) RouteHandler {
		return h.serveNamespacePrincipalChange(op)
	}

	return []Route{
		{
			Method:       http.MethodPost,
			Path:         namespacePrincipalsPath,
			Scope:        ScopeAuthorized,
			Actions:      manage,
			Resolve:      resolveNamespacePath,
			ActionsFor:   nil,
			OpaqueDenial: false,
			Serve:        change(createNamespacePrincipal),
		},
		{
			Method:     http.MethodGet,
			Path:       namespacePrincipalsPath,
			Scope:      ScopeAuthorized,
			Actions:    read,
			Resolve:    resolveNamespacePath,
			ActionsFor: nil, OpaqueDenial: false,
			Serve: h.serveNamespacePrincipalRead(listNamespacePrincipals),
		},
		{
			Method:       http.MethodGet,
			Path:         namespacePrincipalPath,
			Scope:        ScopeAuthorized,
			Actions:      read,
			Resolve:      resolveNamespacePath,
			ActionsFor:   nil,
			OpaqueDenial: false,
			Serve:        h.serveNamespacePrincipalRead(getNamespacePrincipal),
		},
		{
			Method:       http.MethodPatch,
			Path:         namespacePrincipalPath,
			Scope:        ScopeAuthorized,
			Actions:      manage,
			Resolve:      resolveNamespacePath,
			ActionsFor:   nil,
			OpaqueDenial: false,
			Serve:        change(changeNamespacePrincipal),
		},
		{
			Method:       http.MethodDelete,
			Path:         namespacePrincipalPath,
			Scope:        ScopeAuthorized,
			Actions:      manage,
			Resolve:      resolveNamespacePath,
			ActionsFor:   nil,
			OpaqueDenial: false,
			Serve:        change(deleteNamespacePrincipal),
		},
	}
}

// serveNamespacePrincipalRead adapts a read to a route handler.
func (h *Handler) serveNamespacePrincipalRead(op namespacePrincipalOp) RouteHandler {
	return func(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
		var none audit.Entry

		if res, ok := h.runNamespacePrincipalOp(w, r, op, &call{p: p, obj: obj, e: none}); ok {
			h.writePrincipalResult(w, r, res)
		}
	}
}

// serveNamespacePrincipalChange adapts a change to a route handler. The
// Service writes the success entry in the transaction of the change. A
// refused change writes one failure entry in the audit log of the
// namespace, best effort.
func (h *Handler) serveNamespacePrincipalChange(op namespacePrincipalOp) RouteHandler {
	action := identity.ActionNamespaceManagePrincipals

	return func(w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object) {
		res, ok := h.runNamespacePrincipalOp(w, r, op, &call{p: p, obj: obj, e: callerEntry(r, p, action)})

		if !ok && res.object != "" && h.principals != nil {
			h.appendObjectChange(r, p, obj, serverChange{
				at: h.principals.Now(), action: action, object: res.object, outcome: audit.OutcomeFailure,
			})
		}

		if ok {
			h.writePrincipalResult(w, r, res)
		}
	}
}

// runNamespacePrincipalOp runs op and writes its error. It reports whether
// op succeeded, with the result in both cases.
func (h *Handler) runNamespacePrincipalOp(
	w http.ResponseWriter, r *http.Request, op namespacePrincipalOp, c *call,
) (principalResult, bool) {
	ctx := r.Context()

	if h.principals == nil {
		h.writeError(ctx, w, errNoPrincipalStore)

		return failed(""), false
	}

	res, err := op(w, r, h.principals, c)
	if err != nil {
		h.writeError(ctx, w, err)

		return res, false
	}

	return res, true
}

// createNamespacePrincipal serves POST
// /api/server/namespaces/{namespace}/principals: it answers 201 with the
// new service principal. A taken subject answers 409, and a bad or reserved
// subject or a bad body 400.
func createNamespacePrincipal(
	w http.ResponseWriter, r *http.Request, svc *principal.Service, c *call,
) (principalResult, error) {
	var req createNamespacePrincipalRequest
	if err := decodeStrict(w, r, &req, errNamespacePrincipalBody); err != nil {
		return failed("principals"), err
	}

	if req.Subject == nil {
		return failed("principals"), errNamespacePrincipalBody
	}

	in := principal.NamespaceInput{Subject: *req.Subject, DisplayName: ""}
	if req.DisplayName != nil {
		in.DisplayName = *req.DisplayName
	}

	p, err := svc.CreateNamespacePrincipal(r.Context(), c.e, c.obj.Namespace(), in)
	if err != nil {
		return failed("principals"), err
	}

	return ok(http.StatusCreated, principalOf(p), principal.Ref(p.ID)), nil
}

// listNamespacePrincipals serves GET
// /api/server/namespaces/{namespace}/principals: one page, ordered by
// subject, with the query parameters limit, cursor and prefix.
func listNamespacePrincipals(
	_ http.ResponseWriter, r *http.Request, svc *principal.Service, c *call,
) (principalResult, error) {
	req, err := pageRequest(r)
	if err != nil {
		return failed(""), err
	}

	page, err := svc.ListNamespacePrincipals(r.Context(), c.obj.Namespace(), req)
	if err != nil {
		return failed(""), err
	}

	out := principalListResponse{
		Principals: make([]principalResponse, 0, len(page.Items)), NextCursor: page.NextCursor, Total: page.Total,
	}
	for i := range page.Items {
		out.Principals = append(out.Principals, principalOf(page.Items[i]))
	}

	return ok(http.StatusOK, out, ""), nil
}

// getNamespacePrincipal serves GET
// /api/server/namespaces/{namespace}/principals/{id}.
func getNamespacePrincipal(
	_ http.ResponseWriter, r *http.Request, svc *principal.Service, c *call,
) (principalResult, error) {
	p, err := svc.GetNamespacePrincipal(r.Context(), c.obj.Namespace(), r.PathValue("id"))
	if err != nil {
		return failed(""), err
	}

	return ok(http.StatusOK, principalOf(p), ""), nil
}

// changeNamespacePrincipal serves PATCH
// /api/server/namespaces/{namespace}/principals/{id}: it changes the
// display name and answers 200 with the stored principal.
func changeNamespacePrincipal(
	w http.ResponseWriter, r *http.Request, svc *principal.Service, c *call,
) (principalResult, error) {
	id := r.PathValue("id")
	object := principal.Ref(id)

	var req changePrincipalRequest

	err := decodeChange(w, r, &req, errPrincipalChangeBody, func() bool { return req.DisplayName == nil })
	if err != nil {
		return failed(object), err
	}

	p, err := svc.UpdateNamespacePrincipal(r.Context(), c.e, c.obj.Namespace(), id,
		principal.Change{DisplayName: req.DisplayName})
	if err != nil {
		return failed(object), err
	}

	return ok(http.StatusOK, principalOf(p), object), nil
}

// deleteNamespacePrincipal serves DELETE
// /api/server/namespaces/{namespace}/principals/{id}: it ends the
// principal and its grants, and answers 204.
func deleteNamespacePrincipal(
	_ http.ResponseWriter, r *http.Request, svc *principal.Service, c *call,
) (principalResult, error) {
	id := r.PathValue("id")
	object := principal.Ref(id)

	if err := svc.DeleteNamespacePrincipal(r.Context(), c.e, c.p, c.obj.Namespace(), id); err != nil {
		return failed(object), err
	}

	return ok(http.StatusNoContent, nil, object), nil
}
