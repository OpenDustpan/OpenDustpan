package server

// Principal and group routes of the installation API (ADR 52, decisions 1,
// 2 and 4). Principals and groups belong to the installation, so every route
// acts on the object @server:
//
//   - server:read-principals reads principals, groups and members,
//   - server:manage-principals creates, changes and deletes principals,
//   - server:manage-groups creates, changes and deletes groups and their
//     memberships.
//
// Each change writes its audit entry in the transaction of the change (fail
// closed): when the entry cannot be written, the change fails with 500 and
// the store does not change. A refused change writes a failure entry after
// the fact, best effort: a lost failure entry is logged and counted.
//
// The list routes answer one page: the query parameters limit (1 to 1000;
// 100 when the request names none, and 0 answers 400), cursor (the
// nextCursor of the page before, at most 512 bytes) and prefix (the start of
// the subject, or of the group name without regard to case). The list of
// principals also takes kind (person, service or workload).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/internal/errcode"
	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/tempadmin"
)

// maxPrincipalBodyBytes bounds a principal or group body. The fields hold at
// most a few kilobytes of text, so this is ample.
const maxPrincipalBodyBytes = 16 * 1024

// Body errors of the principal and group routes. classify maps each to 400.
var (
	errPrincipalBody = errors.New(
		`invalid principal body: provide {"subject":"...","displayName":"...","kind":"person|service|workload"}`,
	)
	errPrincipalChangeBody = errors.New(`invalid principal change body: provide {"displayName":"..."}`)
	errGroupBody           = errors.New(`invalid group body: provide {"name":"...","description":"..."}`)
	errGroupChangeBody     = errors.New(
		`invalid group change body: provide {"name":"..."}, {"description":"..."} or {"closedToIdentityProvider":true}`,
	)
	errMemberBody = errors.New(`invalid member body: provide {"principalID":"prn-..."}`)
	errPageQuery  = errors.New("invalid page: limit is a number from 1 to 1000")
)

// errNoPrincipalStore reports a server whose store keeps no principals.
// Both SQL adapters keep them, so only a custom store without the port
// answers it.
var errNoPrincipalStore = errors.New("the store of this server keeps no principals")

// principalResponse is one principal on the wire. Times are UTC in RFC 3339
// with nanoseconds, the wire form of the other installation routes. Source
// is local, ldap or scim (ADR 55). GroupCount is the count of its current
// memberships: its groups come from their own paged route. A null
// disabledAt means that the inactivity disable did not end the principal
// (ADR 53); POST /api/server/principals/{id}/disable sets it and POST
// /api/server/principals/{id}/enable clears it.
// TemporaryAdministrator is true for a temporary administrator that still
// works, with its fixed expiry in TemporaryExpiresAt, so the console shows
// a banner for it. Both stay empty for every other principal.
//
// Contract: never remove or rename these fields. The console and the
// clients read only this API, and they update on their own schedule.
type principalResponse struct {
	ID          string  `json:"id"`
	Subject     string  `json:"subject"`
	DisplayName string  `json:"displayName"`
	Kind        string  `json:"kind"`
	Source      string  `json:"source"`
	CreatedAt   string  `json:"createdAt"`
	UpdatedAt   string  `json:"updatedAt"`
	DisabledAt  *string `json:"disabledAt"`
	// Namespace is empty for a principal of the installation, and names the
	// namespace of a service principal of a namespace.
	Namespace  string `json:"namespace"`
	GroupCount int64  `json:"groupCount"`
	// TemporaryAdministrator and TemporaryExpiresAt name a working
	// temporary administrator and its fixed expiry. A null expiry means
	// the principal is no temporary administrator.
	TemporaryAdministrator bool    `json:"temporaryAdministrator"`
	TemporaryExpiresAt     *string `json:"temporaryExpiresAt"`
}

// groupResponse is one group on the wire. A group whose source is not local
// refuses membership changes through the API with 409. MemberCount is the
// count of its current members. Protected reports whether the group holds a
// sensitive action on the installation or on a principal of the
// installation through its own grants, without the grants of its members. A
// read that cannot check the group answers an error, never false.
//
// Contract: never remove or rename these fields.
type groupResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
	MemberCount int64  `json:"memberCount"`
	// ClosedToIdentityProvider is true when the identity provider may not
	// add members to the group.
	ClosedToIdentityProvider bool `json:"closedToIdentityProvider"`
	Protected                bool `json:"protected"`
}

// memberResponse is one member of a group on the wire.
//
// Contract: never remove or rename these fields.
type memberResponse struct {
	AddedAt   string            `json:"addedAt"`
	Principal principalResponse `json:"principal"`
}

// principalListResponse, groupListResponse and memberListResponse wrap one
// page of a list. Total is the count of every row that matches the prefix
// and the kind, on every page. NextCursor is empty on the last page.
type principalListResponse struct {
	Principals []principalResponse `json:"principals"`
	NextCursor string              `json:"nextCursor"`
	Total      int64               `json:"total"`
}

type groupListResponse struct {
	Groups     []groupResponse `json:"groups"`
	NextCursor string          `json:"nextCursor"`
	Total      int64           `json:"total"`
}

type memberListResponse struct {
	Members    []memberResponse `json:"members"`
	NextCursor string           `json:"nextCursor"`
	Total      int64            `json:"total"`
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
}

// createGroupRequest is the body of a group create.
type createGroupRequest struct {
	Name        *string `json:"name"`
	Description *string `json:"description"`
}

// changeGroupRequest is the body of a group change. A missing field keeps
// its value. ClosedToIdentityProvider sets the switch of the group: true
// closes the group to the identity provider.
type changeGroupRequest struct {
	Name                     *string `json:"name"`
	Description              *string `json:"description"`
	ClosedToIdentityProvider *bool   `json:"closedToIdentityProvider"`
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
		ID:                     p.ID,
		Subject:                p.Subject,
		DisplayName:            p.DisplayName,
		Kind:                   string(p.Kind),
		Source:                 string(p.Source),
		CreatedAt:              wireTime(p.CreatedAt),
		UpdatedAt:              wireTime(p.UpdatedAt),
		DisabledAt:             optionalWireTime(p.DisabledAt),
		Namespace:              p.Namespace.String(),
		GroupCount:             p.GroupCount,
		TemporaryAdministrator: false,
		TemporaryExpiresAt:     nil,
	}
}

// markTempAdmin sets the banner flag on each principal of the installation
// in body that still works as a temporary administrator. Without a reader
// the body passes through unchanged. It reads every record once, not one
// per principal. Only persons are marked: only a person holds a record. A
// failed read returns the error, so the handler answers 500.
func (h *Handler) markTempAdmin(ctx context.Context, body any) (any, error) {
	if h.tempAdmins == nil {
		return body, nil
	}

	records, err := h.tempAdmins.ListTempAdmins(ctx)
	if err != nil {
		return nil, err
	}

	active := activeTempAdmins(records, h.now())

	mark := func(res principalResponse) principalResponse {
		return markOneTempAdmin(res, active)
	}

	switch v := body.(type) {
	case principalResponse:
		return mark(v), nil
	case principalListResponse:
		return markTempAdminPage(v, mark), nil
	case memberResponse:
		v.Principal = mark(v.Principal)

		return v, nil
	case memberListResponse:
		return markTempAdminMembers(v, mark), nil
	default:
		return body, nil
	}
}

// activeTempAdmins answers the records that work at now, by principal ID,
// so a new person with the subject of an old record is not marked.
func activeTempAdmins(records []tempadmin.TempAdmin, now time.Time) map[string]tempadmin.TempAdmin {
	active := make(map[string]tempadmin.TempAdmin, len(records))

	for i := range records {
		if records[i].ActiveAt(now) {
			active[records[i].PrincipalID] = records[i]
		}
	}

	return active
}

// markOneTempAdmin sets the banner flag on one principal that still works
// as a temporary administrator. Only persons are marked.
func markOneTempAdmin(res principalResponse, active map[string]tempadmin.TempAdmin) principalResponse {
	if res.Kind != string(principal.KindPerson) {
		return res
	}

	rec, ok := active[res.ID]
	if !ok {
		return res
	}

	at := wireTime(rec.ExpiresAt)
	res.TemporaryAdministrator = true
	res.TemporaryExpiresAt = &at

	return res
}

// markTempAdminPage sets the banner flag on each principal of one page.
func markTempAdminPage(
	v principalListResponse, mark func(principalResponse) principalResponse,
) principalListResponse {
	for i := range v.Principals {
		v.Principals[i] = mark(v.Principals[i])
	}

	return v
}

// markTempAdminMembers sets the banner flag on each member principal of one
// page.
func markTempAdminMembers(
	v memberListResponse, mark func(principalResponse) principalResponse,
) memberListResponse {
	for i := range v.Members {
		v.Members[i].Principal = mark(v.Members[i].Principal)
	}

	return v
}

// groupOf returns one group on the wire with its protected flag.
func groupOf(g principal.Group, protected bool) groupResponse {
	return groupResponse{
		ID:                       g.ID,
		Name:                     g.Name,
		Description:              g.Description,
		Source:                   string(g.Source),
		CreatedAt:                wireTime(g.CreatedAt),
		UpdatedAt:                wireTime(g.UpdatedAt),
		MemberCount:              g.MemberCount,
		ClosedToIdentityProvider: g.ClosedToIdentityProvider,
		Protected:                protected,
	}
}

// protectedGroupOf returns one group on the wire with its protected flag. A
// failed check answers its error, so the route answers an error, never a
// false flag.
func (h *Handler) protectedGroupOf(ctx context.Context, g principal.Group) (groupResponse, error) {
	protected, err := access.Protected(ctx, h.reach, g.ID)
	if err != nil {
		return groupResponse{}, err
	}

	return groupOf(g, protected), nil
}

// storedGroupOf returns one stored group on the wire with its protected
// flag. The write is stored, so the answer must match the stored state: a
// failed check answers protected, the fail-closed value of access.Protected.
func (h *Handler) storedGroupOf(ctx context.Context, g principal.Group) groupResponse {
	body, err := h.protectedGroupOf(ctx, g)
	if err != nil {
		// The cause is the error type only: the text of a store error can hold
		// driver text, and the log must not carry it.
		h.logger.WarnContext(ctx, "the protected check of a stored group failed",
			slog.String("group", g.ID), slog.String("cause", causeType(err)))

		return groupOf(g, true)
	}

	return body
}

// protectedGroupsOf returns one page of groups on the wire with the
// protected flag of each. It checks the whole page with one ProtectedGroups
// call. A failed check of one group answers its error, so the route answers
// an error, never a false flag.
func (h *Handler) protectedGroupsOf(
	ctx context.Context, page principal.Page[principal.Group],
) (groupListResponse, error) {
	ids := make([]string, 0, len(page.Items))
	for i := range page.Items {
		ids = append(ids, page.Items[i].ID)
	}

	flags, err := access.ProtectedGroups(ctx, h.reach, ids)
	if err != nil {
		return groupListResponse{}, err
	}

	return groupsWithFlags(page, flags)
}

// groupsWithFlags returns page on the wire with the flag of each group from
// flags. A group with no flag answers an error, never a false flag.
func groupsWithFlags(page principal.Page[principal.Group], flags map[string]bool) (groupListResponse, error) {
	out := groupListResponse{
		Groups: make([]groupResponse, 0, len(page.Items)), NextCursor: page.NextCursor, Total: page.Total,
	}

	for i := range page.Items {
		// Defense in depth: ProtectedGroups answers a flag for each ID or an
		// error. This check stays so that a missing flag never shows as false.
		g := &page.Items[i]

		protected, ok := flags[g.ID]
		if !ok {
			return groupListResponse{}, fmt.Errorf("protected flag of group %q: %w", g.ID, access.ErrInvalid)
		}

		out.Groups = append(out.Groups, groupOf(*g, protected))
	}

	return out, nil
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
// or an error with the result so far: a change names its record in both. e
// is the audit entry of the caller, which a change hands to the Service.
type principalOp func(
	w http.ResponseWriter, r *http.Request, svc *principal.Service, e audit.Entry, p identity.Principal,
) (principalResult, error)

// servePrincipalRead adapts a read to a route handler: it finds the
// principal service, runs op and writes its answer or its error.
func (h *Handler) servePrincipalRead(op principalOp) RouteHandler {
	return func(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
		var none audit.Entry

		if res, err := h.runPrincipalOp(w, r, op, &call{p: p, obj: identity.Object{}, e: none}); err == nil {
			h.writePrincipalResult(w, r, res)
		}
	}
}

// servePrincipalChange adapts a change to a route handler. The Service
// writes the success entry in the transaction of the change. A refused
// change writes one failure entry here, best effort, with the missing
// actions of a reach refusal.
func (h *Handler) servePrincipalChange(action identity.Action, op principalOp) RouteHandler {
	return func(w http.ResponseWriter, r *http.Request, p identity.Principal, _ identity.Object) {
		res, err := h.runPrincipalOp(w, r, op, &call{p: p, obj: identity.Object{}, e: callerEntry(r, p, action)})

		if err != nil && res.object != "" && h.principals != nil {
			h.appendServerChangeReason(r, p, serverChange{
				at: h.principals.Now(), action: action, object: res.object, outcome: audit.OutcomeFailure,
			}, reachReason(err))
		}

		if err == nil {
			h.writePrincipalResult(w, r, res)
		}
	}
}

// runPrincipalOp runs op and writes its error. It answers the result in both
// cases, and the error of the operation.
func (h *Handler) runPrincipalOp(
	w http.ResponseWriter, r *http.Request, op principalOp, c *call,
) (principalResult, error) {
	ctx := r.Context()

	if h.principals == nil {
		h.writeError(ctx, w, errNoPrincipalStore)

		return principalResult{body: nil, object: "", status: 0}, errNoPrincipalStore
	}

	res, err := op(w, r, h.principals, c.e, c.p)
	if err != nil {
		h.writeError(ctx, w, err)

		return res, err
	}

	return res, nil
}

// writePrincipalResult writes the status and the body of a success. A body
// that holds principals carries the banner flag of each working temporary
// administrator. A failed flag read answers 500 with the usual error body.
func (h *Handler) writePrincipalResult(w http.ResponseWriter, r *http.Request, res principalResult) {
	if res.body == nil {
		w.WriteHeader(res.status)

		return
	}

	marked, err := h.markTempAdmin(r.Context(), res.body)
	if err != nil {
		h.writeError(r.Context(), w, err)

		return
	}

	h.writeJSON(r.Context(), w, res.status, marked)
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
		authorizedRoute(http.MethodPost, principalsPath, manage, resolveServer,
			h.servePrincipalChange(identity.ActionServerManagePrincipals, createPrincipal)),
		authorizedRoute(http.MethodGet, principalsPath, read, resolveServer,
			h.servePrincipalRead(listPrincipals)),
		authorizedRoute(http.MethodGet, principalPath, read, resolveServer,
			h.servePrincipalRead(getPrincipal)),
		authorizedRoute(http.MethodPatch, principalPath, manage, resolveServer,
			h.servePrincipalChange(identity.ActionServerManagePrincipals, changePrincipal)),
		authorizedRoute(http.MethodDelete, principalPath, manage, resolveServer,
			h.servePrincipalChange(identity.ActionServerManagePrincipals, deletePrincipal)),
		authorizedRoute(http.MethodGet, principalPath+"/groups", read, resolveServer,
			h.servePrincipalRead(h.listGroupsOfPrincipal)),
		authorizedRoute(http.MethodPost, principalPath+"/enable", manage, resolveServer,
			h.servePrincipalChange(identity.ActionServerManagePrincipals, enablePrincipal)),
		authorizedRoute(http.MethodPost, principalPath+"/disable", manage, resolveServer,
			h.servePrincipalChange(identity.ActionServerManagePrincipals, disablePrincipal)),
	}
}

// groupRoutes returns the group and membership rows of the route table.
func (h *Handler) groupRoutes() []Route {
	read := []identity.Action{identity.ActionServerReadPrincipals}
	manage := []identity.Action{identity.ActionServerManageGroups}

	return []Route{
		authorizedRoute(http.MethodPost, groupsPath, manage, resolveServer,
			h.servePrincipalChange(identity.ActionServerManageGroups, h.createGroup)),
		authorizedRoute(http.MethodGet, groupsPath, read, resolveServer,
			h.servePrincipalRead(h.listGroups)),
		authorizedRoute(http.MethodGet, groupPath, read, resolveServer,
			h.servePrincipalRead(h.getGroup)),
		authorizedRoute(http.MethodPatch, groupPath, manage, resolveServer,
			h.servePrincipalChange(identity.ActionServerManageGroups, h.changeGroup)),
		authorizedRoute(http.MethodDelete, groupPath, manage, resolveServer,
			h.fresh(identity.ActionServerManageGroups,
				h.servePrincipalChange(identity.ActionServerManageGroups, deleteGroup))),
		authorizedRoute(http.MethodGet, membersPath, read, resolveServer,
			h.servePrincipalRead(listMembers)),
		authorizedRoute(http.MethodPost, membersPath, manage, resolveServer,
			h.fresh(identity.ActionServerManageGroups,
				h.servePrincipalChange(identity.ActionServerManageGroups, addMember))),
		authorizedRoute(http.MethodDelete, membersPath+"/{principal}", manage, resolveServer,
			h.fresh(identity.ActionServerManageGroups,
				h.servePrincipalChange(identity.ActionServerManageGroups, removeMember))),
	}
}

// decodeStrict reads one JSON object from r into dst with no unknown field
// and nothing after it. Any failure answers bad.
func decodeStrict(w http.ResponseWriter, r *http.Request, dst any, bad error) error {
	body, err := readBody(w, r, bad)
	if err != nil {
		return err
	}

	return decodeBody(body, dst, bad)
}

// readBody reads the body of r under the limit of a request body. It
// answers errBodyTooLarge over the limit, and bad when the read fails.
func readBody(w http.ResponseWriter, r *http.Request, bad error) ([]byte, error) {
	return readBodyUpTo(w, r, maxPrincipalBodyBytes, bad)
}

// bodyTooLargeError reports a request body longer than limit bytes. The limit
// is the one of the route that read the body, so the answer names it.
type bodyTooLargeError struct {
	limit int64
}

// Error returns the sentence of the error, with the limit of the route.
func (e *bodyTooLargeError) Error() string {
	return fmt.Sprintf("request body too large: the limit is %d bytes", e.limit)
}

// errBodyTooLarge returns the error of a body longer than limit bytes.
func errBodyTooLarge(limit int64) error {
	return &bodyTooLargeError{limit: limit}
}

// classifyBodyTooLarge maps the error of a body over the limit of its route to
// 413 with the limit. Every route that reads a bounded body answers through it.
func classifyBodyTooLarge(err error) (failure, bool) {
	tooLarge, ok := errors.AsType[*bodyTooLargeError](err)
	if !ok {
		return fail(0, ""), false
	}

	return failCoded(http.StatusRequestEntityTooLarge, tooLarge.Error(), errcode.RequestBodyTooLarge,
		errcode.Quantity("maxBytes", tooLarge.limit)), true
}

// readBodyUpTo is readBody with the limit of the route.
func readBodyUpTo(w http.ResponseWriter, r *http.Request, limit int64, bad error) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
	if tooLarge, isLimit := errors.AsType[*http.MaxBytesError](err); isLimit && tooLarge.Limit > 0 {
		return nil, errBodyTooLarge(limit)
	}

	if err != nil {
		return nil, bad
	}

	return body, nil
}

// decodeBody decodes body into dst as decodeStrict does: one JSON value
// with no unknown field, else bad.
func decodeBody(body []byte, dst any, bad error) error {
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

// pageRequest reads the query parameters limit, cursor and prefix of a
// list route. A limit that is not a number answers 400.
func pageRequest(r *http.Request) (principal.PageRequest, error) {
	q := queryOf(r)
	req := principal.PageRequest{Prefix: q.Get("prefix"), Cursor: q.Get("cursor"), Limit: 0}

	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			return principal.PageRequest{}, errPageQuery
		}

		req.Limit = n
	}

	return req, nil
}

// createPrincipal serves POST /api/server/principals: it answers 201 with
// the new principal. A taken subject answers 409. A bad or reserved subject,
// an unknown kind or a bad body answers 400.
func createPrincipal(
	w http.ResponseWriter, r *http.Request, svc *principal.Service, e audit.Entry, _ identity.Principal,
) (principalResult, error) {
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

	in := principal.Input{Subject: *req.Subject, DisplayName: "", Kind: kind}
	if req.DisplayName != nil {
		in.DisplayName = *req.DisplayName
	}

	p, err := svc.CreatePrincipal(r.Context(), e, in)
	if err != nil {
		return failed("principals"), err
	}

	return ok(http.StatusCreated, principalOf(p), principal.Ref(p.ID)), nil
}

// listPrincipals serves GET /api/server/principals: one page, ordered by
// subject. The query parameter kind keeps the principals of one kind. An
// unknown kind answers 400.
func listPrincipals(
	_ http.ResponseWriter, r *http.Request, svc *principal.Service, _ audit.Entry, _ identity.Principal,
) (principalResult, error) {
	req, err := pageRequest(r)
	if err != nil {
		return failed(""), err
	}

	page, err := svc.ListPrincipals(r.Context(), req, principal.Kind(queryOf(r).Get("kind")))
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

// getPrincipal serves GET /api/server/principals/{id}. An unknown ID
// answers 404.
func getPrincipal(
	_ http.ResponseWriter, r *http.Request, svc *principal.Service, _ audit.Entry, _ identity.Principal,
) (principalResult, error) {
	p, err := svc.GetPrincipal(r.Context(), r.PathValue("id"))
	if err != nil {
		return failed(""), err
	}

	return ok(http.StatusOK, principalOf(p), ""), nil
}

// changePrincipal serves PATCH /api/server/principals/{id}: it changes the
// display name and answers 200 with the stored principal. A body that sets
// no field answers 400.
func changePrincipal(
	w http.ResponseWriter, r *http.Request, svc *principal.Service, e audit.Entry, _ identity.Principal,
) (principalResult, error) {
	object := principal.Ref(r.PathValue("id"))

	var req changePrincipalRequest

	err := decodeChange(w, r, &req, errPrincipalChangeBody, func() bool { return req.DisplayName == nil })
	if err != nil {
		return failed(object), err
	}

	p, err := svc.UpdatePrincipal(r.Context(), e, r.PathValue("id"), principal.Change{DisplayName: req.DisplayName})
	if err != nil {
		return failed(object), err
	}

	return ok(http.StatusOK, principalOf(p), object), nil
}

// deletePrincipal serves DELETE /api/server/principals/{id}: it ends the
// principal and its memberships, and answers 204. The rows stay for the
// access-review history. The last enabled break-glass account answers 409.
func deletePrincipal(
	_ http.ResponseWriter, r *http.Request, svc *principal.Service, e audit.Entry, caller identity.Principal,
) (principalResult, error) {
	object := principal.Ref(r.PathValue("id"))

	if err := svc.DeletePrincipal(r.Context(), e, caller, r.PathValue("id")); err != nil {
		return failed(object), err
	}

	return ok(http.StatusNoContent, nil, object), nil
}

// enablePrincipal serves POST /api/server/principals/{id}/enable: it
// enables a principal again after an inactivity disable and answers 200
// with the principal. The enable restores the use of the grants of the
// principal, so the caller must hold every action that they give. A
// principal that is not disabled answers 409.
func enablePrincipal(
	_ http.ResponseWriter, r *http.Request, svc *principal.Service, e audit.Entry, caller identity.Principal,
) (principalResult, error) {
	id := r.PathValue("id")
	object := principal.Ref(id)

	if err := svc.EnablePrincipal(r.Context(), e, caller, id); err != nil {
		return failed(object), err
	}

	p, err := svc.GetPrincipal(r.Context(), id)
	if err != nil {
		return failed(object), err
	}

	return ok(http.StatusOK, principalOf(p), object), nil
}

// disablePrincipal serves POST /api/server/principals/{id}/disable: it
// disables a principal and answers 200 with the principal. The write only
// narrows access, so it works in maintenance mode. The caller must hold every
// action that the grants of the principal give. A principal that is disabled
// answers 409, and so does the last enabled break-glass account.
func disablePrincipal(
	_ http.ResponseWriter, r *http.Request, svc *principal.Service, e audit.Entry, caller identity.Principal,
) (principalResult, error) {
	id := r.PathValue("id")
	object := principal.Ref(id)

	if err := svc.DisablePrincipal(r.Context(), e, caller, id); err != nil {
		return failed(object), err
	}

	p, err := svc.GetPrincipal(r.Context(), id)
	if err != nil {
		return failed(object), err
	}

	return ok(http.StatusOK, principalOf(p), object), nil
}

// listGroupsOfPrincipal serves GET /api/server/principals/{id}/groups: one
// page, ordered by name, with the protected flag of each group.
func (h *Handler) listGroupsOfPrincipal(
	_ http.ResponseWriter, r *http.Request, svc *principal.Service, _ audit.Entry, _ identity.Principal,
) (principalResult, error) {
	req, err := pageRequest(r)
	if err != nil {
		return failed(""), err
	}

	page, err := svc.ListGroupsOf(r.Context(), r.PathValue("id"), req)
	if err != nil {
		return failed(""), err
	}

	body, err := h.protectedGroupsOf(r.Context(), page)
	if err != nil {
		return failed(""), err
	}

	return ok(http.StatusOK, body, ""), nil
}

// createGroup serves POST /api/server/groups: it answers 201 with the new
// group. A name that another group holds without regard to case answers 409.
func (h *Handler) createGroup(
	w http.ResponseWriter, r *http.Request, svc *principal.Service, e audit.Entry, _ identity.Principal,
) (principalResult, error) {
	var req createGroupRequest
	if err := decodeStrict(w, r, &req, errGroupBody); err != nil {
		return failed("groups"), err
	}

	if req.Name == nil {
		return failed("groups"), errGroupBody
	}

	in := principal.GroupInput{Name: *req.Name, Description: ""}
	if req.Description != nil {
		in.Description = *req.Description
	}

	g, err := svc.CreateGroup(r.Context(), e, in)
	if err != nil {
		return failed("groups"), err
	}

	return ok(http.StatusCreated, h.storedGroupOf(r.Context(), g), principal.GroupRef(g.ID)), nil
}

// listGroups serves GET /api/server/groups: one page, ordered by name, with
// the protected flag of each group.
func (h *Handler) listGroups(
	_ http.ResponseWriter, r *http.Request, svc *principal.Service, _ audit.Entry, _ identity.Principal,
) (principalResult, error) {
	req, err := pageRequest(r)
	if err != nil {
		return failed(""), err
	}

	page, err := svc.ListGroups(r.Context(), req)
	if err != nil {
		return failed(""), err
	}

	body, err := h.protectedGroupsOf(r.Context(), page)
	if err != nil {
		return failed(""), err
	}

	return ok(http.StatusOK, body, ""), nil
}

// getGroup serves GET /api/server/groups/{id}. An unknown ID answers 404.
// The group carries its protected flag.
func (h *Handler) getGroup(
	_ http.ResponseWriter, r *http.Request, svc *principal.Service, _ audit.Entry, _ identity.Principal,
) (principalResult, error) {
	g, err := svc.GetGroup(r.Context(), r.PathValue("id"))
	if err != nil {
		return failed(""), err
	}

	body, err := h.protectedGroupOf(r.Context(), g)
	if err != nil {
		return failed(""), err
	}

	return ok(http.StatusOK, body, ""), nil
}

// changeGroup serves PATCH /api/server/groups/{id}: it renames the group,
// changes its description, closes or opens it to the identity provider, or
// combines those, and answers 200 with the stored group and its protected
// flag.
func (h *Handler) changeGroup(
	w http.ResponseWriter, r *http.Request, svc *principal.Service, e audit.Entry, caller identity.Principal,
) (principalResult, error) {
	var req changeGroupRequest

	object := principal.GroupRef(r.PathValue("id"))
	empty := func() bool {
		return req.Name == nil && req.Description == nil && req.ClosedToIdentityProvider == nil
	}

	err := decodeChange(w, r, &req, errGroupChangeBody, empty)
	if err != nil {
		return failed(object), err
	}

	g, err := svc.UpdateGroup(r.Context(), e, caller, r.PathValue("id"), principal.GroupUpdate{
		Name:                     req.Name,
		Description:              req.Description,
		ClosedToIdentityProvider: req.ClosedToIdentityProvider,
	})
	if err != nil {
		return failed(object), err
	}

	return ok(http.StatusOK, h.storedGroupOf(r.Context(), g), object), nil
}

// deleteGroup serves DELETE /api/server/groups/{id}: it ends the group and
// its memberships, and answers 204. The members keep their records.
func deleteGroup(
	_ http.ResponseWriter, r *http.Request, svc *principal.Service, e audit.Entry, caller identity.Principal,
) (principalResult, error) {
	object := principal.GroupRef(r.PathValue("id"))

	if err := svc.DeleteGroup(r.Context(), e, caller, r.PathValue("id")); err != nil {
		return failed(object), err
	}

	return ok(http.StatusNoContent, nil, object), nil
}

// listMembers serves GET /api/server/groups/{id}/members: one page, ordered
// by subject.
func listMembers(
	_ http.ResponseWriter, r *http.Request, svc *principal.Service, _ audit.Entry, _ identity.Principal,
) (principalResult, error) {
	req, err := pageRequest(r)
	if err != nil {
		return failed(""), err
	}

	page, err := svc.ListMembers(r.Context(), r.PathValue("id"), req)
	if err != nil {
		return failed(""), err
	}

	out := memberListResponse{
		Members: make([]memberResponse, 0, len(page.Items)), NextCursor: page.NextCursor, Total: page.Total,
	}
	for i := range page.Items {
		out.Members = append(out.Members, memberResponse{
			AddedAt:   wireTime(page.Items[i].AddedAt),
			Principal: principalOf(page.Items[i].Principal),
		})
	}

	return ok(http.StatusOK, out, ""), nil
}

// addMember serves POST /api/server/groups/{id}/members: it puts the
// principal of the body in the group and answers 201 with the member. A
// principal that is a member answers 409, and so does a rejoin in the same
// millisecond as a removal: the message says to try again. An unknown group
// or principal answers 404.
func addMember(
	w http.ResponseWriter, r *http.Request, svc *principal.Service, e audit.Entry, caller identity.Principal,
) (principalResult, error) {
	var req addMemberRequest
	if err := decodeStrict(w, r, &req, errMemberBody); err != nil {
		return failed(principal.GroupRef(r.PathValue("id"))), err
	}

	if req.PrincipalID == nil || *req.PrincipalID == "" {
		return failed(principal.GroupRef(r.PathValue("id"))), errMemberBody
	}

	ctx := r.Context()
	object := principal.MemberRef(r.PathValue("id"), *req.PrincipalID)

	p, err := svc.GetPrincipal(ctx, *req.PrincipalID)
	if err != nil {
		return failed(object), err
	}

	m, err := svc.AddMember(ctx, e, caller, r.PathValue("id"), p.ID)
	if err != nil {
		return failed(object), err
	}

	return ok(http.StatusCreated, memberResponse{AddedAt: wireTime(m.AddedAt), Principal: principalOf(p)}, object), nil
}

// removeMember serves DELETE /api/server/groups/{id}/members/{principal}:
// it ends the membership and answers 204. The principal keeps its record.
func removeMember(
	_ http.ResponseWriter, r *http.Request, svc *principal.Service, e audit.Entry, caller identity.Principal,
) (principalResult, error) {
	object := principal.MemberRef(r.PathValue("id"), r.PathValue("principal"))

	if err := svc.RemoveMember(r.Context(), e, caller, r.PathValue("id"), r.PathValue("principal")); err != nil {
		return failed(object), err
	}

	return ok(http.StatusNoContent, nil, object), nil
}

// classifyPrincipal maps the principal and group errors to their status and
// message: a bad body, subject, name or kind answers 400, a taken subject,
// name or membership 409, an unknown principal, group or membership 404, and
// a store with no principal port 501. A membership change of a group that a
// directory keeps answers 409, and the message names the directory. The domain wraps each sentinel with
// the ID or the name it acted on, never with driver text.
func classifyPrincipal(err error) (failure, bool) {
	if f, ok := classifyPrincipalRequest(err); ok {
		return f, true
	}

	if errors.Is(err, principal.ErrLastBreakGlass) {
		return failCoded(http.StatusConflict, err.Error(), errcode.PrincipalLastBreakGlass), true
	}

	return classifyPrincipalState(err)
}

// classifyPrincipalRequest maps the errors of a bad body, subject, page or
// size to 400 and 413.
func classifyPrincipalRequest(err error) (failure, bool) {
	switch {
	case errors.Is(err, errPrincipalBody), errors.Is(err, errPrincipalChangeBody),
		errors.Is(err, errGroupBody), errors.Is(err, errGroupChangeBody), errors.Is(err, errMemberBody),
		errors.Is(err, errNamespacePrincipalBody):
		return failCoded(http.StatusBadRequest, err.Error(), errcode.RequestBodyInvalid), true
	case errors.Is(err, principal.ErrInvalid):
		return failCoded(http.StatusBadRequest, err.Error(), errcode.PrincipalInvalid), true
	case errors.Is(err, principal.ErrReservedSubject):
		return failCoded(http.StatusBadRequest, err.Error(), errcode.PrincipalReservedSubject), true
	case errors.Is(err, errPageQuery):
		return failCoded(http.StatusBadRequest, err.Error(), errcode.RequestPageSizeInvalid), true
	}

	return fail(0, ""), false
}

// classifyPrincipalState maps the errors of a taken, disabled, kept or unknown
// principal, and of a store with no principal port.
func classifyPrincipalState(err error) (failure, bool) {
	switch {
	case errors.Is(err, principal.ErrAlreadyExists):
		return failCoded(http.StatusConflict, err.Error(), errcode.PrincipalAlreadyExists), true
	case errors.Is(err, principal.ErrManagedByDirectory):
		return failCoded(http.StatusConflict, err.Error(), errcode.PrincipalManagedByDirectory), true
	case errors.Is(err, principal.ErrNotDisabled):
		return failCoded(http.StatusConflict, err.Error(), errcode.PrincipalNotDisabled), true
	case errors.Is(err, principal.ErrDisabled):
		return failCoded(http.StatusConflict, err.Error(), errcode.PrincipalDisabled), true
	case errors.Is(err, access.ErrLastSiteAdministrator):
		return failCoded(http.StatusConflict, err.Error(), errcode.AccessLastSiteAdministrator), true
	case errors.Is(err, principal.ErrNotFound):
		return failCoded(http.StatusNotFound, err.Error(), errcode.PrincipalNotFound), true
	case errors.Is(err, errNoPrincipalStore):
		return failCoded(http.StatusNotImplemented, err.Error(), errcode.PrincipalUnsupported), true
	}

	return fail(0, ""), false
}
