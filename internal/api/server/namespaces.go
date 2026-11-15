package server

// Namespace routes of the installation API: create a namespace, and list
// the namespaces the caller may read.
//
// POST /api/server/namespaces creates a namespace whose primary installation is the installation
// of the server. The bootstrap admin token calls it on an empty installation
// for the first namespace. It needs the action server:create-namespace on
// the installation. A creator with a stored principal holds the
// namespace-admin role on the new namespace: the grant and the namespace
// commit in one transaction, and the grant writes an audit entry.
//
// GET /api/server/namespaces lists the namespaces where the caller holds
// namespace:read, in the order of the name column of the store, one page at
// a time. The caller needs only caller:read. A grant on the namespace or on
// the installation puts the namespace in the list; a namespace that the
// caller reaches only through a collection or a unit grant is not listed.
// Paging is by name with an opaque, sealed cursor: limit names the page size,
// and cursor is the nextCursor of the page before. A cursor works only with
// the filter that made it, so a cursor of another query answers 400. With the
// cursor the order is stable. The total is on the first page only, and null
// on the pages after it. The list writes no audit entry. With no access
// service only administrators list (all namespaces) and everybody else gets
// an empty page.

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
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/pagetoken"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
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

// createNamespaceResponse answers a created namespace: its stable ID, name,
// primary installation, failover version and creation time. Times are UTC in RFC 3339 with
// nanoseconds, the wire form of backup times.
//
// Contract: never remove or rename these fields. The command and the console
// read only this API, and they update on their own schedule.
type createNamespaceResponse struct {
	ID                  string `json:"id"`
	Name                string `json:"name"`
	PrimaryInstallation string `json:"primaryInstallation"`
	FailoverVersion     int64  `json:"failoverVersion"`
	CreatedAt           string `json:"createdAt"`
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
	body, err := readBodyUpTo(w, r, maxNamespaceBodyBytes, errInvalidNamespaceBody)
	if err != nil {
		return identity.Object{}, err
	}

	r.Body = io.NopCloser(bytes.NewReader(body))

	if _, err := decodeNamespaceName(body); err != nil {
		return identity.Object{}, err
	}

	return identity.ServerObject(), nil
}

// createNamespace serves POST /api/server/namespaces: it creates a namespace
// with the installation of the server as primary installation and answers 201 with it. A duplicate
// answers 409. A bad name answers 400.
func (h *Handler) createNamespace(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	body, err := readBodyUpTo(w, r, maxNamespaceBodyBytes, errInvalidNamespaceBody)
	if err != nil {
		h.writeError(ctx, w, err)

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
		ID:                  ns.ID.String(),
		Name:                ns.Name.String(),
		PrimaryInstallation: ns.PrimaryInstallation.String(),
		FailoverVersion:     ns.FailoverVersion,
		CreatedAt:           ns.CreatedAt.UTC().Format(time.RFC3339Nano),
	})
}

// storeNamespace stores the namespace name. With an access service, the
// namespace, the namespace-admin grant of the creator and the audit entry
// commit together. It does not wrap the error: the error of either service
// already starts with "create namespace" and the name, and the 400 and 409
// answers send its text.
func (h *Handler) storeNamespace(
	r *http.Request,
	p identity.Principal,
	name namespace.Name,
) (namespace.Namespace, error) {
	if h.access == nil {
		return h.namespaces.Create(r.Context(), name)
	}

	ns := h.namespaces.New(name)

	e := callerEntry(r, p, identity.ActionServerCreateNamespace)
	if _, err := h.access.CreateNamespace(r.Context(), e, p, ns); err != nil {
		return namespace.Namespace{}, err
	}

	return ns, nil
}

// errNamespaceCursor reports a cursor that does not decode to a namespace
// name. classify maps it to 400.
var errNamespaceCursor = errors.New("invalid cursor: provide the nextCursor of the page before")

// errNoCursorSealer reports a handler without a cursor sealer. The list fails
// closed with this error, so it never answers an unsealed cursor.
var errNoCursorSealer = errors.New("namespace cursor sealer is not configured")

// namespaceCursorWire is the opaque cursor of the namespace list: the name of
// the last namespace of the page before, and a fingerprint of the filter.
type namespaceCursorWire struct {
	After       string `json:"a"`
	Fingerprint string `json:"f"`
}

// listNamespacesResponse is one page of GET /api/server/namespaces.
// Namespaces holds the page in the order of the name column of the store,
// never null. NextCursor is empty on the last page. Total is the count of
// every visible namespace, on the first page only, and null on the pages
// after it.
//
// Contract: never remove or rename these fields. The console reads only
// this API, and it updates on its own schedule.
type listNamespacesResponse struct {
	Namespaces []createNamespaceResponse `json:"namespaces"`
	NextCursor string                    `json:"nextCursor"`
	Total      *int64                    `json:"total"`
}

// namespaceListQuery is one validated list request: the page size, the
// sealed cursor of the page before, the name after which the page starts, and
// whether it is the first page.
type namespaceListQuery struct {
	limit  int
	cursor string
	after  namespace.Name
	first  bool
}

// listNamespaces serves GET /api/server/namespaces: one page of the
// namespaces where the caller holds namespace:read, in the order of the
// name column of the store. With the cursor the order is stable. A bad limit
// or cursor answers 400. A store without the paging port answers 501.
func (h *Handler) listNamespaces(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
	ctx := r.Context()

	filter, empty, err := h.visibleNamespaces(ctx, p)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	fingerprint := namespaceListFingerprint(filter)

	query, err := parseNamespaceList(r, fingerprint)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if query.cursor != "" {
		if query.after, err = h.openNamespaceCursor(ctx, fingerprint, query.cursor); err != nil {
			h.writeError(ctx, w, err)

			return
		}
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

	out, err := h.readNamespacePage(ctx, query, filter, fingerprint)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, http.StatusOK, out)
}

// namespaceListFingerprint returns a short digest of the filter of a
// namespace list, so a cursor works only with the query that made it. The
// page size is not part of it: a caller may change the limit between pages.
// Each kind of filter has its own prefix, so a filter of the one namespace
// "all" never gives the fingerprint of the filter of all namespaces.
func namespaceListFingerprint(filter namespace.Filter) string {
	if filter.All {
		return pagetoken.Fingerprint("all:")
	}

	names := make([]string, 0, len(filter.Names))
	for i := range filter.Names {
		names = append(names, filter.Names[i].String())
	}

	slices.Sort(names)

	return pagetoken.Fingerprint("names:" + strings.Join(names, "\n"))
}

// parseNamespaceList reads the limit and the cursor of a list request: the
// page size and the sealed cursor of the page before.
func parseNamespaceList(r *http.Request, fingerprint string) (namespaceListQuery, error) {
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

	cursor, first, err := parseNamespaceCursor(req.Cursor, fingerprint)
	if err != nil {
		return none, err
	}

	return namespaceListQuery{limit: limit, cursor: cursor, after: "", first: first}, nil
}

// parseNamespaceCursor reads a cursor of listNamespaces: empty means the
// first page, otherwise the sealed cursor of the last namespace of the page
// before. It answers whether the request asks for the first page.
func parseNamespaceCursor(raw, fingerprint string) (string, bool, error) {
	if raw == "" {
		return "", true, nil
	}

	var wire namespaceCursorWire
	if err := pagetoken.Decode(raw, &wire); err != nil || wire.After == "" {
		return "", false, errNamespaceCursor
	}

	// Defense in depth: the AAD of the seal refuses a cursor of another query
	// too, and this check stays so that a change to the seal cannot skip it.
	if !pagetoken.SameFingerprint(wire.Fingerprint, fingerprint) {
		return "", false, errNamespaceCursor
	}

	return wire.After, false, nil
}

// readNamespacePage reads one page of the namespaces of filter: at most the
// page size rows after the cursor, with the total on the first page only.
func (h *Handler) readNamespacePage(
	ctx context.Context, query namespaceListQuery, filter namespace.Filter, fingerprint string,
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

		var cursor string
		if cursor, err = h.sealNamespaceCursor(ctx, fingerprint, page[len(page)-1].Name); err != nil {
			return listNamespacesResponse{}, err
		}

		token, err := pagetoken.Encode(namespaceCursorWire{After: cursor, Fingerprint: fingerprint})
		if err != nil {
			return listNamespacesResponse{}, fmt.Errorf("encode namespace cursor: %w", err)
		}

		nextCursor = token
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
			ID:                  page[i].ID.String(),
			Name:                page[i].Name.String(),
			PrimaryInstallation: page[i].PrimaryInstallation.String(),
			FailoverVersion:     page[i].FailoverVersion,
			CreatedAt:           page[i].CreatedAt.UTC().Format(time.RFC3339Nano),
		})
	}

	return out, nil
}

// namespaceCursorAAD binds a sealed cursor to the filter of the list, so a
// cursor works only in the token of the query that made it.
func namespaceCursorAAD(fingerprint string) []byte {
	return []byte("namespace-list-cursor\n" + fingerprint)
}

// sealNamespaceCursor returns the cursor of a page token for the namespace
// after. A handler without a sealer fails closed: it answers an error and
// never an unsealed cursor.
func (h *Handler) sealNamespaceCursor(
	ctx context.Context, fingerprint string, after namespace.Name,
) (string, error) {
	if h.cursors == nil {
		return "", errNoCursorSealer
	}

	sealed, err := h.cursors.Wrap(ctx, []byte(after), namespaceCursorAAD(fingerprint))
	if err != nil {
		return "", fmt.Errorf("seal namespace cursor: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// openNamespaceCursor returns the namespace name that a cursor holds. A
// handler without a sealer, a cursor that does not open, and a cursor that
// does not hold a namespace name give errNamespaceCursor.
func (h *Handler) openNamespaceCursor(
	ctx context.Context, fingerprint, cursor string,
) (namespace.Name, error) {
	if h.cursors == nil || cursor == "" || len(cursor) > pagetoken.MaxLen {
		return "", errNamespaceCursor
	}

	sealed, err := base64.RawURLEncoding.Strict().DecodeString(cursor)
	if err != nil {
		return "", errNamespaceCursor
	}

	plain, err := h.cursors.Unwrap(ctx, sealed, namespaceCursorAAD(fingerprint))
	if err != nil {
		return "", errNamespaceCursor
	}

	name, err := namespace.ParseName(string(plain))
	if err != nil {
		return "", errNamespaceCursor
	}

	return name, nil
}

// visibleNamespaces answers the filter of the namespaces where p holds
// namespace:read, as the authorizer of the routes lists them, so the IP
// allowlist of each namespace applies. A caller whose scope holds every
// namespace, such as an administrator, is not filtered by the allowlists
// yet. Empty is true when no namespace can qualify, so the caller answers
// the empty page without calling the store. With no access service only an
// administrator sees every namespace. The filter runs in the SQL query,
// never after paging.
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

	scope, err := h.authz.ListObjects(ctx, p, identity.ActionNamespaceRead)
	if err != nil {
		return none, false, err
	}

	if scope.CoversEveryObject() {
		return namespace.Filter{All: true, Names: nil}, false, nil
	}

	names := make([]namespace.Name, 0, len(scope.Namespaces))
	for i := range scope.Namespaces {
		names = append(names, scope.Namespaces[i].Namespace())
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
