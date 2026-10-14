package access

import (
	"context"
	"encoding/base64"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
)

// ObjectsRequest asks for one page of the objects of one kind where a
// principal can do one action (ADR 52, decision 14). Kind is
// ObjectNamespace, ObjectCollection or ObjectUnit. Action empty means the
// read action of the kind. Cursor and Limit page as in ReviewRequest.
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

// readActions is the read action of each object kind of an objects
// request.
//
//nolint:gochecknoglobals // A constant table; Go has no constant maps.
var readActions = map[ObjectKind]identity.Action{
	ObjectNamespace:  identity.ActionNamespaceRead,
	ObjectCollection: identity.ActionCollectionRead,
	ObjectUnit:       identity.ActionUnitRead,
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
		return page, fmt.Errorf("%w: type %q: write namespace, collection or unit", ErrInvalid, req.Kind)
	}

	if page.Action == "" {
		page.Action = read
	}

	if _, classified := FamilyOf(page.Action); !classified {
		return page, fmt.Errorf("%w: unknown action %q", ErrInvalid, page.Action)
	}

	limit, after, err := objectsPaging(req)
	if err != nil {
		return page, err
	}

	scope, err := s.authz.ListObjects(ctx, p, page.Action)
	if err != nil {
		return page, err
	}

	if scope.All {
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
// sorted by their text form.
func scopeObjects(scope Scope, kind ObjectKind) []identity.Object {
	out := slices.Clone(scope.Namespaces)

	if kind == ObjectCollection || kind == ObjectUnit {
		out = append(out, scope.Collections...)
	}

	if kind == ObjectUnit {
		out = append(out, scope.Units...)
	}

	slices.SortFunc(out, func(a, b identity.Object) int {
		return strings.Compare(a.String(), b.String())
	})

	return out
}
