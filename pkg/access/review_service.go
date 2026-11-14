package access

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
)

// ErrNoReview reports a Service whose store reads no access review.
var ErrNoReview = errors.New("the store reads no access review")

// ErrCollectionReview reports an export of a collection on a Service whose
// authorizer has no collection port that lists the units of a collection.
// The export of a collection must hold the grants on the units inside it, so
// the export is refused instead of an incomplete answer.
var ErrCollectionReview = errors.New("the export of a collection needs a store that holds collections")

// ErrCollectionUnitCap reports the refusal of the export of a collection
// that holds more units than the cap of SettingCollectionUnitCap allows. The
// export is refused: no truncation and no paging.
var ErrCollectionUnitCap = errors.New("the collection holds more units than the export allows")

// CollectionUnitCapError is the refusal of the export of a collection that
// holds more units than the cap of SettingCollectionUnitCap. Key is the key
// of the setting, Count the count of the units of the collection, and Cap
// the cap.
type CollectionUnitCapError struct {
	Key   string
	Count int
	Cap   int
}

// Error implements error. The text names the count, the cap and the setting
// key.
func (e *CollectionUnitCapError) Error() string {
	return fmt.Sprintf("the collection holds %d units; the export allows at most %d; change the setting %s",
		e.Count, e.Cap, e.Key)
}

// Unwrap returns ErrCollectionUnitCap.
func (e *CollectionUnitCapError) Unwrap() error { return ErrCollectionUnitCap }

// CollectionUnits lists the units of a collection, for the export of a
// collection and for the read of deleted units. The collection package
// implements it. CollectionUnitIDs returns the stable IDs of the units in the
// collection obj, deleted units that are not purged included. It returns an
// error when the collection does not exist. A unit of another collection is
// never in the answer.
//
// obj carries the stable ID of the collection. An implementation returns an
// error when no collection with that name and that ID exists, so the units of a
// collection that was created again under the same name are never returned for
// the ID of the old one.
type CollectionUnits interface {
	CollectionUnitIDs(ctx context.Context, collection identity.Object) ([]string, error)
}

// unitTargetPrefix starts the target of a grant on a unit: the prefix and the
// stable ID of the unit.
const unitTargetPrefix = "unit:"

// BootstrapHolder is the holder of a row of an export that a bootstrap
// admin token gives: it holds every action and has no grant (ADR 6).
const BootstrapHolder = "bootstrap admin token"

// ReviewRequest asks for one page of the access-review export of an object
// (ADR 52, decision 10).
//
// From and To are both nil for a snapshot, or both set for a history of the
// grants whose life overlaps [From, To). A snapshot reads the grants that
// were current at the time of its first page, and its cursor keeps that
// time, so every page reads the same snapshot. Subject, when it is not
// empty, is the text form of a subject: the export then keeps its grants.
// For a principal it also keeps the grants of its groups, for the time it
// was a member. Expand puts each member of a group in its own row. Cursor
// is the NextCursor of the page before, or empty, at most
// principal.MaxCursorLength bytes. Limit is 1 to MaxLimit, and 0 reads
// DefaultLimit rows.
type ReviewRequest struct {
	From    *time.Time
	To      *time.Time
	Subject string
	Cursor  string
	Limit   int
	Expand  bool
}

// Object states of the object of a grant in an export.
const (
	// ObjectLive is an object that exists.
	ObjectLive = "live"
	// ObjectDeleted is a unit that was deleted (ADR 47). A restore can bring
	// it back with its grants.
	ObjectDeleted = "deleted"
	// ObjectPurged is a unit that no longer exists.
	ObjectPurged = "purged"
)

// ReviewEntry is one row of an export.
//
// InheritedFrom is the object that the grant names when that object
// contains the object of the export, so the grant applies to it from above.
// It is nil for a grant on the object of the export or inside it.
// CanGrantAccess is true when the role of the row holds an action that
// changes who has access to an object (managesAccess): the holder can give
// itself access. ObjectState is ObjectLive, ObjectDeleted or
// ObjectPurged. EffectiveFrom and EffectiveUntil are the time that the row
// gave access: the life of the grant, and of the membership for a row
// through a group. EffectiveUntil is nil while it lasts. Holder is
// BootstrapHolder for a row of a bootstrap admin token, which has no grant
// and names the subject of the token in Row.SubjectName, and empty for any
// other row.
type ReviewEntry struct {
	InheritedFrom  *identity.Object
	EffectiveUntil *time.Time
	EffectiveFrom  time.Time
	ObjectState    string
	Holder         string
	Row            ReviewRow
	CanGrantAccess bool
}

// ReviewPage is one page of an export and the cursor of the next page
// (empty on the last page). Total is the count of every row of the export,
// on the first page only: it is nil on the pages after it. AsOf is the time
// of a snapshot, and nil for a history.
type ReviewPage struct {
	AsOf       *time.Time
	Total      *int64
	NextCursor string
	Entries    []ReviewEntry
}

// Review answers one page of the access-review export of obj: who can
// access obj, and what is inside it.
//
// The export of the installation holds every grant. The export of a
// namespace or a unit holds the grants on the object and inside it, and the
// grants on each object that contains it with a role that gives a data
// action or manages access. So an operator grant on the installation, which
// gives no data access, is not in it. In a history, a grant on a container
// with a custom role is in it, a deleted role included. An export starts with
// one row for each bootstrap admin token, inside the limit of its pages,
// unless the request names a subject.
//
// A namespace of obj must exist (ErrNotFound). The export of a collection
// holds the grants on the collection, the grants on each unit that is in it
// now (a deleted unit that is not purged included, with the state deleted),
// and the grants on the containers of the collection. A collection that does
// not exist returns ErrNotFound, an authorizer with no collection port
// ErrCollectionReview, and a collection over the cap of
// SettingCollectionUnitCap a CollectionUnitCapError. A bad request returns
// ErrInvalid, and a store that reads no review ErrNoReview.
//
// Each page reads collection membership again, so units that join or leave
// between pages change the next page. The snapshot fixes the time of the
// grants, not collection membership.
//
// Review reads only. The caller writes the audit entry of the export.
func (s *Service) Review(ctx context.Context, obj identity.Object, req ReviewRequest) (ReviewPage, error) {
	rs, ok := s.store.(ReviewStore)
	if !ok {
		return ReviewPage{}, ErrNoReview
	}

	q, boot, err := reviewQuery(req, obj, s.Now())
	if err != nil {
		return ReviewPage{}, err
	}

	var grantAccess map[string]bool
	if q.Scope, grantAccess, err = s.reviewScope(ctx, obj); err != nil {
		return ReviewPage{}, err
	}

	return s.reviewEntries(ctx, rs, &reviewRead{
		read: rs.ReviewGrants, grantAccess: grantAccess, obj: obj, q: q, boot: boot,
	})
}

// reviewReader reads one page of grant rows of an export and, when the
// query asks, their count.
type reviewReader func(ctx context.Context, q ReviewQuery) ([]ReviewRow, int64, error)

// reviewRead is one page read of an export: the reader of its rows, the
// object of the export, its query with its scope, the count of bootstrap
// rows that the pages before gave, and the roles that manage access.
type reviewRead struct {
	read        reviewReader
	grantAccess map[string]bool
	obj         identity.Object
	q           ReviewQuery
	boot        int
}

// reviewEntries reads the page of r and returns it with the state of each
// unit and who can grant access. It takes r by pointer: the query it holds
// is too heavy to copy.
func (s *Service) reviewEntries(ctx context.Context, rs ReviewStore, r *reviewRead) (ReviewPage, error) {
	page, rows, err := s.reviewPage(ctx, r.read, r.obj, r.q, r.boot)
	if err != nil {
		return ReviewPage{}, err
	}

	states, err := s.unitStates(ctx, rs, rows)
	if err != nil {
		return ReviewPage{}, err
	}

	uses, err := s.lastAccess(ctx, rs, rows)
	if err != nil {
		return ReviewPage{}, err
	}

	setLastAccess(rows, uses)

	for i := range rows {
		page.Entries = append(page.Entries, reviewEntry(r.q, rows[i], states, r.grantAccess))
	}

	return page, nil
}

// reviewPage reads one page of the export of obj with the query q, after
// boot bootstrap rows that the pages before gave. It answers the page with
// its bootstrap rows, its time, its total and its next cursor, and the grant
// rows that come after the bootstrap rows. The bootstrap rows come first,
// inside the limit of a page.
func (s *Service) reviewPage(
	ctx context.Context, read reviewReader, obj identity.Object, q ReviewQuery, boot int,
) (ReviewPage, []ReviewRow, error) {
	var tokens []ReviewEntry
	if q.Subject == "" {
		tokens = s.bootstrapEntries(q.Scope)
	}

	pending := tokens[min(boot, len(tokens)):]
	take := min(len(pending), q.Limit)
	room := q.Limit - take

	first := q.After == (ReviewKey{ID: "", MemberID: "", AtMs: 0, MemberAtMs: 0}) && boot == 0
	q.Limit, q.Count = room+1, first

	rows, total, err := read(ctx, q)
	if err != nil {
		return ReviewPage{}, nil, fmt.Errorf("read access review: %w", err)
	}

	page := ReviewPage{
		AsOf:       nil,
		Total:      nil,
		NextCursor: "",
		Entries:    make([]ReviewEntry, 0, take+min(len(rows), room)),
	}
	page.Entries = append(page.Entries, pending[:take]...)

	if !q.History {
		asOf := time.UnixMilli(q.AsOfMs).UTC()
		page.AsOf = &asOf
	}

	if first {
		total += int64(len(tokens))
		page.Total = &total
	}

	next := reviewPosition{binding: reviewBinding(obj, q), key: q.After, asOfMs: q.AsOfMs, boot: boot + take}

	switch {
	case take < len(pending):
		page.NextCursor = encodeReviewCursor(next)
	case len(rows) > room:
		rows = rows[:room]
		if room > 0 {
			next.key = rows[room-1].Key()
		}

		next.boot = len(tokens)
		page.NextCursor = encodeReviewCursor(next)
	default:
	}

	return page, rows, nil
}

// reviewEntry returns the entry of one row of the export of q.
func reviewEntry(q ReviewQuery, row ReviewRow, states map[string]UnitState, grantAccess map[string]bool) ReviewEntry {
	e := ReviewEntry{
		InheritedFrom: nil, EffectiveUntil: nil, EffectiveFrom: row.Grant.CreatedAt, ObjectState: ObjectLive,
		Holder: "", Row: row, CanGrantAccess: grantAccess[row.Grant.Role],
	}

	if row.Grant.Object.IsUnit() {
		state, found := states[row.Grant.Object.UnitID()]

		switch {
		case !found:
			e.ObjectState = ObjectPurged
		case state.Deleted:
			e.ObjectState, e.Row.Grant.Object = ObjectDeleted, state.Object
		default:
			e.Row.Grant.Object = state.Object
		}
	}

	e.InheritedFrom = inheritedFrom(q.Scope, e.Row.Grant.Object)

	if !q.History {
		// A snapshot shows the rows as they were at its time: a revoke or a
		// membership end after it has not happened yet.
		e.Row.RevokedAt, e.Row.RevokedBy = nil, ""

		if e.Row.Via != nil {
			via := *e.Row.Via
			via.RemovedAt = nil
			e.Row.Via = &via
		}
	} else {
		// The member count of a group is a count at one time: a history
		// leaves it out.
		e.Row.MemberCount = nil
	}

	e.EffectiveUntil = e.Row.RevokedAt

	if e.Row.Via != nil {
		if e.Row.Via.AddedAt.After(e.EffectiveFrom) {
			e.EffectiveFrom = e.Row.Via.AddedAt
		}

		e.EffectiveUntil = earliest(e.EffectiveUntil, e.Row.Via.RemovedAt)
	}

	return e
}

// earliest returns the earlier of a and b, where nil is a time with no end.
func earliest(a, b *time.Time) *time.Time {
	switch {
	case a == nil:
		return b
	case b == nil || a.Before(*b):
		return a
	default:
		return b
	}
}

// bootstrapEntries returns one row for each bootstrap admin token: the
// holder of every action on the installation, with no grant (ADR 6).
func (s *Service) bootstrapEntries(scope ReviewScope) []ReviewEntry {
	out := make([]ReviewEntry, 0, len(s.bootstrap))
	server := identity.ServerObject()

	for _, subject := range s.bootstrap {
		e := ReviewEntry{
			InheritedFrom: nil, EffectiveUntil: nil, EffectiveFrom: time.Time{}, ObjectState: ObjectLive,
			Holder: BootstrapHolder, CanGrantAccess: true,
			Row: ReviewRow{
				RevokedAt: nil, LastAccessAt: nil, MemberCount: nil, Via: nil, SubjectName: subject, RevokedBy: "",
				Grant: Grant{
					CreatedAt: time.Time{}, Condition: nil, Object: server, ID: "", Role: RoleAdmin,
					CreatedBy: "", Subject: Subject{Kind: "", ID: ""},
				},
				// A bootstrap row carries no last access.
				LastAccessTracked: false,
			},
		}

		if !scope.All {
			e.InheritedFrom = &server
		}

		out = append(out, e)
	}

	return out
}

// unitStates reads the state of each unit that the grants of rows name.
func (s *Service) unitStates(ctx context.Context, rs ReviewStore, rows []ReviewRow) (map[string]UnitState, error) {
	var ids []string

	for i := range rows {
		if id := rows[i].Grant.Object.UnitID(); id != "" {
			ids = append(ids, id)
		}
	}

	if len(ids) == 0 {
		return map[string]UnitState{}, nil
	}

	states, err := rs.ReviewUnits(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("read units of grants: %w", err)
	}

	return states, nil
}

// lastAccess reads the latest last-use time of the access tokens and the
// console sessions of each principal of rows, with one lookup for the page.
func (s *Service) lastAccess(ctx context.Context, rs ReviewStore, rows []ReviewRow) (map[string]time.Time, error) {
	uses, err := rs.LastAccess(ctx, principalIDsOf(rows))
	if err != nil {
		return nil, fmt.Errorf("read last access: %w", err)
	}

	return uses, nil
}

// principalOfRow returns the ID of the principal that a row speaks of: the
// member of a row read through a group, or the subject of a grant to a
// principal. It is empty for a group row with no Via.
func principalOfRow(row *ReviewRow) string {
	switch {
	case row.Via != nil:
		return row.Via.PrincipalID
	case row.Grant.Subject.Kind == SubjectPrincipal:
		return row.Grant.Subject.ID
	default:
		return ""
	}
}

// principalIDsOf returns the IDs of the principals of rows, deduplicated.
func principalIDsOf(rows []ReviewRow) []string {
	var ids []string

	seen := map[string]bool{}

	for i := range rows {
		id := principalOfRow(&rows[i])
		if id == "" || seen[id] {
			continue
		}

		seen[id] = true
		ids = append(ids, id)
	}

	return ids
}

// setLastAccess sets the last access of each row of rows from uses: a row
// of a principal is tracked, with the latest use of its tokens and its
// console sessions, or with no time when none was ever used. A group row with no Via keeps neither.
func setLastAccess(rows []ReviewRow, uses map[string]time.Time) {
	for i := range rows {
		id := principalOfRow(&rows[i])
		if id == "" {
			continue
		}

		rows[i].LastAccessTracked = true

		if at, ok := uses[id]; ok {
			used := at
			rows[i].LastAccessAt = &used
		}
	}
}

// reviewScope returns the scope of the export of obj, and the roles that
// manage access.
func (s *Service) reviewScope(ctx context.Context, obj identity.Object) (ReviewScope, map[string]bool, error) {
	scope := ReviewScope{
		Object: identity.Object{}, Namespace: "", Inside: nil, Containers: nil, ContainerRoles: nil, TokenNamespace: "",
		TokenNamespaceRoles: nil, All: false,
	}

	roles, grantAccess, err := s.authz.reviewRoles(ctx)
	if err != nil {
		return scope, nil, err
	}

	kind, ok := KindOf(obj)
	if !ok {
		return scope, nil, fmt.Errorf("%w: object %q has no access review", ErrInvalid, obj.String())
	}

	if kind == ObjectInstallation {
		scope.All = true

		return scope, grantAccess, nil
	}

	if kind == ObjectCollection {
		scope, err = s.collectionReviewScope(ctx, obj, roles)

		return scope, grantAccess, err
	}

	if kind == ObjectPrincipal {
		scope, err = s.principalReviewScope(ctx, obj)

		return scope, grantAccess, err
	}

	if nsErr := s.reviewNamespace(ctx, obj); nsErr != nil {
		return scope, nil, nsErr
	}

	if kind == ObjectNamespace {
		scope.Namespace = obj.Namespace().String()
	} else {
		// A unit export follows the stable ID of the unit, as its grants do.
		if obj, err = s.existingUnit(ctx, obj); err != nil {
			return scope, nil, err
		}

		scope.Object = obj
	}

	chain, err := s.authz.chain(ctx, obj)
	if err != nil {
		return scope, nil, err
	}

	// The chain starts with obj itself: every later object contains it.
	for i := 1; i < len(chain); i++ {
		scope.Containers = append(scope.Containers, chain[i].String())
	}

	scope.ContainerRoles = roles

	return scope, grantAccess, nil
}

// collectionReviewScope returns the scope of the export of obj, a collection:
// the grants on the collection by its stable ID, the grants on the units that
// are in it now, and the grants with a data role on the objects that contain
// it. It fails closed: it returns ErrNotFound for a collection that does not
// exist, ErrCollectionReview when no port lists the units, and a
// CollectionUnitCapError for a collection over the cap, so a partial answer
// is never given.
func (s *Service) collectionReviewScope(
	ctx context.Context, obj identity.Object, roles []string,
) (ReviewScope, error) {
	scope := ReviewScope{
		Object: identity.Object{}, Namespace: "", Inside: nil, Containers: nil, ContainerRoles: nil,
		TokenNamespace: "", TokenNamespaceRoles: nil, All: false,
	}

	if err := s.reviewNamespace(ctx, obj); err != nil {
		return scope, err
	}

	units, ok := s.authz.collections.(CollectionUnits)
	if !ok {
		return scope, fmt.Errorf("export of %q: %w", obj.String(), ErrCollectionReview)
	}

	col, inside, err := s.collectionInside(ctx, obj, units)
	if err != nil {
		return scope, err
	}

	chain, err := s.authz.chain(ctx, col)
	if err != nil {
		return scope, err
	}

	// The chain starts with the collection: every later object contains it.
	for i := 1; i < len(chain); i++ {
		scope.Containers = append(scope.Containers, chain[i].String())
	}

	scope.Object, scope.Inside, scope.ContainerRoles = col, inside, roles

	return scope, nil
}

// collectionInside returns the collection obj with its current stable ID, and
// the targets of the grants on the units in it. It returns ErrNotFound when
// the collection does not exist or when obj carries the ID of another
// collection of the same name, and a CollectionUnitCapError when the
// collection holds more units than the cap of SettingCollectionUnitCap.
func (s *Service) collectionInside(
	ctx context.Context, obj identity.Object, units CollectionUnits,
) (identity.Object, []string, error) {
	// Defense in depth: the unit list is read for the stable ID of the
	// collection only. A name that has no collection stops here, so the scope
	// never holds the units of another collection.
	col, err := collectionIDOf(ctx, obj, s.authz.collections)
	if err != nil {
		return obj, nil, err
	}

	// Defense in depth: collectionIDOf keeps an ID that the caller gives, and
	// the collection port checks the ID in its own transaction too. This check
	// stays so that an ID of a collection that was created again under the
	// same name never selects the units of the new collection.
	current, found, err := s.authz.collections.CollectionID(ctx, col)
	if err != nil {
		return obj, nil, fmt.Errorf("read collection ID: %w", err)
	}

	if col.CollectionID() == "" || !found || current != col.CollectionID() {
		return obj, nil, fmt.Errorf("%w: collection %q does not exist", ErrNotFound, obj.String())
	}

	ids, err := units.CollectionUnitIDs(ctx, col)
	if err != nil {
		return obj, nil, fmt.Errorf("read units of collection %q: %w", obj.String(), err)
	}

	inside := make([]string, 0, len(ids))

	for _, id := range ids {
		// An empty ID would make the target "unit:", which no grant holds.
		if id != "" {
			inside = append(inside, unitTargetPrefix+id)
		}
	}

	// The export reads every unit of the collection, so a collection over the
	// cap is refused instead of a truncated answer. The cap is read at each
	// export, and a reader that fails gives the default cap.
	// Defense in depth: the parse refuses a value above MaxCollectionUnitCap
	// too, and this bound stays so that a new caller or a change of the parse
	// cannot turn off the refusal.
	unitCap := boundCollectionUnitCap(collectionUnitCap(ctx, s.settings))

	if len(inside) > unitCap {
		return obj, nil, &CollectionUnitCapError{Key: SettingCollectionUnitCap, Count: len(inside), Cap: unitCap}
	}

	return col, inside, nil
}

// principalReviewScope returns the scope of the export of obj, a service
// principal: the grants on the principal, the grants on the installation
// with a role that holds principal:manage-tokens, and, for a principal of
// a namespace, the grants on its namespace with a role that holds
// namespace:manage-principal-tokens. So the export answers who can make a
// token for the principal and act as it. It returns ErrNotFound when the
// principal does not exist, and ErrInvalid when it is not a service
// principal.
func (s *Service) principalReviewScope(ctx context.Context, obj identity.Object) (ReviewScope, error) {
	scope := ReviewScope{
		Object: obj, Namespace: "", Inside: nil, Containers: nil, ContainerRoles: nil, TokenNamespace: "",
		TokenNamespaceRoles: nil, All: false,
	}

	p, err := s.directory.GetPrincipal(ctx, obj.PrincipalID())
	if errors.Is(err, principal.ErrNotFound) {
		return scope, fmt.Errorf("%w: principal %q", ErrNotFound, obj.PrincipalID())
	}

	if err != nil {
		return scope, fmt.Errorf("read principal: %w", err)
	}

	if p.Kind != principal.KindService {
		return scope, fmt.Errorf("%w: principal %q is a %s principal; only a service principal has this export",
			ErrInvalid, p.ID, p.Kind)
	}

	if scope.ContainerRoles, err = s.authz.rolesWith(ctx, identity.ActionPrincipalManageTokens); err != nil {
		return scope, err
	}

	scope.Containers = []string{identity.ServerObject().String()}

	if p.Namespace != "" {
		scope.TokenNamespace = identity.NamespaceObject(p.Namespace).String()

		tokenRoles, rolesErr := s.authz.rolesWith(ctx, identity.ActionNamespaceManagePrincipalTokens)
		if rolesErr != nil {
			return scope, rolesErr
		}

		scope.TokenNamespaceRoles = tokenRoles
	}

	return scope, nil
}

// TokenManagers returns the current holders of principal:manage-tokens on
// the service principal with the ID id, sorted and each once: the text form
// of the subject of each grant that gives it, and the subject of each
// bootstrap admin token. A group stands for each of its members.
func (s *Service) TokenManagers(ctx context.Context, id string) ([]string, error) {
	obj, err := identity.ParsePrincipalObject(id)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	seen := map[string]bool{}
	req := ReviewRequest{From: nil, To: nil, Subject: "", Cursor: "", Limit: MaxLimit, Expand: false}

	for {
		page, err := s.Review(ctx, obj, req)
		if err != nil {
			return nil, fmt.Errorf("read token managers of principal %q: %w", id, err)
		}

		for i := range page.Entries {
			row := page.Entries[i].Row
			if page.Entries[i].Holder == BootstrapHolder {
				seen[row.SubjectName] = true
			} else {
				seen[row.Grant.Subject.String()] = true
			}
		}

		if page.NextCursor == "" {
			break
		}

		req.Cursor = page.NextCursor
	}

	return slices.Sorted(maps.Keys(seen)), nil
}

// reviewNamespace returns ErrNotFound when the namespace of obj does not
// exist.
func (s *Service) reviewNamespace(ctx context.Context, obj identity.Object) error {
	if _, err := s.namespaces.GetNamespace(ctx, obj.Namespace()); err != nil {
		if errors.Is(err, namespace.ErrNotFound) {
			return fmt.Errorf("%w: namespace %q", ErrNotFound, obj.Namespace().String())
		}

		return fmt.Errorf("read namespace: %w", err)
	}

	return nil
}

// inheritedFrom returns the object of a grant when it is a container of the
// scope, or the token namespace of the scope, else nil.
func inheritedFrom(scope ReviewScope, obj identity.Object) *identity.Object {
	for _, c := range scope.Containers {
		if c == obj.String() {
			return &obj
		}
	}

	if scope.TokenNamespace != "" && obj.String() == scope.TokenNamespace {
		return &obj
	}

	return nil
}

// reviewQuery checks req for the export of obj and returns its query, with
// no scope yet, and the count of bootstrap rows that the pages before gave.
// A snapshot with no cursor reads at now. A cursor of another export (other
// object, mode, window, subject or expand) is ErrInvalid.
func reviewQuery(req ReviewRequest, obj identity.Object, now time.Time) (ReviewQuery, int, error) {
	q := ReviewQuery{
		Scope: ReviewScope{
			Object:              identity.Object{},
			Namespace:           "",
			Inside:              nil,
			Containers:          nil,
			ContainerRoles:      nil,
			TokenNamespace:      "",
			TokenNamespaceRoles: nil,
			All:                 false,
		},
		Subject:   "",
		Principal: "",
		After:     ReviewKey{ID: "", MemberID: "", AtMs: 0, MemberAtMs: 0},
		AsOfMs:    0,
		FromMs:    0,
		ToMs:      0,
		Limit:     req.Limit,
		History:   false,
		Expand:    req.Expand,
		Count:     false,
	}

	switch {
	case q.Limit == 0:
		q.Limit = DefaultLimit
	case q.Limit < 1 || q.Limit > MaxLimit:
		return ReviewQuery{}, 0, fmt.Errorf("%w: limit %d is not 1 to %d", ErrInvalid, req.Limit, MaxLimit)
	default:
	}

	if err := reviewWindow(&q, req); err != nil {
		return ReviewQuery{}, 0, err
	}

	if err := reviewSubject(&q, req.Subject); err != nil {
		return ReviewQuery{}, 0, err
	}

	pos, err := decodeReviewCursor(req.Cursor)
	if err != nil {
		return ReviewQuery{}, 0, err
	}

	if req.Cursor != "" && pos.binding != reviewBinding(obj, q) {
		return ReviewQuery{}, 0, fmt.Errorf("%w: the cursor is of another export", ErrInvalid)
	}

	q.After = pos.key

	if !q.History {
		q.AsOfMs = pos.asOfMs
		if req.Cursor == "" {
			q.AsOfMs = now.UnixMilli()
		}
	}

	return q, pos.boot, nil
}

// reviewSubject sets the subject filter raw (the text form of a subject,
// or empty) on q.
func reviewSubject(q *ReviewQuery, raw string) error {
	if raw == "" {
		return nil
	}

	subject, err := ParseSubject(raw)
	if err != nil {
		return err
	}

	q.Subject = subject.String()
	if subject.Kind == SubjectPrincipal {
		q.Principal = subject.ID
	}

	return nil
}

// reviewWindow sets the history window of req on q: both ends or neither,
// and From before To.
func reviewWindow(q *ReviewQuery, req ReviewRequest) error {
	switch {
	case req.From == nil && req.To == nil:
		return nil
	case req.From == nil || req.To == nil:
		return fmt.Errorf("%w: a history needs both from and to", ErrInvalid)
	case !req.From.Before(*req.To):
		return fmt.Errorf("%w: from must be before to", ErrInvalid)
	default:
		q.History, q.FromMs, q.ToMs = true, req.From.UnixMilli(), req.To.UnixMilli()

		return nil
	}
}

// reviewPosition is the position of a page in an export: the time of a
// snapshot (0 for a history), the key of the last grant row, the count of
// bootstrap rows that the pages before gave, and the binding of the export.
type reviewPosition struct {
	binding string
	key     ReviewKey
	asOfMs  int64
	boot    int
}

// reviewBinding returns the binding of the export of obj with the query q:
// a short hash of the object, the mode and its window, the subject filter
// and expand. A cursor holds it, so a cursor of one export cannot page
// another. The scope comes from the URL and its authorization, so the
// binding is no secret and needs no key.
func reviewBinding(obj identity.Object, q ReviewQuery) string {
	mode := "snapshot"
	if q.History {
		mode = "history " + strconv.FormatInt(q.FromMs, 10) + "-" + strconv.FormatInt(q.ToMs, 10)
	}

	sum := sha256.Sum256([]byte(strings.Join([]string{
		obj.String(), mode, q.Subject, strconv.FormatBool(q.Expand),
	}, "\x00")))

	return hex.EncodeToString(sum[:reviewBindingBytes])
}

// validCursorParts reports whether the text parts of a decoded cursor are
// well formed: seven parts, IDs that are empty or valid, and a binding of
// the right length.
func validCursorParts(parts []string) bool {
	idOK := func(id string) bool { return id == "" || validSubjectID(id) }

	return len(parts) == 7 && idOK(parts[2]) && idOK(parts[4]) && len(parts[6]) == 2*reviewBindingBytes
}

// reviewBindingBytes is the length of the binding hash in a cursor, in
// bytes.
const reviewBindingBytes = 8

// encodeReviewCursor returns the opaque cursor of the position p.
func encodeReviewCursor(p reviewPosition) string {
	raw := strings.Join([]string{
		strconv.FormatInt(p.asOfMs, 10), strconv.FormatInt(p.key.AtMs, 10), p.key.ID,
		strconv.FormatInt(p.key.MemberAtMs, 10), p.key.MemberID, strconv.Itoa(p.boot), p.binding,
	}, "\x00")

	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeReviewCursor returns the position of the cursor c, the zero
// position for an empty cursor, or ErrInvalid.
func decodeReviewCursor(c string) (reviewPosition, error) {
	zero := reviewPosition{
		binding: "",
		key:     ReviewKey{ID: "", MemberID: "", AtMs: 0, MemberAtMs: 0},
		asOfMs:  0,
		boot:    0,
	}
	if c == "" {
		return zero, nil
	}

	bad := fmt.Errorf("%w: cursor", ErrInvalid)

	if len(c) > principal.MaxCursorLength {
		return zero, fmt.Errorf("%w: cursor longer than %d bytes", ErrInvalid, principal.MaxCursorLength)
	}

	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return zero, bad
	}

	parts := strings.Split(string(raw), "\x00")
	if !validCursorParts(parts) {
		return zero, bad
	}

	var nums [4]int64

	// The last number is the count of bootstrap rows and becomes an int. Its
	// bit size is the size of int, so a value that an int cannot hold is
	// rejected here and never wraps.
	bits := [4]int{64, 64, 64, strconv.IntSize}

	for i, part := range []string{parts[0], parts[1], parts[3], parts[5]} {
		if nums[i], err = strconv.ParseInt(part, 10, bits[i]); err != nil || nums[i] < 0 {
			return zero, bad
		}
	}

	return reviewPosition{
		binding: parts[6], key: ReviewKey{ID: parts[2], MemberID: parts[4], AtMs: nums[1], MemberAtMs: nums[2]},
		asOfMs: nums[0], boot: int(nums[3]),
	}, nil
}
