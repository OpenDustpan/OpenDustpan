package access

import (
	"context"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// ObjectsRequest asks for one page of the objects of one kind where a
// principal can do one action (ADR 52, decision 14). Kind is
// ObjectNamespace, ObjectCollection, ObjectUnit or ObjectPrincipal. Action
// empty means the default action of the kind: the read action, or
// principal:manage-tokens for ObjectPrincipal. Cursor and Limit page as in
// ReviewRequest.
type ObjectsRequest struct {
	Kind   ObjectKind
	Action identity.Action
	Cursor string
	Limit  int
}

// ObjectsPage is one page of the objects that grants name for an action.
// All is true, with no objects, when a grant on the installation covers
// every object. Each object is of the kind of the request, or a container
// of that kind: a namespace or a collection covers each unit inside it.
type ObjectsPage struct {
	Action     identity.Action
	NextCursor string
	Objects    []identity.Object
	Total      int64
	All        bool
}

// readActions is the default action of each object kind of an objects
// request.
//
//nolint:gochecknoglobals // A constant table; Go has no constant maps.
var readActions = map[ObjectKind]identity.Action{
	ObjectNamespace:  identity.ActionNamespaceRead,
	ObjectCollection: identity.ActionCollectionRead,
	ObjectUnit:       identity.ActionUnitRead,
	ObjectPrincipal:  identity.ActionPrincipalManageTokens,
}

// Access answers what p can do on obj and which grants give it: the "my
// access" of the console. It never answers for every object.
func (s *Service) Access(ctx context.Context, p identity.Principal, obj identity.Object) (Access, error) {
	if _, ok := KindOf(obj); !ok {
		return Access{}, fmt.Errorf("%w: object %q takes no grant", ErrInvalid, obj.String())
	}

	return s.authz.Explain(ctx, p, obj)
}

// Objects answers one page of the objects where p can do the action of req,
// as grants name them, in the order of their text form.
func (s *Service) Objects(ctx context.Context, p identity.Principal, req ObjectsRequest) (ObjectsPage, error) {
	page := ObjectsPage{Action: req.Action, NextCursor: "", Objects: nil, Total: 0, All: false}

	read, ok := readActions[req.Kind]
	if !ok {
		return page, fmt.Errorf("%w: type %q: write namespace, collection, unit or principal", ErrInvalid, req.Kind)
	}

	if page.Action == "" {
		page.Action = read
	}

	family, classified := FamilyOf(page.Action)
	if !classified {
		return page, fmt.Errorf("%w: unknown action %q", ErrInvalid, page.Action)
	}

	// A grant on the installation gives every action, so all=true for a
	// principal would also follow from an action that gives nothing on a
	// principal. Only a delegation action has a meaning on a principal.
	if req.Kind == ObjectPrincipal && family != FamilyDelegation {
		return page, fmt.Errorf("%w: action %q: type principal takes a principal action, such as %s",
			ErrInvalid, page.Action, identity.ActionPrincipalManageTokens)
	}

	limit, after, err := objectsPaging(req)
	if err != nil {
		return page, err
	}

	scope, err := s.authz.ListObjects(ctx, p, page.Action)
	if err != nil {
		return page, err
	}

	if scope.CoversEveryObject() {
		page.All = true

		return page, nil
	}

	objs := scopeObjects(scope, req.Kind)
	page.Total = int64(len(objs))
	page.Objects, page.NextCursor = pageOf(objs, after, limit)

	return page, nil
}

// pageOf returns the objects of the sorted list objs after the text form
// after, at most limit of them, and the cursor of the next page or empty.
func pageOf(objs []identity.Object, after string, limit int) ([]identity.Object, string) {
	start := 0
	for start < len(objs) && after != "" && objs[start].String() <= after {
		start++
	}

	end := min(start+limit, len(objs))
	if end == len(objs) {
		return objs[start:end], ""
	}

	return objs[start:end], base64.RawURLEncoding.EncodeToString([]byte(objs[end-1].String()))
}

// objectsPaging returns the limit and the key after which the page of req
// starts.
func objectsPaging(req ObjectsRequest) (int, string, error) {
	limit := req.Limit

	switch {
	case limit == 0:
		limit = DefaultLimit
	case limit < 1 || limit > MaxLimit:
		return 0, "", fmt.Errorf("%w: limit %d is not 1 to %d", ErrInvalid, req.Limit, MaxLimit)
	default:
	}

	if req.Cursor == "" {
		return limit, "", nil
	}

	if len(req.Cursor) > principal.MaxCursorLength {
		return 0, "", fmt.Errorf("%w: cursor longer than %d bytes", ErrInvalid, principal.MaxCursorLength)
	}

	raw, err := base64.RawURLEncoding.DecodeString(req.Cursor)
	if err != nil || len(raw) == 0 || !utf8.Valid(raw) || slices.Contains(raw, 0) {
		return 0, "", fmt.Errorf("%w: cursor", ErrInvalid)
	}

	return limit, string(raw), nil
}

// scopeObjects returns the objects of scope that cover objects of kind,
// sorted by their text form. A principal is covered only by a grant on it:
// the grants on namespaces, collections and units do not reach principals.
func scopeObjects(scope Scope, kind ObjectKind) []identity.Object {
	if kind == ObjectPrincipal {
		return sortedObjects(slices.Clone(scope.Principals))
	}

	out := slices.Clone(scope.Namespaces)

	if kind == ObjectCollection || kind == ObjectUnit {
		out = append(out, scope.Collections...)
	}

	if kind == ObjectUnit {
		out = append(out, scope.Units...)
	}

	return sortedObjects(out)
}

// sortedObjects sorts objs by their text form and returns them.
func sortedObjects(objs []identity.Object) []identity.Object {
	slices.SortFunc(objs, func(a, b identity.Object) int {
		return strings.Compare(a.String(), b.String())
	})

	return objs
}

// CallerGrantsRequest asks for one page of the current grants of a caller.
// Cursor and Limit page as in ReviewRequest.
type CallerGrantsRequest struct {
	Cursor string
	Limit  int
}

// CallerGrants answers one page of the current grants of p: the "my access"
// list of the console. The page holds the grants of the principal of p and
// the grants of each group that it is a member of, one row for each
// membership, with the membership in Row.Via and the name of the group in
// Row.SubjectName. It is a snapshot at the time of the first page, in the
// order of an access-review export, with the total on the first page only.
//
// Each page reads the groups of p at the time of its request, and keeps a
// grant of a group only while the membership of p is current at the time
// of the snapshot. So a page after the first can leave out the grants of a
// group that p joined or left since the first page, and a grant revoked
// since. The store reads only the grants of p and its groups (ADR 52).
//
// A caller with no principal record, for example a bootstrap admin token
// (ADR 6), holds no grant: its page is empty. Access answers what such a
// caller can do. A bad request returns ErrInvalid, and a store that reads
// no review ErrNoReview.
func (s *Service) CallerGrants(ctx context.Context, p identity.Principal, req CallerGrantsRequest) (ReviewPage, error) {
	rs, ok := s.store.(ReviewStore)
	if !ok {
		return ReviewPage{}, ErrNoReview
	}

	subjects, err := s.authz.subjectsOf(ctx, p)
	if err != nil {
		return ReviewPage{}, err
	}

	own, principalID, groups := "", "", []string{}

	for _, subject := range subjects {
		switch subject.Kind {
		case SubjectPrincipal:
			own, principalID = subject.String(), subject.ID
		case SubjectGroup:
			groups = append(groups, subject.ID)
		default:
		}
	}

	obj := identity.CallerObject()

	review := ReviewRequest{From: nil, To: nil, Subject: own, Cursor: req.Cursor, Limit: req.Limit, Expand: false}

	q, _, err := reviewQuery(review, obj, s.Now())
	if err != nil {
		return ReviewPage{}, err
	}

	if own == "" {
		// With no subject, the query would read every grant.
		return noGrants(q), nil
	}

	_, grantAccess, err := s.authz.reviewRoles(ctx)
	if err != nil {
		return ReviewPage{}, err
	}

	// The rows read only the grants of the caller: none is on a container
	// of another object, so the scope names no object.
	read := func(ctx context.Context, q ReviewQuery) ([]ReviewRow, int64, error) {
		return rs.CallerGrants(ctx, CallerGrantsQuery{
			Groups: groups, Principal: principalID, After: q.After, AsOfMs: q.AsOfMs, Limit: q.Limit, Count: q.Count,
		})
	}

	return s.reviewEntries(ctx, rs, &reviewRead{read: read, grantAccess: grantAccess, obj: obj, q: q, boot: 0})
}

// noGrants returns the page of a caller with no grant at the time of the
// snapshot of q: no rows and no next page, and a total of 0 on the first
// page.
func noGrants(q ReviewQuery) ReviewPage {
	asOf := time.UnixMilli(q.AsOfMs).UTC()
	page := ReviewPage{AsOf: &asOf, Total: nil, NextCursor: "", Entries: []ReviewEntry{}}

	if q.After == (ReviewKey{ID: "", MemberID: "", AtMs: 0, MemberAtMs: 0}) {
		var zero int64
		page.Total = &zero
	}

	return page
}
