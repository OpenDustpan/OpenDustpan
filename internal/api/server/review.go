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
// The export of a collection holds the grants on the collection, and the
// grants on each unit that is in it now (deleted units that are not purged
// included). It answers 501 on a server that holds no collections.
//
// Each export request writes one audit entry of the kind read-access (ADR
// 52, decision 13): in the namespace of a namespace or unit export, in
// @server for the installation. A failed append answers 500 with no rows.
//
// GET /api/server/caller/access answers the access of the caller: on one
// object (?object=), or the objects of one kind where it can do an action
// (?type=&action=), paged. It needs caller:read only, so every signed-in
// principal reads its own access. With type=principal, the default action
// is principal:manage-tokens: the answer lists the service principals whose
// tokens the caller can make. There, all=true means every service principal
// of the installation, because principal:manage-tokens gives no right on a
// person; the answer never lists a person.
//
// GET /api/server/caller/grants answers the current grants of the caller,
// paged: its own grants and the grants of its groups, with the group
// named. It needs caller:read only, and writes no audit entry. Each page
// reads the groups of the caller at its request, and the memberships and
// the grants as of the snapshot time of the first page (asOfMs).

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
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
		`invalid access query: provide "object=<object>", or "type=namespace|collection|unit|principal" ` +
			`and an optional "action"`,
	)
)

// Route paths of the access review and the caller access.
const (
	serverReviewPath     = "/api/server/access-review"
	namespaceReviewPath  = "/api/server/namespaces/{namespace}/access-review"
	collectionReviewPath = "/api/server/namespaces/{namespace}/collections/{collection}/access-review"
	unitReviewPath       = "/api/server/namespaces/{namespace}/unit-access-review"
	// principalReviewPath answers who holds principal:manage-tokens on one
	// service principal, and so can act as it.
	principalReviewPath = "/api/server/principals/{principal}/access-review"
	callerAccessPath    = "/api/server/caller/access"
	callerGrantsPath    = "/api/server/caller/grants"
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
// LastAccessTracked is true on a row of a principal: on a grant whose
// subject is a principal, or on a row read through a group, for the member.
// LastAccessAtMs is the latest use of an access token or a console session
// of that principal, and is left out when no token and no session was ever
// used. A group row with no Via and a
// bootstrap token row carry neither field.
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
// MemberSource is "local", "scim" or "claim".
// MemberSourceID is the identity that the source names, or empty for a local membership.
// MemberSuspended is true when the membership is suspended at the snapshot time and grants nothing.
// It is always false in a history, which lists only active memberships.
type reviewViaResponse struct {
	MemberUntilMs   *int64 `json:"memberUntilMs"`
	Group           string `json:"group"`
	Member          string `json:"member"`
	MemberSubject   string `json:"memberSubject"`
	MemberSource    string `json:"memberSource"`
	MemberSourceID  string `json:"memberSourceId"` //nolint:tagliatelle // The tool-neutral API names this field in lower camel case.
	MemberFromMs    int64  `json:"memberFromMs"`
	MemberSuspended bool   `json:"memberSuspended"`
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
// sorted, and the grants that give them. MissingActions names, sorted, each
// other grantable action that the caller does not hold on the object, with
// the roles that give it.
//
// Contract: never remove or rename these fields.
type accessResponse struct {
	Object         string                  `json:"object"`
	Actions        []string                `json:"actions"`
	Grants         []grantResponse         `json:"grants"`
	MissingActions []missingActionResponse `json:"missingActions"`
}

// missingActionResponse is one action that the caller does not hold on the
// object, with the roles that give it.
type missingActionResponse struct {
	Action    string            `json:"action"`
	GrantedBy grantedByResponse `json:"grantedBy"`
}

// grantedByResponse names the roles that give one action, by role ID: every
// built-in role that holds it, and every custom role that holds it. Every
// signed-in principal holds role:read, so the answer always lists both.
type grantedByResponse struct {
	BuiltInRoles []string `json:"builtInRoles"`
	CustomRoles  []string `json:"customRoles"`
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
		{resolvePrincipalPath, principalReviewPath, identity.ActionServerReadAccess},
	}

	callers := []Route{
		{
			Method: http.MethodGet,
			Path:   callerAccessPath,
			Scope:  ScopeAuthorized,
			Actions: []identity.Action{
				identity.ActionCallerRead,
			},
			Resolve:      resolveCaller,
			ActionsFor:   nil,
			OpaqueDenial: false,
			Serve:        h.serveAccessRead(callerAccess),
		},
		{
			Method: http.MethodGet,
			Path:   callerGrantsPath,
			Scope:  ScopeAuthorized,
			Actions: []identity.Action{
				identity.ActionCallerRead,
			},
			Resolve:      resolveCaller,
			ActionsFor:   nil,
			OpaqueDenial: false,
			Serve:        h.serveAccessRead(callerGrants),
		},
	}

	out := make([]Route, 0, len(kinds)+len(callers))

	for _, k := range kinds {
		out = append(out, Route{
			Method: http.MethodGet,
			Path:   k.path,
			Scope:  ScopeAuthorized,
			Actions: []identity.Action{
				k.action,
			},
			Resolve: k.resolve, ActionsFor: nil, OpaqueDenial: false,
			Serve: h.serveAccessRead(h.reviewAccess(k.action)),
		})
	}

	return append(out, callers...)
}

// reviewAccess returns the operation of GET on an access-review path with
// the read-access action action: one page of the export of the object, and
// its audit entry.
func (h *Handler) reviewAccess(action identity.Action) accessOp {
	return func(_ http.ResponseWriter, r *http.Request, svc *access.Service, c *call) (principalResult, error) {
		req, err := reviewRequest(r)
		if err != nil {
			return failed(""), err
		}

		page, err := svc.Review(r.Context(), c.obj, req)
		if err != nil {
			appendErr := h.appendReviewFailure(r, c, action, reviewAuditObject(c.obj, req, nil), svc.Now())
			if appendErr != nil {
				h.logger.WarnContext(r.Context(), "the audit entry of a failed access review export was not written",
					slog.String("action", string(action)), slog.String("error", appendErr.Error()))

				return failed(""), reviewExportError{export: err, append: appendErr}
			}

			return failed(""), err
		}

		if err := h.appendReviewRead(r, c, action, reviewAuditObject(c.obj, req, page.AsOf), svc.Now()); err != nil {
			return failed(""), err
		}

		return ok(http.StatusOK, reviewPageOf(page), ""), nil
	}
}

// reviewExportError is the error of an export that failed and whose audit
// entry also failed. The error of the export decides the answer, as it does
// when the entry succeeds: the handler maps an error to a status by its wrap
// chain, and the error of the entry would answer its own class instead (an
// unavailable store answers 503, a fenced append 409). The error of the
// entry goes to the log, not to the message.
type reviewExportError struct {
	export error
	append error
}

func (e reviewExportError) Error() string {
	return e.export.Error()
}

// Unwrap returns the error of the export, which decides the answer.
func (e reviewExportError) Unwrap() error { return e.export }

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

// appendReviewRead writes the audit entry of one export request that
// succeeded: kind read-access with the success outcome. It fails closed:
// the caller answers the error and no rows. NewHandler refuses a config
// that serves the export with no audit store.
func (h *Handler) appendReviewRead(
	r *http.Request,
	c *call,
	action identity.Action,
	object string,
	at time.Time,
) error {
	entry := reviewEntry(r, c, action, object, at)
	entry.Outcome = audit.OutcomeSuccess

	return h.appendReviewEntry(r, c, entry)
}

// appendReviewFailure writes the audit entry of one export request that
// failed: kind read-access with the failure outcome. It fails closed as
// appendReviewRead does.
func (h *Handler) appendReviewFailure(
	r *http.Request,
	c *call,
	action identity.Action,
	object string,
	at time.Time,
) error {
	entry := reviewEntry(r, c, action, object, at)
	entry.Outcome = audit.OutcomeFailure

	return h.appendReviewEntry(r, c, entry)
}

// reviewEntry returns the audit entry of one export request: kind
// read-access, with the action, the object and the time of the request.
// The caller sets the outcome.
func reviewEntry(r *http.Request, c *call, action identity.Action, object string, at time.Time) audit.Entry {
	entry := callerEntry(r, c.p, action)
	entry.At, entry.Kind = at, audit.KindReadAccess
	entry.Object = object

	return entry
}

// appendReviewEntry appends the audit entry of one export request in the
// namespace of the object or in @server. It fails closed: the caller
// answers the error and no rows. NewHandler refuses a config that serves
// the export with no audit store.
func (h *Handler) appendReviewEntry(r *http.Request, c *call, entry audit.Entry) error {
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

	q := queryOf(r)
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

	// A row of a principal is tracked: a grant to a principal, or a row
	// read through a group, for the member. LastAccessAtMs is the latest
	// use of an access token or a console session of the principal, left
	// out when no token and no session was ever used. A group row with no Via and a bootstrap token row carry
	// neither field.
	if row.LastAccessTracked {
		tracked := true
		out.LastAccessTracked = &tracked
		out.LastAccessAtMs = millisOf(row.LastAccessAt)
	}

	if v := row.Via; v != nil {
		out.Via = &reviewViaResponse{
			MemberUntilMs: millisOf(v.RemovedAt), Group: row.Grant.Subject.String(),
			Member:        access.Subject{Kind: access.SubjectPrincipal, ID: v.PrincipalID}.String(),
			MemberSubject: v.PrincipalSubject, MemberFromMs: v.AddedAt.UnixMilli(),
			MemberSource: v.Source, MemberSourceID: v.SourceID, MemberSuspended: v.Suspended,
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
	_ http.ResponseWriter, r *http.Request, svc *access.Service, c *call,
) (principalResult, error) {
	q := queryOf(r)
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

	actions := make([]string, 0, len(got.Actions))
	for _, a := range got.Actions {
		actions = append(actions, string(a))
	}

	grants := make([]grantResponse, 0, len(got.Grants))
	for i := range got.Grants {
		grants = append(grants, grantOf(got.Grants[i]))
	}

	missing, err := missingActionsOf(r.Context(), svc, got)
	if err != nil {
		return failed(""), err
	}

	return ok(http.StatusOK, accessResponse{
		Object: got.Object.String(), Actions: actions, Grants: grants, MissingActions: missing,
	}, ""), nil
}

// missingActionsOf returns each grantable action that held does not name,
// sorted, with the roles that give it. The self actions are not grantable:
// every signed-in principal holds them with no grant, so they are never
// missing. A grant on the installation gives every action of its role on
// every object, so each role that holds a missing action can give it,
// whatever the object is.
func missingActionsOf(
	ctx context.Context, svc *access.Service, held access.Access,
) ([]missingActionResponse, error) {
	missing := missingActionList(held)
	if len(missing) == 0 {
		return []missingActionResponse{}, nil
	}

	sets, err := roleSetsFor(ctx, svc)
	if err != nil {
		return nil, err
	}

	out := make([]missingActionResponse, 0, len(missing))
	for _, a := range missing {
		out = append(out, missingActionOf(a, sets))
	}

	return out, nil
}

// missingActionList returns each grantable action that held does not name,
// sorted.
func missingActionList(held access.Access) []identity.Action {
	have := make(map[identity.Action]bool, len(held.Actions))
	for _, a := range held.Actions {
		have[a] = true
	}

	var out []identity.Action

	for _, a := range grantableActions() {
		if !have[a] {
			out = append(out, a)
		}
	}

	return out
}

// roleSets holds the roles for missing actions: every built-in role, and
// every custom role.
type roleSets struct {
	builtIn []access.Role
	custom  []access.Role
}

// roleSetsFor returns the roles for the missing actions: every built-in
// role, and every custom role in the catalog. A grant on the installation
// reaches every object through its chain, so each role that holds a missing
// action can give it, whatever the object is. Every signed-in principal
// holds role:read, so the answer always lists the custom roles.
func roleSetsFor(ctx context.Context, svc *access.Service) (roleSets, error) {
	var sets roleSets

	sets.builtIn = access.BuiltInRoles()

	roles, err := svc.ListRoles(ctx)
	if err != nil {
		return sets, fmt.Errorf("list roles: %w", err)
	}

	for i := range roles {
		if !roles[i].BuiltIn {
			sets.custom = append(sets.custom, roles[i])
		}
	}

	return sets, nil
}

// missingActionOf returns action with the roles of sets that give it.
func missingActionOf(action identity.Action, sets roleSets) missingActionResponse {
	entry := missingActionResponse{
		Action: string(action),
		GrantedBy: grantedByResponse{
			BuiltInRoles: []string{}, CustomRoles: []string{},
		},
	}

	for i := range sets.builtIn {
		if sets.builtIn[i].Has(action) {
			entry.GrantedBy.BuiltInRoles = append(entry.GrantedBy.BuiltInRoles, sets.builtIn[i].ID)
		}
	}

	for i := range sets.custom {
		if sets.custom[i].Has(action) {
			entry.GrantedBy.CustomRoles = append(entry.GrantedBy.CustomRoles, sets.custom[i].ID)
		}
	}

	return entry
}

// grantableActions returns every classified action that a grant can give,
// sorted: every family but the self family.
func grantableActions() []identity.Action {
	out := append(access.Actions(access.FamilyOperator), access.Actions(access.FamilyAdministration)...)
	out = append(out, access.Actions(access.FamilyData)...)
	out = append(out, access.Actions(access.FamilyDelegation)...)
	slices.Sort(out)

	return out
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
		Kind: kind, Action: identity.Action(queryOf(r).Get("action")), Cursor: page.Cursor, Limit: page.Limit,
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

	for i := range got.Objects {
		obj := got.Objects[i]
		k, _ := access.KindOf(obj)
		out.Objects = append(out.Objects, accessObjectResponse{Object: obj.String(), Kind: string(k)})
	}

	return ok(http.StatusOK, out, ""), nil
}

// callerGrants serves GET /api/server/caller/grants: one page of the
// current grants of the caller, in the shape of an access-review snapshot.
func callerGrants(
	_ http.ResponseWriter, r *http.Request, svc *access.Service, c *call,
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
