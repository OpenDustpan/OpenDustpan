package tfe

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
// unit:manage.
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
// a 422 error.
func createRef(r *http.Request, body []byte) (workspace.Ref, error) {
	var req workspaceCreateRequest

	if err := json.Unmarshal(body, &req); err != nil || req.Data.Attributes.Name == "" {
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

	ws, err := h.workspaces.Create(ctx, ref)
	if err != nil {
		if errors.Is(err, workspace.ErrAlreadyExists) {
			h.writeUnprocessable(ctx, w, duplicateWorkspaceDetail, duplicateWorkspaceTitle)

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

	all, err := h.workspaces.List(ctx, ns)
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	visible, err := h.filterReadable(ctx, p, filterWorkspaces(all, r.URL.Query().Get("search[name]")))
	if err != nil {
		h.writeError(ctx, w, err)

		return
	}

	h.writeWorkspaceList(ctx, w, p, visible, page)
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

// workspacePage holds one list page request: the page number and size.
type workspacePage struct {
	number int
	size   int
}

// parseWorkspacePage reads page[number] and page[size] from the query.
// Missing values default to the first page of defaultPageSize entries. A
// value that is not a positive integer fails with a 422 error. A size above
// maxPageSize reads as maxPageSize.
func parseWorkspacePage(r *http.Request) (workspacePage, error) {
	number, err := workspacePageValue(r.URL.Query().Get("page[number]"), 1)
	if err != nil {
		return workspacePage{}, err
	}

	size, err := workspacePageValue(r.URL.Query().Get("page[size]"), defaultPageSize)
	if err != nil {
		return workspacePage{}, err
	}

	if size > maxPageSize {
		size = maxPageSize
	}

	return workspacePage{number: number, size: size}, nil
}

// workspacePageValue parses one page query value, or returns fallback when
// the value is missing.
func workspacePageValue(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}

	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		return 0, fmt.Errorf("%w: page value %q", errInvalidBody, raw)
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

// writeWorkspaceList writes one page of the workspace list with the
// meta.pagination object go-tfe reads. The start and end stay within the
// list without overflow: page number and size come from the caller.
func (h *Handler) writeWorkspaceList(
	ctx context.Context, w http.ResponseWriter, p identity.Principal, all []workspace.Workspace, page workspacePage,
) {
	total := len(all)

	pages := 0
	if total > 0 {
		pages = (total-1)/page.size + 1
	}

	start := total
	if page.number-1 <= total/page.size {
		start = (page.number - 1) * page.size
	}

	end := total
	if page.size <= total-start {
		end = start + page.size
	}

	items := make([]workspaceResource, 0, end-start)

	for i := range all[start:end] {
		doc, err := h.workspaceDocument(ctx, p, all[start+i])
		if err != nil {
			h.writeError(ctx, w, err)

			return
		}

		items = append(items, doc)
	}

	h.writeJSON(ctx, w, http.StatusOK, workspaceListDocument{
		Data: items,
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
		CanForceUnlock: allow(identity.ActionUnitManage),
		CanQueueRun:    write,
	}
}
