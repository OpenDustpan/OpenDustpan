package server

// Principal and group routes of the installation API (ADR 52, decisions 1,
// 2 and 4). Principals and groups belong to the installation, so every route
// acts on the object @server:
//
//   - server:read-principals reads principals, groups and members,
//   - server:manage-principals creates, changes and deletes principals,
//   - server:manage-groups creates, changes and deletes groups and their
//     memberships.

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// maxPrincipalBodyBytes bounds a principal or group body. The fields hold at
// most a few kilobytes of text, so this is ample.
const maxPrincipalBodyBytes = 16 * 1024

// Body errors of the principal and group routes. classify maps each to 400.
var (
	errPrincipalBody = errors.New(
		`invalid principal body: provide {"subject":"...","displayName":"...","kind":"person|service|workload"}`,
	)
	errPrincipalChangeBody = errors.New(
		`invalid principal change body: provide {"displayName":"..."} or {"disabled":true|false}`,
	)
	errGroupBody       = errors.New(`invalid group body: provide {"name":"...","description":"..."}`)
	errGroupChangeBody = errors.New(`invalid group change body: provide {"name":"..."} or {"description":"..."}`)
	errMemberBody      = errors.New(`invalid member body: provide {"principalID":"prn-..."}`)
)

// errNoPrincipalStore reports a server whose store keeps no principals.
// Both SQL adapters keep them, so only a custom store without the port
// answers it.
var errNoPrincipalStore = errors.New("the store of this server keeps no principals")

// principalResponse is one principal on the wire. Times are UTC in RFC 3339
// with nanoseconds, the wire form of the other installation routes.
//
// Contract: never remove or rename these fields. The console and the
// clients read only this API, and they update on their own schedule.
type principalResponse struct {
	ID          string `json:"id"`
	Subject     string `json:"subject"`
	DisplayName string `json:"displayName"`
	Kind        string `json:"kind"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	Disabled    bool   `json:"disabled"`
}

// groupResponse is one group on the wire.
//
// Contract: never remove or rename these fields.
type groupResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

// memberResponse is one member of a group on the wire.
//
// Contract: never remove or rename these fields.
type memberResponse struct {
	AddedAt   string            `json:"addedAt"`
	Principal principalResponse `json:"principal"`
}

// principalListResponse, groupListResponse and memberListResponse wrap the
// lists, so a later field (a cursor) can join without a break.
type principalListResponse struct {
	Principals []principalResponse `json:"principals"`
}

type groupListResponse struct {
	Groups []groupResponse `json:"groups"`
}

type memberListResponse struct {
	Members []memberResponse `json:"members"`
}

// createPrincipalRequest is the body of a principal create.
type createPrincipalRequest struct {
	Subject     *string `json:"subject"`
	DisplayName *string `json:"displayName"`
	Kind        *string `json:"kind"`
}

// changePrincipalRequest is the body of a principal change. A missing field
// keeps its value.
type changePrincipalRequest struct {
	DisplayName *string `json:"displayName"`
	Disabled    *bool   `json:"disabled"`
}

// createGroupRequest is the body of a group create.
type createGroupRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// changeGroupRequest is the body of a group change. A missing field keeps
// its value.
type changeGroupRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// addMemberRequest is the body of a member add.
type addMemberRequest struct {
	PrincipalID *string `json:"principalID"`
}

// wireTime formats t for the wire.
func wireTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func principalOf(p principal.Principal) principalResponse {
	return principalResponse{
		ID:          p.ID,
		Subject:     p.Subject,
		DisplayName: p.DisplayName,
		Kind:        string(p.Kind),
		CreatedAt:   wireTime(p.CreatedAt),
		UpdatedAt:   wireTime(p.UpdatedAt),
		Disabled:    p.Disabled,
	}
}

func groupOf(g principal.Group) groupResponse {
	return groupResponse{
		ID:          g.ID,
		Name:        g.Name,
		Description: g.Description,
		CreatedAt:   wireTime(g.CreatedAt),
		UpdatedAt:   wireTime(g.UpdatedAt),
	}
}

func groupsOf(gs []principal.Group) groupListResponse {
	out := groupListResponse{Groups: make([]groupResponse, 0, len(gs))}
	for _, g := range gs {
		out.Groups = append(out.Groups, groupOf(g))
	}

	return out
}

// Route paths of the principals and the groups.
const (
	principalsPath = "/api/server/principals"
	principalPath  = "/api/server/principals/{id}"
	groupsPath     = "/api/server/groups"
	groupPath      = "/api/server/groups/{id}"
	membersPath    = "/api/server/groups/{id}/members"
)

// principalResult is the answer of one principal or group operation: the
// status and the body of a success, and the record that a change acted on,
// for its audit entry. A nil body answers the status alone.
type principalResult struct {
	body   any
	object string
	status int
}

// principalOp is one principal or group operation. It answers its result,
// or an error with the result so far: a change names its record in both.
type principalOp func(w http.ResponseWriter, r *http.Request, svc *principal.Service) (principalResult, error)

// Audit object names of the records that a change acts on. An entry keeps
// the name as text (ADR 60, decision 6), never a request path.
func principalRef(id string) string { return "principal:" + id }

func groupRef(id string) string { return "group:" + id }

func memberRef(groupID, principalID string) string {
	return "group:" + groupID + "#member:" + principalID
}

// servePrincipalRead adapts a read to a route handler: it finds the
// principal service, runs op and writes its answer or its error.
func (h *Handler) servePrincipalRead(op principalOp) RouteHandler {
	return func(w http.ResponseWriter, r *http.Request, _ identity.Principal, _ identity.Object) {
		if res, ok := h.runPrincipalOp(w, r, op); ok {
			h.writePrincipalResult(w, r, res)
		}
	}
}

// servePrincipalChange adapts a change to a route handler. It writes one
// audit entry of the kind change in the scope @server for each change that
// the service runs, with the outcome success or failure.
func (h *Handler) servePrincipalChange(action identity.Action, op principalOp) RouteHandler {
	return func(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
		res, ok := h.runPrincipalOp(w, r, op)

		outcome := audit.OutcomeSuccess
		if !ok {
			outcome = audit.OutcomeFailure
		}

		if res.object != "" {
			h.appendServerChange(r, p, serverChange{action: action, object: res.object, outcome: outcome})
		}

		if ok {
			h.writePrincipalResult(w, r, res)
		}
	}
}

// runPrincipalOp runs op and writes its error. It reports whether op
// succeeded, with the result in both cases.
func (h *Handler) runPrincipalOp(w http.ResponseWriter, r *http.Request, op principalOp) (principalResult, bool) {
	ctx := r.Context()

	if h.principals == nil {
		h.writeError(ctx, w, errNoPrincipalStore)

		return principalResult{body: nil, object: "", status: 0}, false
	}

	res, err := op(w, r, h.principals)
	if err != nil {
		h.writeError(ctx, w, err)

		return res, false
	}

	return res, true
}

// writePrincipalResult writes the status and the body of a success.
func (h *Handler) writePrincipalResult(w http.ResponseWriter, r *http.Request, res principalResult) {
	if res.body == nil {
		w.WriteHeader(res.status)

		return
	}

	h.writeJSON(r.Context(), w, res.status, res.body)
}

// ok returns the result of a success.
func ok(status int, body any, object string) principalResult {
	return principalResult{body: body, object: object, status: status}
}

// failed returns the result of a failure of a change of object.
func failed(object string) principalResult {
	return principalResult{body: nil, object: object, status: 0}
}

// principalRoutes returns the principal rows of the route table.
func (h *Handler) principalRoutes() []Route {
	read := []identity.Action{identity.ActionServerReadPrincipals}
	manage := []identity.Action{identity.ActionServerManagePrincipals}

	return []Route{
		{
			Method:  http.MethodPost,
			Path:    principalsPath,
			Scope:   ScopeAuthorized,
			Actions: manage,
			Resolve: resolveServer,
			Serve:   h.servePrincipalChange(identity.ActionServerManagePrincipals, createPrincipal),
		},
		{
			Method: http.MethodGet, Path: principalsPath, Scope: ScopeAuthorized,
			Actions: read, Resolve: resolveServer, Serve: h.servePrincipalRead(listPrincipals),
		},
		{
			Method: http.MethodGet, Path: principalPath, Scope: ScopeAuthorized,
			Actions: read, Resolve: resolveServer, Serve: h.servePrincipalRead(getPrincipal),
		},
		{
			Method:  http.MethodPatch,
			Path:    principalPath,
			Scope:   ScopeAuthorized,
			Actions: manage,
			Resolve: resolveServer,
			Serve:   h.servePrincipalChange(identity.ActionServerManagePrincipals, changePrincipal),
		},
		{
			Method:  http.MethodDelete,
			Path:    principalPath,
			Scope:   ScopeAuthorized,
			Actions: manage,
			Resolve: resolveServer,
			Serve:   h.servePrincipalChange(identity.ActionServerManagePrincipals, deletePrincipal),
		},
		{
			Method: http.MethodGet, Path: principalPath + "/groups", Scope: ScopeAuthorized,
			Actions: read, Resolve: resolveServer, Serve: h.servePrincipalRead(listGroupsOfPrincipal),
		},
	}
}

// groupRoutes returns the group and membership rows of the route table.
func (h *Handler) groupRoutes() []Route {
	read := []identity.Action{identity.ActionServerReadPrincipals}
	manage := []identity.Action{identity.ActionServerManageGroups}

	return []Route{
		{
			Method:  http.MethodPost,
			Path:    groupsPath,
			Scope:   ScopeAuthorized,
			Actions: manage,
			Resolve: resolveServer,
			Serve:   h.servePrincipalChange(identity.ActionServerManageGroups, createGroup),
		},
		{
			Method: http.MethodGet, Path: groupsPath, Scope: ScopeAuthorized,
			Actions: read, Resolve: resolveServer, Serve: h.servePrincipalRead(listGroups),
		},
		{
			Method: http.MethodGet, Path: groupPath, Scope: ScopeAuthorized,
			Actions: read, Resolve: resolveServer, Serve: h.servePrincipalRead(getGroup),
		},
		{
			Method:  http.MethodPatch,
			Path:    groupPath,
			Scope:   ScopeAuthorized,
			Actions: manage,
			Resolve: resolveServer,
			Serve:   h.servePrincipalChange(identity.ActionServerManageGroups, changeGroup),
		},
		{
			Method:  http.MethodDelete,
			Path:    groupPath,
			Scope:   ScopeAuthorized,
			Actions: manage,
			Resolve: resolveServer,
			Serve:   h.servePrincipalChange(identity.ActionServerManageGroups, deleteGroup),
		},
		{
			Method: http.MethodGet, Path: membersPath, Scope: ScopeAuthorized,
			Actions: read, Resolve: resolveServer, Serve: h.servePrincipalRead(listMembers),
		},
		{
			Method:  http.MethodPost,
			Path:    membersPath,
			Scope:   ScopeAuthorized,
			Actions: manage,
			Resolve: resolveServer,
			Serve:   h.servePrincipalChange(identity.ActionServerManageGroups, addMember),
		},
		{
			Method:  http.MethodDelete,
			Path:    membersPath + "/{principalId}",
			Scope:   ScopeAuthorized,
			Actions: manage,
			Resolve: resolveServer,
			Serve:   h.servePrincipalChange(identity.ActionServerManageGroups, removeMember),
		},
	}
}

// decodeStrict reads one JSON object from r into dst with no unknown field
// and nothing after it. Any failure answers bad.
func decodeStrict(w http.ResponseWriter, r *http.Request, dst any, bad error) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxPrincipalBodyBytes))
	if err != nil {
		return bad
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	if decErr := dec.Decode(dst); decErr != nil {
		return bad
	}

	if tailErr := dec.Decode(&struct{}{}); !errors.Is(tailErr, io.EOF) {
		return bad
	}

	return nil
}

// decodeChange reads a change body into dst like decodeStrict, and answers
// bad when empty reports that the body sets no field.
func decodeChange(w http.ResponseWriter, r *http.Request, dst any, bad error, empty func() bool) error {
	if err := decodeStrict(w, r, dst, bad); err != nil {
		return err
	}

	if empty() {
		return bad
	}

	return nil
}

// createPrincipal serves POST /api/server/principals: it answers 201 with
// the new principal. A taken subject answers 409. A bad or reserved subject,
// an unknown kind or a bad body answers 400.
func createPrincipal(w http.ResponseWriter, r *http.Request, svc *principal.Service) (principalResult, error) {
	var req createPrincipalRequest
	if err := decodeStrict(w, r, &req, errPrincipalBody); err != nil {
		return failed("principals"), err
	}

	if req.Subject == nil || req.Kind == nil {
		return failed("principals"), errPrincipalBody
	}

	kind, err := principal.ParseKind(*req.Kind)
	if err != nil {
		return failed("principals"), err
	}

	displayName := ""
	if req.DisplayName != nil {
		displayName = *req.DisplayName
	}

	p, err := svc.CreatePrincipal(r.Context(), *req.Subject, displayName, kind)
	if err != nil {
		return failed("principals"), err
	}

	return ok(http.StatusCreated, principalOf(p), principalRef(p.ID)), nil
}

// listPrincipals serves GET /api/server/principals, ordered by subject.
func listPrincipals(_ http.ResponseWriter, r *http.Request, svc *principal.Service) (principalResult, error) {
	ps, err := svc.ListPrincipals(r.Context())
	if err != nil {
		return failed(""), err
	}

	out := principalListResponse{Principals: make([]principalResponse, 0, len(ps))}
	for i := range ps {
		out.Principals = append(out.Principals, principalOf(ps[i]))
	}

	return ok(http.StatusOK, out, ""), nil
}

// getPrincipal serves GET /api/server/principals/{id}. An unknown ID
// answers 404.
func getPrincipal(_ http.ResponseWriter, r *http.Request, svc *principal.Service) (principalResult, error) {
	p, err := svc.GetPrincipal(r.Context(), r.PathValue("id"))
	if err != nil {
		return failed(""), err
	}

	return ok(http.StatusOK, principalOf(p), ""), nil
}

// changePrincipal serves PATCH /api/server/principals/{id}: it changes the
// display name, the disabled flag or both, and answers 200 with the stored
// principal. A body that sets neither field answers 400.
func changePrincipal(w http.ResponseWriter, r *http.Request, svc *principal.Service) (principalResult, error) {
	object := principalRef(r.PathValue("id"))

	var req changePrincipalRequest

	err := decodeChange(w, r, &req, errPrincipalChangeBody, func() bool {
		return req.DisplayName == nil && req.Disabled == nil
	})
	if err != nil {
		return failed(object), err
	}

	change := principal.Change{DisplayName: req.DisplayName, Disabled: req.Disabled}

	p, err := svc.UpdatePrincipal(r.Context(), r.PathValue("id"), change)
	if err != nil {
		return failed(object), err
	}

	return ok(http.StatusOK, principalOf(p), object), nil
}

// deletePrincipal serves DELETE /api/server/principals/{id}: it deletes the
// principal and its memberships, and answers 204.
func deletePrincipal(_ http.ResponseWriter, r *http.Request, svc *principal.Service) (principalResult, error) {
	if err := svc.DeletePrincipal(r.Context(), r.PathValue("id")); err != nil {
		return failed(principalRef(r.PathValue("id"))), err
	}

	return ok(http.StatusNoContent, nil, principalRef(r.PathValue("id"))), nil
}

// listGroupsOfPrincipal serves GET /api/server/principals/{id}/groups,
// ordered by name.
func listGroupsOfPrincipal(_ http.ResponseWriter, r *http.Request, svc *principal.Service) (principalResult, error) {
	gs, err := svc.ListGroupsOf(r.Context(), r.PathValue("id"))
	if err != nil {
		return failed(""), err
	}

	return ok(http.StatusOK, groupsOf(gs), ""), nil
}

// createGroup serves POST /api/server/groups: it answers 201 with the new
// group. A name that another group holds without regard to case answers 409.
func createGroup(w http.ResponseWriter, r *http.Request, svc *principal.Service) (principalResult, error) {
	var req createGroupRequest
	if err := decodeStrict(w, r, &req, errGroupBody); err != nil {
		return failed("groups"), err
	}

	if req.Name == nil {
		return failed("groups"), errGroupBody
	}

	description := ""
	if req.Description != nil {
		description = *req.Description
	}

	g, err := svc.CreateGroup(r.Context(), *req.Name, description)
	if err != nil {
		return failed("groups"), err
	}

	return ok(http.StatusCreated, groupOf(g), groupRef(g.ID)), nil
}

// listGroups serves GET /api/server/groups, ordered by name.
func listGroups(_ http.ResponseWriter, r *http.Request, svc *principal.Service) (principalResult, error) {
	gs, err := svc.ListGroups(r.Context())
	if err != nil {
		return failed(""), err
	}

	return ok(http.StatusOK, groupsOf(gs), ""), nil
}

// getGroup serves GET /api/server/groups/{id}. An unknown ID answers 404.
func getGroup(_ http.ResponseWriter, r *http.Request, svc *principal.Service) (principalResult, error) {
	g, err := svc.GetGroup(r.Context(), r.PathValue("id"))
	if err != nil {
		return failed(""), err
	}

	return ok(http.StatusOK, groupOf(g), ""), nil
}

// changeGroup serves PATCH /api/server/groups/{id}: it renames the group,
// changes its description or both, and answers 200 with the stored group.
func changeGroup(w http.ResponseWriter, r *http.Request, svc *principal.Service) (principalResult, error) {
	var req changeGroupRequest

	object := groupRef(r.PathValue("id"))
	empty := func() bool { return req.Name == nil && req.Description == nil }

	err := decodeChange(w, r, &req, errGroupChangeBody, empty)
	if err != nil {
		return failed(object), err
	}

	g, err := svc.UpdateGroup(r.Context(), r.PathValue("id"), principal.GroupChange{
		Name:        req.Name,
		Description: req.Description,
	})
	if err != nil {
		return failed(object), err
	}

	return ok(http.StatusOK, groupOf(g), object), nil
}

// deleteGroup serves DELETE /api/server/groups/{id}: it deletes the group
// and its memberships, and answers 204. The members keep their records.
func deleteGroup(_ http.ResponseWriter, r *http.Request, svc *principal.Service) (principalResult, error) {
	if err := svc.DeleteGroup(r.Context(), r.PathValue("id")); err != nil {
		return failed(groupRef(r.PathValue("id"))), err
	}

	return ok(http.StatusNoContent, nil, groupRef(r.PathValue("id"))), nil
}

// listMembers serves GET /api/server/groups/{id}/members, ordered by
// subject.
func listMembers(_ http.ResponseWriter, r *http.Request, svc *principal.Service) (principalResult, error) {
	ms, err := svc.ListMembers(r.Context(), r.PathValue("id"))
	if err != nil {
		return failed(""), err
	}

	out := memberListResponse{Members: make([]memberResponse, 0, len(ms))}
	for i := range ms {
		out.Members = append(out.Members, memberResponse{
			AddedAt:   wireTime(ms[i].AddedAt),
			Principal: principalOf(ms[i].Principal),
		})
	}

	return ok(http.StatusOK, out, ""), nil
}

// addMember serves POST /api/server/groups/{id}/members: it puts the
// principal of the body in the group and answers 201 with the member. A
// principal that is a member answers 409. An unknown group or principal
// answers 404.
func addMember(w http.ResponseWriter, r *http.Request, svc *principal.Service) (principalResult, error) {
	var req addMemberRequest
	if err := decodeStrict(w, r, &req, errMemberBody); err != nil {
		return failed(groupRef(r.PathValue("id"))), err
	}

	if req.PrincipalID == nil || *req.PrincipalID == "" {
		return failed(groupRef(r.PathValue("id"))), errMemberBody
	}

	ctx := r.Context()
	object := memberRef(r.PathValue("id"), *req.PrincipalID)

	p, err := svc.GetPrincipal(ctx, *req.PrincipalID)
	if err != nil {
		return failed(object), err
	}

	m, err := svc.AddMember(ctx, r.PathValue("id"), p.ID)
	if err != nil {
		return failed(object), err
	}

	return ok(http.StatusCreated, memberResponse{AddedAt: wireTime(m.AddedAt), Principal: principalOf(p)},
		memberRef(m.GroupID, m.PrincipalID)), nil
}

// removeMember serves DELETE /api/server/groups/{id}/members/{principalId}:
// it takes the principal out of the group and answers 204. The principal
// keeps its record.
func removeMember(_ http.ResponseWriter, r *http.Request, svc *principal.Service) (principalResult, error) {
	if err := svc.RemoveMember(r.Context(), r.PathValue("id"), r.PathValue("principalId")); err != nil {
		return failed(memberRef(r.PathValue("id"), r.PathValue("principalId"))), err
	}

	return ok(http.StatusNoContent, nil, memberRef(r.PathValue("id"), r.PathValue("principalId"))), nil
}

// classifyPrincipal maps the principal and group errors to their status and
// message: a bad body, subject, name or kind answers 400, a taken subject,
// name or membership 409, an unknown principal, group or membership 404, and
// a store with no principal port 501. The domain wraps each sentinel with
// the ID or the name it acted on, never with driver text.
func classifyPrincipal(err error) (int, string, bool) {
	switch {
	case errors.Is(err, errPrincipalBody), errors.Is(err, errPrincipalChangeBody),
		errors.Is(err, errGroupBody), errors.Is(err, errGroupChangeBody), errors.Is(err, errMemberBody):
		return http.StatusBadRequest, err.Error(), true
	case errors.Is(err, principal.ErrInvalid), errors.Is(err, principal.ErrReservedSubject):
		return http.StatusBadRequest, err.Error(), true
	case errors.Is(err, principal.ErrAlreadyExists):
		return http.StatusConflict, err.Error(), true
	case errors.Is(err, principal.ErrNotFound):
		return http.StatusNotFound, err.Error(), true
	case errors.Is(err, errNoPrincipalStore):
		return http.StatusNotImplemented, err.Error(), true
	default:
		return 0, "", false
	}
}
