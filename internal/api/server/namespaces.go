package server

// Namespace routes of the installation API: create a namespace, and list
// the namespaces the caller may read.
//
// POST /api/server/namespaces creates a namespace homed in the region of
// the server. The bootstrap admin token calls it on an empty installation
// for the first namespace. It needs the action server:create-namespace on
// the installation. A creator with a stored principal holds the
// namespace-admin role on the new namespace: the grant and the namespace
// commit in one transaction, and the grant writes an audit entry.
//
// GET /api/server/namespaces lists the namespaces where the caller holds
// namespace:read, in name order, one page at a time. The caller needs only
// caller:read. A grant on the namespace or on the installation puts the
// namespace in the list; a namespace that the caller reaches only through
// a collection or a unit grant is not listed. Paging is by name with an
// opaque cursor: limit names the page size, and cursor is the nextCursor of
// the page before. The total is on the first page only, and null on the
// pages after it. The list writes no audit entry. With no access service
// only administrators list (all namespaces) and everybody else gets an
// empty page.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// errInvalidNamespaceBody reports a namespace create body that is not valid JSON
// with a name. classify maps it to 400.
var errInvalidNamespaceBody = errors.New(`invalid namespace body: provide {"name":"..."}`)

// maxNamespaceBodyBytes bounds a namespace create body the resolver reads
// before the handler. A name holds at most 100 characters, so this is ample.
const maxNamespaceBodyBytes = 8192

// createNamespaceRequest is the create body: the name of the namespace.
type createNamespaceRequest struct {
	Name string `json:"name"`
}

// createNamespaceResponse answers a created namespace: its name, home region,
// failover version and creation time. Times are UTC in RFC 3339 with
// nanoseconds, the wire form of backup times.
//
// Contract: never remove or rename these fields. The command and the console
// read only this API, and they update on their own schedule.
type createNamespaceResponse struct {
	Name            string `json:"name"`
	HomeRegion      string `json:"homeRegion"`
	FailoverVersion int64  `json:"failoverVersion"`
	CreatedAt       string `json:"createdAt"`
}

// decodeNamespaceName reads body as a create request and answers the parsed
// namespace name.
func decodeNamespaceName(body []byte) (namespace.Name, error) {
	var req createNamespaceRequest

	if err := json.Unmarshal(body, &req); err != nil {
		return "", errInvalidNamespaceBody
	}

	name, err := namespace.ParseName(req.Name)
	if err != nil {
		return "", err
	}

	return name, nil
}

// resolveCreateNamespace returns the object the create acts on: the
// installation. It reads the body with http.MaxBytesReader and restores
// r.Body before returning, so the handler can read it again. A missing or
// bad name fails before authorization runs.
func resolveCreateNamespace(w http.ResponseWriter, r *http.Request) (identity.Object, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxNamespaceBodyBytes))
	if err != nil {
		return identity.Object{}, errInvalidNamespaceBody
	}

	r.Body = io.NopCloser(bytes.NewReader(body))

	if _, err := decodeNamespaceName(body); err != nil {
		return identity.Object{}, err
	}

	return identity.ServerObject(), nil
}

// createNamespace serves POST /api/server/namespaces: it creates a namespace
// homed in the region of the server and answers 201 with it. A duplicate
// answers 409. A bad name answers 400.
func (h *Handler) createNamespace(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxNamespaceBodyBytes))
	if err != nil {
		h.writeError(ctx, w, errInvalidNamespaceBody)

		return
	}

	name, err := decodeNamespaceName(body)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	ns, err := h.storeNamespace(r, p, name)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusCreated, createNamespaceResponse{
		Name:            ns.Name.String(),
		HomeRegion:      ns.HomeRegion.String(),
		FailoverVersion: ns.FailoverVersion,
		CreatedAt:       ns.CreatedAt.UTC().Format(time.RFC3339Nano),
	})
}

// storeNamespace stores the namespace name. With an access service, the
// namespace, the namespace-admin grant of the creator and the audit entry
// commit together.
func (h *Handler) storeNamespace(
	r *http.Request,
	p identity.Principal,
	name namespace.Name,
) (namespace.Namespace, error) {
	if h.access == nil {
		ns, err := h.namespaces.Create(r.Context(), name)
		if err != nil {
			return namespace.Namespace{}, fmt.Errorf("create namespace: %w", err)
		}

		return ns, nil
	}

	ns := h.namespaces.New(name)

	e := callerEntry(r, p, identity.ActionServerCreateNamespace)
	if _, err := h.access.CreateNamespace(r.Context(), e, p, ns); err != nil {
		return namespace.Namespace{}, fmt.Errorf("create namespace: %w", err)
	}

	return ns, nil
}

// errNamespaceCursor reports a cursor that does not decode to a namespace
// name. classify maps it to 400.
var errNamespaceCursor = errors.New("invalid cursor: provide the nextCursor of the page before")

// listNamespacesResponse is one page of GET /api/server/namespaces.
// Namespaces holds the page in name order, never null. NextCursor is empty
// on the last page. Total is the count of every visible namespace, on the
// first page only, and null on the pages after it.
//
// Contract: never remove or rename these fields. The console reads only
// this API, and it updates on its own schedule.
type listNamespacesResponse struct {
	Namespaces []createNamespaceResponse `json:"namespaces"`
	NextCursor string                    `json:"nextCursor"`
	Total      *int64                    `json:"total"`
}

// namespaceListQuery is one validated list request: the page size, the name
// after which the page starts, and whether it is the first page.
type namespaceListQuery struct {
	limit int
	after namespace.Name
	first bool
}

// listNamespaces serves GET /api/server/namespaces: one page of the
// namespaces where the caller holds namespace:read. A bad limit or cursor
// answers 400. A store without the paging port answers 501.
func (h *Handler) listNamespaces(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	query, err := parseNamespaceList(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	filter, empty, err := h.visibleNamespaces(ctx, p)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if empty {
		var total *int64

		if query.first {
			var zero int64

			total = &zero
		}

		h.writeJSON(ctx, w, http.StatusOK, emptyNamespacePage(total))

		return
	}

	out, err := h.readNamespacePage(ctx, query, filter)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, out)
}

// parseNamespaceList reads the limit and the cursor of a list request: the
// page size and the name after which the page starts.
func parseNamespaceList(r *http.Request) (namespaceListQuery, error) {
	var none namespaceListQuery

	req, err := pageRequest(r)
	if err != nil {
		return none, err
	}

	limit := req.Limit
	if limit == 0 {
		limit = access.DefaultLimit
	}

	if limit < 1 || limit > access.MaxLimit {
		return none, errPageQuery
	}

	after, first, err := decodeNamespaceCursor(req.Cursor)
	if err != nil {
		return none, err
	}

	return namespaceListQuery{limit: limit, after: after, first: first}, nil
}

// readNamespacePage reads one page of the namespaces of filter: at most the
// page size rows after the cursor, with the total on the first page only.
func (h *Handler) readNamespacePage(
	ctx context.Context, query namespaceListQuery, filter namespace.Filter,
) (listNamespacesResponse, error) {
	page, err := h.namespaces.PageNamespaces(ctx, namespace.PageQuery{
		After: query.after, Limit: query.limit + 1, Filter: filter,
	})
	if err != nil {
		return listNamespacesResponse{}, err
	}

	nextCursor := ""

	if len(page) > query.limit {
		page = page[:query.limit]
		nextCursor = base64.RawURLEncoding.EncodeToString([]byte(page[len(page)-1].Name.String()))
	}

	var total *int64

	if query.first {
		n, err := h.namespaces.CountNamespaces(ctx, filter)
		if err != nil {
			return listNamespacesResponse{}, err
		}

		total = &n
	}

	out := listNamespacesResponse{
		Namespaces: make([]createNamespaceResponse, 0, len(page)), NextCursor: nextCursor, Total: total,
	}
	for i := range page {
		out.Namespaces = append(out.Namespaces, createNamespaceResponse{
			Name:            page[i].Name.String(),
			HomeRegion:      page[i].HomeRegion.String(),
			FailoverVersion: page[i].FailoverVersion,
			CreatedAt:       page[i].CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}

	return out, nil
}

// decodeNamespaceCursor reads a cursor of listNamespaces: empty means the
// first page, otherwise the base64url name of the last namespace of the
// page before. It answers whether the request asks for the first page.
func decodeNamespaceCursor(cursor string) (namespace.Name, bool, error) {
	if cursor == "" {
		return "", true, nil
	}

	if len(cursor) > principal.MaxCursorLength {
		return "", false, errNamespaceCursor
	}

	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return "", false, errNamespaceCursor
	}

	name, err := namespace.ParseName(string(raw))
	if err != nil {
		return "", false, errNamespaceCursor
	}

	return name, false, nil
}

// visibleNamespaces answers the filter of the namespaces where p holds
// namespace:read. Empty is true when no namespace can qualify, so the
// caller answers the empty page without calling the store. With no access
// service only an administrator sees every namespace. The filter runs in
// the SQL query, never after paging.
func (h *Handler) visibleNamespaces(
	ctx context.Context, p identity.Principal,
) (namespace.Filter, bool, error) {
	none := namespace.Filter{All: false, Names: nil}

	if h.access == nil {
		if p.Admin {
			return namespace.Filter{All: true, Names: nil}, false, nil
		}

		return none, true, nil
	}

	scope, err := h.access.Authorizer().ListObjects(ctx, p, identity.ActionNamespaceRead)
	if err != nil {
		return none, false, err
	}

	if scope.All {
		return namespace.Filter{All: true, Names: nil}, false, nil
	}

	names := make([]namespace.Name, 0, len(scope.Namespaces))
	for _, obj := range scope.Namespaces {
		names = append(names, obj.Namespace())
	}

	slices.Sort(names)
	names = slices.Compact(names)

	if len(names) == 0 {
		return none, true, nil
	}

	return namespace.Filter{All: false, Names: names}, false, nil
}

// emptyNamespacePage answers the page of a caller that sees no namespace:
// no rows and no next page. Total is 0 on the first page and null after it,
// so the caller passes what the request asked for.
func emptyNamespacePage(total *int64) listNamespacesResponse {
	return listNamespacesResponse{Namespaces: []createNamespaceResponse{}, NextCursor: "", Total: total}
}
