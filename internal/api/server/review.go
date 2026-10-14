package server

// Access-review routes of the installation API (ADR 52, decisions 10, 11,
// 13 and 14).
//
// The export of an object answers who can access it: the grants on the
// object and inside it, and the grants with a data role on each object that
// contains it (with inheritedFrom). Each object kind has its own route and
// its own read-access action, checked on that object:
//
//   - /api/server/access-review: server:read-access, every grant,
//   - /api/server/namespaces/{namespace}/access-review:
//     namespace:read-access,
//   - /api/server/namespaces/{namespace}/collections/{collection}/access-review:
//     collection:read-access,
//   - /api/server/namespaces/{namespace}/unit-access-review?tool=&unit=:
//     unit:read-access.
//
// With no from and to, an export is a snapshot of the grants that were
// current at the time of its first page (asOfMs), and every page reads the
// same snapshot. With from and to (Unix milliseconds), it is a history of
// the grants whose life overlaps [from, to). subject keeps the grants of one
// subject. For a principal it also keeps the grants of its groups, for the
// time it was a member (via). expand=members puts each member of a group in
// its own row. The exports take limit and cursor, and answer nextCursor.
// The first page answers total. The export starts with a row for each
// bootstrap admin token, inside the limit. A cursor holds a binding of its
// export (object, mode, window, subject and expand), and a cursor of
// another export answers 400.
// The export of a collection answers 501 until collections are wired.
//
// Each export request writes one audit entry of the kind read-access (ADR
// 52, decision 13): in the namespace of a namespace or unit export, in
// @server for the installation. A failed append answers 500 with no rows.
//
// GET /api/server/caller/access answers the access of the caller: on one
// object (?object=), or the objects of one kind where it can do an action
// (?type=&action=), paged. It needs caller:read only, so every signed-in
// principal reads its own access.
//
// GET /api/server/caller/grants answers the current grants of the caller,
// paged: its own grants and the grants of its groups, with the group
// named. It needs caller:read only, and writes no audit entry. Each page
// reads the groups of the caller at its request, and the memberships and
// the grants as of the snapshot time of the first page (asOfMs).

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
)

// Query errors of the access-review and caller-access routes.
// classifyAccess maps each to 400.
var (
	errReviewQuery = errors.New(
		`invalid access review query: provide "from" and "to" as Unix milliseconds, both or neither`,
	)
	errExpandQuery       = errors.New(`invalid access review query: "expand" takes the value "members" only`)
	errCallerAccessQuery = errors.New(
		`invalid access query: provide "object=<object>", or "type=namespace|collection|unit" and an optional "action"`,
	)
)

// Route paths of the access review and the caller access.
const (
	serverReviewPath     = "/api/server/access-review"
	namespaceReviewPath  = "/api/server/namespaces/{namespace}/access-review"
	collectionReviewPath = "/api/server/namespaces/{namespace}/collections/{collection}/access-review"
	unitReviewPath       = "/api/server/namespaces/{namespace}/unit-access-review"
	callerAccessPath     = "/api/server/caller/access"
	callerGrantsPath     = "/api/server/caller/grants"
)

// reviewRowResponse is one row of an access-review export on the wire.
//
// Subject is the text form of the holder, and SubjectName its subject or
// group name. Holder is "bootstrap admin token" on the row of a bootstrap
// admin token, which has no grant: its grantID, subject and grantedAtMs are
// empty, and SubjectName names the subject of the token. InheritedFrom is
// the object of a grant that contains the object of the export, and empty
// for any other grant. ObjectState is "live", "deleted" (a unit that a
// restore can bring back) or "purged". CanGrantAccess is true when the role
// can change who has access to an object (a grant, a custom role, a group
// membership), so its holder can give itself access. RevokedAtMs is
// null for a current grant. Via is set on a row that a principal filter or
// expand=members reads through a group. EffectiveFromMs and
// EffectiveUntilMs are the time that the row gave access (null while it
// lasts). MemberCount is set for a group row of a snapshot only.
// LastAccessTracked is set on a row of a principal: it is false while the
// server records no sign-in and no token use, and LastAccessAtMs is then
// left out, so an export never shows a "Never" that it cannot prove.
//
// Contract: never remove or rename these fields.
type reviewRowResponse struct {
	Via               *reviewViaResponse `json:"via"`
	RevokedAtMs       *int64             `json:"revokedAtMs"`
	GrantedAtMs       *int64             `json:"grantedAtMs"`
	EffectiveFromMs   *int64             `json:"effectiveFromMs"`
	EffectiveUntilMs  *int64             `json:"effectiveUntilMs"`
	MemberCount       *int64             `json:"memberCount,omitempty"`
	LastAccessTracked *bool              `json:"lastAccessTracked,omitempty"`
	LastAccessAtMs    *int64             `json:"lastAccessAtMs,omitempty"`
	GrantID           string             `json:"grantID"`
	Subject           string             `json:"subject"`
	SubjectName       string             `json:"subjectName"`
	Holder            string             `json:"holder,omitempty"`
	Role              string             `json:"role"`
	Object            string             `json:"object"`
	ObjectState       string             `json:"objectState"`
	InheritedFrom     string             `json:"inheritedFrom"`
	GrantedBy         string             `json:"grantedBy"`
	RevokedBy         string             `json:"revokedBy"`
	CanGrantAccess    bool               `json:"canGrantAccess"`
}

// reviewViaResponse is the membership of a principal in the group of a
// row. MemberUntilMs is null while the membership lasts.
type reviewViaResponse struct {
	MemberUntilMs *int64 `json:"memberUntilMs"`
	Group         string `json:"group"`
	Member        string `json:"member"`
	MemberSubject string `json:"memberSubject"`
	MemberFromMs  int64  `json:"memberFromMs"`
}

// reviewResponse is one page of an export. Total is the count of every row
// of the export, on the first page only, and null on the pages after it.
// AsOfMs is the time of a snapshot, and null for a history. NextCursor is
// empty on the last page.
type reviewResponse struct {
	Total      *int64              `json:"total"`
	AsOfMs     *int64              `json:"asOfMs"`
	Grants     []reviewRowResponse `json:"grants"`
	NextCursor string              `json:"nextCursor"`
}

// accessResponse is the access of the caller on one object: its actions,
// sorted, and the grants that give them.
type accessResponse struct {
	Object  string          `json:"object"`
	Actions []string        `json:"actions"`
	Grants  []grantResponse `json:"grants"`
}

// accessObjectsResponse is one page of the objects where the caller can do
// an action. All is true, with no objects, when a grant on the
// installation covers every object. An object of a containing kind covers
// each object of the type inside it.
type accessObjectsResponse struct {
	Type       string                 `json:"type"`
	Action     string                 `json:"action"`
	NextCursor string                 `json:"nextCursor"`
	Objects    []accessObjectResponse `json:"objects"`
	Total      int64                  `json:"total"`
	All        bool                   `json:"all"`
}

type accessObjectResponse struct {
	Object string `json:"object"`
	Kind   string `json:"kind"`
}

// reviewRoutes returns the access-review and caller-access rows of the
// route table.
func (h *Handler) reviewRoutes() []Route {
	kinds := []struct {
		resolve ObjectResolver
		path    string
		action  identity.Action
	}{
		{resolveServer, serverReviewPath, identity.ActionServerReadAccess},
		{resolveNamespacePath, namespaceReviewPath, identity.ActionNamespaceReadAccess},
		{resolveCollectionPath, collectionReviewPath, identity.ActionCollectionReadAccess},
		{resolveUnitQuery, unitReviewPath, identity.ActionUnitReadAccess},
	}

	callers := []Route{{
		Method: http.MethodGet, Path: callerAccessPath, Scope: ScopeAuthorized,
		Actions: []identity.Action{identity.ActionCallerRead}, Resolve: resolveCaller,
		Serve: h.serveAccessRead(callerAccess),
	}, {
		Method: http.MethodGet, Path: callerGrantsPath, Scope: ScopeAuthorized,
		Actions: []identity.Action{identity.ActionCallerRead}, Resolve: resolveCaller,
		Serve: h.serveAccessRead(callerGrants),
	}}

	out := make([]Route, 0, len(kinds)+len(callers))

	for _, k := range kinds {
		out = append(out, Route{
			Method: http.MethodGet,
			Path:   k.path,
			Scope:  ScopeAuthorized,
			Actions: []identity.Action{
				k.action,
			},
			Resolve: k.resolve,
			Serve:   h.serveAccessRead(h.reviewAccess(k.action)),
		})
	}

	return append(out, callers...)
}

// reviewAccess returns the operation of GET on an access-review path with
// the read-access action action: one page of the export of the object, and
// its audit entry.
func (h *Handler) reviewAccess(action identity.Action) accessOp {
	return func(_ http.ResponseWriter, r *http.Request, svc *access.Service, c call) (principalResult, error) {
		req, err := reviewRequest(r)
		if err != nil {
			return failed(""), err
		}

		page, err := svc.Review(r.Context(), c.obj, req)
		if err != nil {
			return failed(""), err
		}

		if err := h.appendReviewRead(r, c, action, reviewAuditObject(c.obj, req, page.AsOf), svc.Now()); err != nil {
			return failed(""), err
		}

		return ok(http.StatusOK, reviewPageOf(page), ""), nil
	}
}

// reviewPageOf maps one page of an export to the wire.
func reviewPageOf(page access.ReviewPage) reviewResponse {
	out := reviewResponse{
		Total: page.Total, AsOfMs: nil, Grants: make([]reviewRowResponse, 0, len(page.Entries)),
		NextCursor: page.NextCursor,
	}

	if page.AsOf != nil {
		ms := page.AsOf.UnixMilli()
		out.AsOfMs = &ms
	}

	for i := range page.Entries {
		out.Grants = append(out.Grants, reviewRowOf(page.Entries[i]))
	}

	return out
}

// appendReviewRead writes the audit entry of one export request: kind
// read-access, in the namespace of the object or in @server. It fails
// closed: the caller answers the error and no rows. NewHandler refuses a
// config that serves the export with no audit store.
func (h *Handler) appendReviewRead(r *http.Request, c call, action identity.Action, object string, at time.Time) error {
	entry := callerEntry(r, c.p, action)
	entry.At, entry.Kind, entry.Outcome = at, audit.KindReadAccess, audit.OutcomeSuccess
	entry.Object = object

	ctx := r.Context()

	if c.obj.Namespace() == "" {
		entry.Namespace = audit.ServerScope
		if _, err := h.audit.AppendServer(ctx, entry); err != nil {
			return fmt.Errorf("append audit entry: %w", err)
		}

		return nil
	}

	entries, ok := h.audit.(namespaceAppender)
	if !ok {
		return fmt.Errorf("append audit entry: %w", errNoNamespaceAudit)
	}

	ns, err := h.namespaces.Get(ctx, c.obj.Namespace())
	if err != nil {
		return fmt.Errorf("read namespace of the export: %w", err)
	}

	entry.Namespace = ns.Name
	if _, err := entries.Append(ctx, ns.Fence(), entry); err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}

	return nil
}

// errNoNamespaceAudit reports an audit store that keeps only @server, so
// the entry of an export inside a namespace has nowhere to go.
var errNoNamespaceAudit = errors.New("the audit store keeps no namespace entries")

// reviewAuditObject returns the object of the audit entry of one page of
// an export: "access-review <object> <mode>", where the mode is "snapshot
// asOf=<ms>" with the time that the page read (also a time that a cursor
// chose), or "history <from>-<to>", then the subject filter and
// expand=members when the request has them. The Service has validated each
// value, so no request text reaches the log unchecked.
func reviewAuditObject(obj identity.Object, req access.ReviewRequest, asOf *time.Time) string {
	out := "access-review " + obj.String() + " snapshot"
	if asOf != nil {
		out += " asOf=" + strconv.FormatInt(asOf.UnixMilli(), 10)
	}

	if req.From != nil && req.To != nil {
		out = fmt.Sprintf("access-review %s history %d-%d", obj.String(), req.From.UnixMilli(), req.To.UnixMilli())
	}

	if req.Subject != "" {
		if subject, err := access.ParseSubject(req.Subject); err == nil {
			out += " subject=" + subject.String()
		}
	}

	if req.Expand {
		out += " expand=members"
	}

	return out
}

// reviewRequest reads the query of an export.
func reviewRequest(r *http.Request) (access.ReviewRequest, error) {
	page, err := pageRequest(r)
	if err != nil {
		return access.ReviewRequest{}, err
	}

	q := r.URL.Query()
	req := access.ReviewRequest{
		From: nil, To: nil, Subject: q.Get("subject"), Cursor: page.Cursor, Limit: page.Limit, Expand: false,
	}

	switch q.Get("expand") {
	case "":
	case "members":
		req.Expand = true
	default:
		return access.ReviewRequest{}, errExpandQuery
	}

	if req.From, err = queryMillis(q.Get("from")); err != nil {
		return access.ReviewRequest{}, err
	}

	if req.To, err = queryMillis(q.Get("to")); err != nil {
		return access.ReviewRequest{}, err
	}

	if (req.From == nil) != (req.To == nil) {
		return access.ReviewRequest{}, errReviewQuery
	}

	return req, nil
}

// queryMillis reads a time in Unix milliseconds, or nil for no value.
func queryMillis(raw string) (*time.Time, error) {
	if raw == "" {
		return nil, nil //nolint:nilnil // No value is a valid answer: the export is a snapshot.
	}

	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || ms < 0 {
		return nil, errReviewQuery
	}

	t := time.UnixMilli(ms).UTC()

	return &t, nil
}

// reviewRowOf maps one export row to the wire.
func reviewRowOf(e access.ReviewEntry) reviewRowResponse {
	row := e.Row
	out := reviewRowResponse{
		Via: nil, RevokedAtMs: millisOf(row.RevokedAt), GrantedAtMs: nil, EffectiveFromMs: nil,
		EffectiveUntilMs: millisOf(e.EffectiveUntil), MemberCount: row.MemberCount, LastAccessTracked: nil,
		LastAccessAtMs: nil, GrantID: row.Grant.ID, Subject: "", SubjectName: row.SubjectName, Holder: e.Holder,
		Role: row.Grant.Role, Object: row.Grant.Object.String(), ObjectState: e.ObjectState, InheritedFrom: "",
		GrantedBy: row.Grant.CreatedBy, RevokedBy: row.RevokedBy, CanGrantAccess: e.CanGrantAccess,
	}

	if e.Holder == "" {
		out.Subject = row.Grant.Subject.String()
		out.GrantedAtMs = millisOf(&row.Grant.CreatedAt)
		out.EffectiveFromMs = millisOf(&e.EffectiveFrom)
	}

	if e.InheritedFrom != nil {
		out.InheritedFrom = e.InheritedFrom.String()
	}

	// No sign-in and no token use is recorded yet (ADR 53): a principal row
	// says so, and a group row carries neither field.
	untracked := false
	if row.Grant.Subject.Kind == access.SubjectPrincipal || row.Via != nil {
		out.LastAccessTracked = &untracked
	}

	if v := row.Via; v != nil {
		out.Via = &reviewViaResponse{
			MemberUntilMs: millisOf(v.RemovedAt), Group: row.Grant.Subject.String(),
			Member:        access.Subject{Kind: access.SubjectPrincipal, ID: v.PrincipalID}.String(),
			MemberSubject: v.PrincipalSubject, MemberFromMs: v.AddedAt.UnixMilli(),
		}
	}

	return out
}

// millisOf returns t in Unix milliseconds, or nil for nil.
func millisOf(t *time.Time) *int64 {
	if t == nil {
		return nil
	}

	ms := t.UnixMilli()

	return &ms
}

// callerAccess serves GET /api/server/caller/access: the access of the
// caller on one object, or by object kind.
func callerAccess(
	_ http.ResponseWriter, r *http.Request, svc *access.Service, c call,
) (principalResult, error) {
	q := r.URL.Query()
	object, kind := q.Get("object"), q.Get("type")

	switch {
	case object != "" && kind == "":
		return callerAccessOn(r, svc, c.p, object)
	case object == "" && kind != "":
		return callerAccessByType(r, svc, c.p, access.ObjectKind(kind))
	default:
		return failed(""), errCallerAccessQuery
	}
}

// callerAccessOn answers the access of p on the object with the wire form
// object.
func callerAccessOn(
	r *http.Request, svc *access.Service, p identity.Principal, object string,
) (principalResult, error) {
	obj, err := identity.ParseObject(object)
	if err != nil {
		return failed(""), err
	}

	got, err := svc.Access(r.Context(), p, obj)
	if err != nil {
		return failed(""), err
	}

	out := accessResponse{
		Object:  got.Object.String(),
		Actions: make([]string, 0, len(got.Actions)),
		Grants:  make([]grantResponse, 0, len(got.Grants)),
	}

	for _, a := range got.Actions {
		out.Actions = append(out.Actions, string(a))
	}

	for i := range got.Grants {
		out.Grants = append(out.Grants, grantOf(got.Grants[i]))
	}

	return ok(http.StatusOK, out, ""), nil
}

// callerAccessByType answers one page of the objects of kind where p can
// do the action of the query.
func callerAccessByType(
	r *http.Request, svc *access.Service, p identity.Principal, kind access.ObjectKind,
) (principalResult, error) {
	page, err := pageRequest(r)
	if err != nil {
		return failed(""), err
	}

	got, err := svc.Objects(r.Context(), p, access.ObjectsRequest{
		Kind: kind, Action: identity.Action(r.URL.Query().Get("action")), Cursor: page.Cursor, Limit: page.Limit,
	})
	if err != nil {
		return failed(""), err
	}

	out := accessObjectsResponse{
		Type:       string(kind),
		Action:     string(got.Action),
		NextCursor: got.NextCursor,
		Objects:    make([]accessObjectResponse, 0, len(got.Objects)),
		Total:      got.Total,
		All:        got.All,
	}

	for _, obj := range got.Objects {
		k, _ := access.KindOf(obj)
		out.Objects = append(out.Objects, accessObjectResponse{Object: obj.String(), Kind: string(k)})
	}

	return ok(http.StatusOK, out, ""), nil
}

// callerGrants serves GET /api/server/caller/grants: one page of the
// current grants of the caller, in the shape of an access-review snapshot.
func callerGrants(
	_ http.ResponseWriter, r *http.Request, svc *access.Service, c call,
) (principalResult, error) {
	page, err := pageRequest(r)
	if err != nil {
		return failed(""), err
	}

	got, err := svc.CallerGrants(r.Context(), c.p, access.CallerGrantsRequest{Cursor: page.Cursor, Limit: page.Limit})
	if err != nil {
		return failed(""), err
	}

	return ok(http.StatusOK, reviewPageOf(got), ""), nil
}
