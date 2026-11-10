package tfe

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/internal/api/tofuunit"
	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/gate"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/workspace"
)

// Workspace routes serve what `tofu init` needs: read by name, create, read
// by ID and list. The server keeps state and locks for local execution only,
// so every workspace reports execution-mode local.
const (
	// workspaceType is the JSON:API type of every workspace document.
	workspaceType = "workspaces"
	// stateVersionType is the JSON:API type of the current-state-version
	// relationship.
	stateVersionType = "state-versions"
	// localExecutionMode is the execution mode every workspace reports: the
	// server never runs plans or applies.
	localExecutionMode = "local"
	// defaultPageSize is the list size when the caller sends no page[size].
	defaultPageSize = 20
	// maxPageSize caps page[size]: larger values read as 100, the TFE cap.
	maxPageSize = 100
	// duplicateWorkspaceDetail is the 422 detail a duplicate create answers
	// with. go-tfe reads the detail text, so it must hold these words.
	duplicateWorkspaceDetail = "Name has already been taken"
	// duplicateWorkspaceTitle is the 422 title a duplicate create answers with.
	duplicateWorkspaceTitle = "invalid attribute"
)

// workspacePermissions are the flags go-tfe reads from a workspace document.
// Each flag follows the Authorizer result for the caller on the workspace
// unit: lock, unlock and queue-run need unit:write, force-unlock needs
// unit:write-forced.
type workspacePermissions struct {
	CanLock        bool `json:"can-lock"`         //nolint:tagliatelle // The TFE wire name; it must not change.
	CanUnlock      bool `json:"can-unlock"`       //nolint:tagliatelle // The TFE wire name; it must not change.
	CanForceUnlock bool `json:"can-force-unlock"` //nolint:tagliatelle // The TFE wire name; it must not change.
	CanQueueRun    bool `json:"can-queue-run"`    //nolint:tagliatelle // The TFE wire name; it must not change.
}

// workspaceAttributes are the attributes of a workspace document.
type workspaceAttributes struct {
	Name          string               `json:"name"`
	ExecutionMode string               `json:"execution-mode"` //nolint:tagliatelle // The TFE wire name; it must not change.
	Locked        bool                 `json:"locked"`
	LockID        string               `json:"lock-id"`     //nolint:tagliatelle // The TFE wire name; it must not change.
	LockHolder    string               `json:"lock-holder"` //nolint:tagliatelle // The TFE wire name; it must not change.
	LockedAt      string               `json:"locked-at"`   //nolint:tagliatelle // The TFE wire name; it must not change.
	LockReason    string               `json:"lock-reason"` //nolint:tagliatelle // The TFE wire name; it must not change.
	AutoApply     bool                 `json:"auto-apply"`  //nolint:tagliatelle // The TFE wire name; it must not change.
	Permissions   workspacePermissions `json:"permissions"`
	CreatedAt     time.Time            `json:"created-at"` //nolint:tagliatelle // The TFE wire name; it must not change.
	UpdatedAt     time.Time            `json:"updated-at"` //nolint:tagliatelle // The TFE wire name; it must not change.
}

// relationshipRef is one JSON:API relationship target.
type relationshipRef struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// relationship is one JSON:API relationship: data holds the target, or null
// when the workspace has no current state.
type relationship struct {
	Data *relationshipRef `json:"data"`
}

// workspaceResource is one workspace as a JSON:API resource.
type workspaceResource struct {
	ID            string                  `json:"id"`
	Type          string                  `json:"type"`
	Attributes    workspaceAttributes     `json:"attributes"`
	Relationships map[string]relationship `json:"relationships"`
	Links         map[string]string       `json:"links,omitempty"`
}

// workspaceDocument is the response of a single workspace route.
type workspaceDocument struct {
	Data workspaceResource `json:"data"`
}

// workspaceListMeta holds the pagination object go-tfe reads.
type workspaceListMeta struct {
	Pagination workspacePagination `json:"pagination"`
}

// workspacePagination is the meta.pagination object of a workspace list.
type workspacePagination struct {
	CurrentPage int  `json:"current-page"` //nolint:tagliatelle // The TFE wire name; it must not change.
	PageSize    int  `json:"page-size"`    //nolint:tagliatelle // The TFE wire name; it must not change.
	PrevPage    *int `json:"prev-page"`    //nolint:tagliatelle // The TFE wire name; it must not change.
	NextPage    *int `json:"next-page"`    //nolint:tagliatelle // The TFE wire name; it must not change.
	TotalPages  int  `json:"total-pages"`  //nolint:tagliatelle // The TFE wire name; it must not change.
	TotalCount  int  `json:"total-count"`  //nolint:tagliatelle // The TFE wire name; it must not change.
}

// workspaceListDocument is the response of the workspace list route.
type workspaceListDocument struct {
	Data []workspaceResource `json:"data"`
	Meta workspaceListMeta   `json:"meta"`
}

// workspaceCreateAttributes holds the name in a workspace create body.
type workspaceCreateAttributes struct {
	Name string `json:"name"`
}

// workspaceCreateData holds the data object of a workspace create body.
type workspaceCreateData struct {
	Type       string                    `json:"type"`
	Attributes workspaceCreateAttributes `json:"attributes"`
}

// workspaceCreateRequest is the body of a workspace create: the name in
// data.attributes. Every other attribute is ignored.
type workspaceCreateRequest struct {
	Data workspaceCreateData `json:"data"`
}

// createRef reads the workspace reference in a create request: the namespace
// in organization and the name in the body. A missing or bad name fails with
// a 422 error. A missing name gets the refusal of workspace.ParseName, as a
// bad name does.
func createRef(r *http.Request, body []byte) (workspace.Ref, error) {
	var req workspaceCreateRequest

	if err := json.Unmarshal(body, &req); err != nil {
		return workspace.Ref{}, errInvalidBody
	}

	ns, err := namespace.ParseName(r.PathValue("organization"))
	if err != nil {
		return workspace.Ref{}, err
	}

	name, err := workspace.ParseName(req.Data.Attributes.Name)
	if err != nil {
		return workspace.Ref{}, err
	}

	return workspace.Ref{Namespace: ns, Name: name}, nil
}

// resolveCreateWorkspace returns the unit the create acts on: the namespace
// in organization and the workspace name in the body. It reads the body with
// http.MaxBytesReader and restores r.Body before returning, so the handler
// can read it again. A missing or bad name fails with a 422 error.
func resolveCreateWorkspace(w http.ResponseWriter, r *http.Request) (identity.Object, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		return identity.Object{}, errInvalidBody
	}

	r.Body = io.NopCloser(bytes.NewReader(body))

	ref, err := createRef(r, body)
	if err != nil {
		return identity.Object{}, err
	}

	return unitObject(ref)
}

// deleteWorkspace serves DELETE
// /api/v2/organizations/{organization}/workspaces/{workspace}, as the
// OpenTofu remote backend sends it for "tofu workspace delete". It is a soft
// delete (ADR 47): the workspace leaves every route and list at once and its
// name is free, but its state versions and data key stay until the final
// delete. It answers 204, 404 for an unknown organization or workspace, and
// 409 while the workspace is locked. Each delete writes an audit entry of the
// kind change.
func (h *Handler) deleteWorkspace(
	w http.ResponseWriter, r *http.Request, p identity.Principal, obj identity.Object,
) {
	ctx := r.Context()

	ns, err := namespace.ParseName(r.PathValue("organization"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	name, err := workspace.ParseName(r.PathValue("workspace"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if !h.guardStateWrite(
		w,
		r,
		p,
		obj,
		newStateCheck(identity.ActionUnitManage, gate.OperationDelete),
	) {
		return
	}

	entry := changeEntry(r, p, obj, identity.ActionUnitManage)
	if err := h.workspaces.Delete(ctx, workspace.Ref{Namespace: ns, Name: name}, entry); err != nil {
		h.writeError(ctx, w, err)

		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// readWorkspace serves GET /api/v2/organizations/{organization}/workspaces/{workspace}.
// An unknown organization or workspace answers 404 as a JSON:API error.
func (h *Handler) readWorkspace(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	ns, err := namespace.ParseName(r.PathValue("organization"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	name, err := workspace.ParseName(r.PathValue("workspace"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	ws, err := h.workspaces.Get(ctx, workspace.Ref{Namespace: ns, Name: name})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeWorkspace(ctx, w, http.StatusOK, p, ws)
}

// readWorkspaceByID serves GET /api/v2/workspaces/{id}. The resolver already
// resolved the ID to the unit and authorized it.
func (h *Handler) readWorkspaceByID(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	ws, err := h.workspaces.GetByID(ctx, workspace.ID(r.PathValue("id")))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeWorkspace(ctx, w, http.StatusOK, p, ws)
}

// createWorkspace serves POST /api/v2/organizations/{organization}/workspaces.
// A bad name answers 422. A duplicate answers 422 with the detail go-tfe
// reads. An unknown organization answers 404. The store publishes the
// inventory entry in the same transaction as the workspace.
func (h *Handler) createWorkspace(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		h.writeError(ctx, w, errInvalidBody)

		return
	}

	ref, err := createRef(r, body)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	// The job of a job token decides whether a manual lock of the namespace
	// applies to the create: the phase of a job that started before the lock
	// passes it, as on the OpenTofu HTTP backend. Any other token passes no
	// job.
	placement, err := h.placer.Place(ctx, tofuunit.PlaceRequest{
		Request: r, Principal: p, Ref: ref, Guard: h.guard, Namespaces: h.namespaces, Workspaces: h.workspaces,
	})
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	ws, err := h.workspaces.Create(workspace.WithJob(ctx, p.JobTokenID()), ref, placement)
	if err != nil {
		if errors.Is(err, workspace.ErrAlreadyExists) {
			h.writeClassError(ctx, w, errorClass{
				status: http.StatusUnprocessableEntity, title: duplicateWorkspaceTitle,
				detail: duplicateWorkspaceDetail, code: errcode.WorkspaceAlreadyExists, args: nil,
			})

			return
		}

		h.writeError(ctx, w, err)

		return
	}

	h.writeWorkspace(ctx, w, http.StatusCreated, p, ws)
}

// listWorkspaces serves GET /api/v2/organizations/{organization}/workspaces,
// ordered by name, with page[number] and page[size] capped at maxPageSize.
// search[name] keeps the workspaces whose name contains the value; an empty
// value keeps every workspace. The list holds only the workspaces the caller
// may unit:read, so the current-state-version relationship and the permission
// checks run only for the visible page. An unknown organization answers 404.
func (h *Handler) listWorkspaces(
	w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object,
) {
	ctx := r.Context()

	ns, err := namespace.ParseName(r.PathValue("organization"))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if _, nsErr := h.namespaces.Get(ctx, ns); nsErr != nil {
		h.writeError(ctx, w, nsErr)

		return
	}

	page, err := parseWorkspacePage(r)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	every, err := h.readsEveryUnit(ctx, p, ns)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	if every {
		served, pageErr := h.listWorkspacePage(ctx, w, p, ns, page)
		if pageErr != nil {
			h.writeError(ctx, w, pageErr)

			return
		}

		if served {
			return
		}
	}

	all, err := h.workspaces.List(ctx, ns)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	visible, err := h.filterReadable(ctx, p, filterWorkspaces(all, page.search))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeWorkspaceList(ctx, w, p, workspace.ListPage{Workspaces: pageOf(visible, page), Total: len(visible)}, page)
}

// readsEveryUnit reports whether the scope of unit:read of p holds every unit
// of ns: a grant on the installation or on the namespace, from an authorizer
// whose scope is exact. The list of such a caller pages and counts in the
// store. Any other caller, and any caller of an authorizer that does not say
// that its scope is exact, lists every workspace and checks each one: the
// count of the store holds the workspaces that the scope names, not the
// workspaces that Authorize allows.
func (h *Handler) readsEveryUnit(ctx context.Context, p identity.Principal, ns namespace.Name) (bool, error) {
	// Defense in depth: the page check below drops a denied workspace too.
	// This check stays so that the count of the page cannot show a workspace
	// that the page leaves out.
	if !identity.HasExactScope(h.authz) {
		return false, nil
	}

	// Defense in depth: the authorizer says that its scope is exact, and the
	// route authorizes namespace:read first. This check of unit:read on the
	// namespace stays so that a scope that covers every object, or an
	// authorizer that refuses the units of ns, cannot take the store path and
	// show the count of the store. A refusal sends the caller to the check of
	// each workspace.
	authErr := h.authz.Authorize(ctx, p, identity.ActionUnitRead, identity.NamespaceObject(ns))
	if authErr != nil {
		if errors.Is(authErr, identity.ErrDenied) {
			return false, nil
		}

		return false, fmt.Errorf("authorize unit:read on namespace %q: %w", ns, authErr)
	}

	scope, err := h.authz.ListObjects(ctx, p, identity.ActionUnitRead)
	if err != nil {
		return false, fmt.Errorf("list readable units: %w", err)
	}

	if scope.CoversEveryObject() {
		return true, nil
	}

	// Defense in depth: the check of unit:read on the namespace above allows
	// ns already. This check of the listed scope stays so that an authorizer
	// whose Authorize allows ns, but whose scope does not name ns, cannot take
	// the store path. The count of the store holds every workspace of ns, so
	// the scope must name all of ns.
	for i := range scope.Namespaces {
		if scope.Namespaces[i].Namespace() == ns {
			return true, nil
		}
	}

	return false, nil
}

// listWorkspacePage writes one page of the workspaces of ns whose name holds
// the search of the page, for a caller who may read every unit of ns. The
// store reads the page and the count. It reports false, and writes nothing,
// when the check of unit:read drops a workspace of the page: the count of the
// store then holds workspaces that the caller may not read, and the caller of
// this function lists with the check of each workspace.
func (h *Handler) listWorkspacePage(
	ctx context.Context, w http.ResponseWriter, p identity.Principal, ns namespace.Name, page workspacePage,
) (bool, error) {
	offset := math.MaxInt
	if page.number-1 <= (math.MaxInt-page.size)/page.size {
		offset = (page.number - 1) * page.size
	}

	got, err := h.workspaces.Query(ctx, workspace.ListQuery{
		Namespace: ns, NameContains: page.search, Offset: offset, Limit: page.size,
	})
	if err != nil {
		return false, err
	}

	// Defense in depth: the scope of the caller holds every unit of ns, and
	// the authorizer says that its scope is exact. This check stays so that
	// an authorizer whose scope and decisions differ cannot show a workspace
	// that the caller may not read, and cannot show its count.
	visible, err := h.filterReadable(ctx, p, got.Workspaces)
	if err != nil {
		return false, err
	}

	if len(visible) != len(got.Workspaces) {
		h.logger.WarnContext(ctx, "workspace page holds a denied workspace: listing with a check of each workspace",
			slog.String("namespace", ns.String()))

		return false, nil
	}

	h.writeWorkspaceList(ctx, w, p, workspace.ListPage{Workspaces: visible, Total: got.Total}, page)

	return true, nil
}

// filterReadable keeps the workspaces the caller may unit:read. A denied
// workspace is left out. Another authorizer failure fails the list.
func (h *Handler) filterReadable(
	ctx context.Context, p identity.Principal, all []workspace.Workspace,
) ([]workspace.Workspace, error) {
	kept := make([]workspace.Workspace, 0, len(all))

	for i := range all {
		obj, err := unitObject(all[i].Ref)
		if err != nil {
			return nil, err
		}

		if err := h.authz.Authorize(ctx, p, identity.ActionUnitRead, obj); err != nil {
			if errors.Is(err, identity.ErrDenied) {
				continue
			}

			return nil, err
		}
		kept = append(kept, all[i])
	}

	return kept, nil
}

// filterWorkspaces keeps the workspaces whose name contains search. An empty
// search keeps every workspace.
func filterWorkspaces(all []workspace.Workspace, search string) []workspace.Workspace {
	if search == "" {
		return all
	}

	kept := make([]workspace.Workspace, 0, len(all))

	for i := range all {
		if strings.Contains(all[i].Ref.Name.String(), search) {
			kept = append(kept, all[i])
		}
	}

	return kept
}

// workspacePage holds one list page request: the page number and size, and
// the search[name] value.
type workspacePage struct {
	search string
	number int
	size   int
}

// parseWorkspacePage reads page[number] and page[size] from the query.
// Missing values default to the first page of defaultPageSize entries. A
// value that is not a positive integer fails with a 422 error. A size above
// maxPageSize reads as maxPageSize.
func parseWorkspacePage(r *http.Request) (workspacePage, error) {
	number, err := workspacePageValue(r.URL.Query().Get("page[number]"), "page[number]", 1)
	if err != nil {
		return workspacePage{}, err
	}

	size, err := workspacePageValue(r.URL.Query().Get("page[size]"), "page[size]", defaultPageSize)
	if err != nil {
		return workspacePage{}, err
	}

	if size > maxPageSize {
		size = maxPageSize
	}

	return workspacePage{search: r.URL.Query().Get("search[name]"), number: number, size: size}, nil
}

// workspacePageValue parses raw, the value of the page query parameter name,
// or returns fallback when the value is missing. The error names the
// parameter and its rule, with the largest number that the server reads, and
// does not repeat the value.
func workspacePageValue(raw, name string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%w: %s must be a whole number from 1 to %d", errInvalidQuery, name, math.MaxInt)
	}

	return n, nil
}

// writeWorkspace writes one workspace document with its permission flags for
// the caller and its current-state-version relationship.
func (h *Handler) writeWorkspace(
	ctx context.Context, w http.ResponseWriter, status int, p identity.Principal, ws workspace.Workspace,
) {
	doc, err := h.workspaceDocument(ctx, p, ws)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeJSON(ctx, w, status, workspaceDocument{Data: doc})
}

// pageOf returns the workspaces of the page in all. The start and the end of
// the page stay within the list without overflow: the page number and size
// come from the caller.
func pageOf(all []workspace.Workspace, page workspacePage) []workspace.Workspace {
	total := len(all)

	start := total
	if page.number-1 <= total/page.size {
		start = (page.number - 1) * page.size
	}

	end := total
	if page.size <= total-start {
		end = start + page.size
	}

	return all[start:end]
}

// writeWorkspaceList writes the workspaces of one page of the workspace list
// with the meta.pagination object go-tfe reads. list.Total is the count of
// the workspaces on every page.
func (h *Handler) writeWorkspaceList(
	ctx context.Context, w http.ResponseWriter, p identity.Principal, list workspace.ListPage, page workspacePage,
) {
	items, total := list.Workspaces, list.Total

	pages := 0
	if total > 0 {
		pages = (total-1)/page.size + 1
	}

	docs := make([]workspaceResource, 0, len(items))

	for i := range items {
		doc, err := h.workspaceDocument(ctx, p, items[i])
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		docs = append(docs, doc)
	}

	h.writeJSON(ctx, w, http.StatusOK, workspaceListDocument{
		Data: docs,
		Meta: workspaceListMeta{Pagination: paginate(page, total, pages)},
	})
}

// paginate builds the meta.pagination object: the current page, the page
// size, the previous and next pages (null when there are none) and the totals.
func paginate(page workspacePage, total, pages int) workspacePagination {
	var prev, next *int

	if page.number > 1 {
		prev = new(page.number - 1)
	}

	if page.number < pages {
		next = new(page.number + 1)
	}

	return workspacePagination{
		CurrentPage: page.number,
		PageSize:    page.size,
		PrevPage:    prev,
		NextPage:    next,
		TotalPages:  pages,
		TotalCount:  total,
	}
}

// workspaceDocument renders ws as a JSON:API resource: type workspaces,
// execution-mode local, auto-apply false, the lock state with its holder,
// time and reason, the permission flags for the caller and the
// current-state-version relationship (null data when the workspace has no
// state yet).
func (h *Handler) workspaceDocument(
	ctx context.Context, p identity.Principal, ws workspace.Workspace,
) (workspaceResource, error) {
	name := ws.Ref.Name.String()

	relation := relationship{Data: nil}

	current, err := h.workspaces.Current(ctx, ws.ID)
	switch {
	case err == nil:
		relation.Data = &relationshipRef{ID: current.ID.String(), Type: stateVersionType}
	case errors.Is(err, workspace.ErrNoState):
	default:
		return workspaceResource{}, err
	}

	// The lock time travels as RFC 3339, empty when the workspace is
	// unlocked.
	lockedAt := ""
	if ws.Lock.Locked() {
		lockedAt = ws.Lock.LockedAt.UTC().Format(time.RFC3339)
	}

	return workspaceResource{
		ID:   ws.ID.String(),
		Type: workspaceType,
		Attributes: workspaceAttributes{
			Name:          name,
			ExecutionMode: localExecutionMode,
			Locked:        ws.Lock.Locked(),
			LockID:        ws.Lock.ID,
			LockHolder:    ws.Lock.Holder,
			LockedAt:      lockedAt,
			LockReason:    ws.Lock.Reason,
			AutoApply:     false,
			Permissions:   h.workspacePermissions(ctx, p, ws),
			CreatedAt:     ws.CreatedAt,
			UpdatedAt:     ws.UpdatedAt,
		},
		Relationships: map[string]relationship{"current-state-version": relation},
		Links: map[string]string{
			linkSelf: "/api/v2/organizations/" + ws.Ref.Namespace.String() + "/workspaces/" + name,
		},
	}, nil
}

// workspacePermissions answers the permission flags of ws for the caller.
// Each flag holds whether the Authorizer allows the matching action on the
// workspace unit.
func (h *Handler) workspacePermissions(
	ctx context.Context, p identity.Principal, ws workspace.Workspace,
) workspacePermissions {
	obj, err := unitObject(ws.Ref)
	if err != nil {
		return workspacePermissions{CanLock: false, CanUnlock: false, CanForceUnlock: false, CanQueueRun: false}
	}

	allow := func(action identity.Action) bool {
		return h.authz.Authorize(ctx, p, action, obj) == nil
	}

	write := allow(identity.ActionUnitWrite)

	return workspacePermissions{
		CanLock:        write,
		CanUnlock:      write,
		CanForceUnlock: allow(identity.ActionUnitWriteForced),
		CanQueueRun:    write,
	}
}
