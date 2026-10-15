package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var (
	_ access.Store   = (*Store)(nil)
	_ access.Callers = (*Store)(nil)
)

// CreateGrant implements access.GrantStore.
func (s *Store) CreateGrant(ctx context.Context, g access.Grant, fence namespace.Fence, e audit.Entry) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inGrantTx(ctx, g.Object.Namespace(), fence, e, func(q *db.Queries) error {
		return insertGrant(ctx, q, g)
	})
}

// insertGrant inserts g with q.
func insertGrant(ctx context.Context, q *db.Queries, g access.Grant) error {
	kind, ok := access.KindOf(g.Object)
	if !ok {
		return fmt.Errorf("%w: object %q takes no grant", access.ErrInvalid, g.Object.String())
	}

	target, err := targetOf(g.Object)
	if err != nil {
		return err
	}

	ended, err := q.CountEndedGrantRecords(ctx, db.CountEndedGrantRecordsParams{
		SubjectKind: string(g.Subject.Kind), SubjectID: g.Subject.ID, Role: g.Role,
	})
	if err != nil {
		return fmt.Errorf("read records of grant: %w", err)
	}

	if ended > 0 {
		return fmt.Errorf(
			"%w: the subject %s or the role %q was deleted",
			access.ErrNotFound,
			g.Subject.String(),
			g.Role,
		)
	}

	n, err := q.CreateGrant(ctx, db.CreateGrantParams{
		ID:          g.ID,
		Subject:     g.Subject.String(),
		SubjectKind: string(g.Subject.Kind),
		SubjectID:   g.Subject.ID,
		Role:        g.Role,
		Object:      g.Object.String(),
		Target:      target,
		ObjectKind:  string(kind),
		Namespace:   g.Object.Namespace().String(),
		Condition:   toNullString(g.Condition),
		CreatedAtMs: codec.Millis(g.CreatedAt),
		CreatedBy:   g.CreatedBy,
	})
	if err != nil {
		return fmt.Errorf("insert grant: %w", err)
	}

	if n == 0 {
		return access.ErrAlreadyExists
	}

	return nil
}

// GetGrant implements access.GrantStore.
func (s *Store) GetGrant(ctx context.Context, id string) (access.Grant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetGrant(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return access.Grant{}, access.ErrNotFound
	}

	if err != nil {
		return access.Grant{}, fmt.Errorf("select grant: %w", err)
	}

	return fromGrantRow(grantRow(row))
}

// RevokeGrant implements access.GrantStore. It ends the grant in the
// transaction of the entry, after it checks that the grant is in the
// namespace of fence.
func (s *Store) RevokeGrant(ctx context.Context, id string, fence namespace.Fence, e audit.Entry) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inGrantTx(ctx, fence.Namespace, fence, e, func(q *db.Queries) error {
		if err := grantInNamespace(ctx, q, id, fence.Namespace); err != nil {
			return err
		}

		n, err := q.RevokeGrant(ctx, db.RevokeGrantParams{
			RevokedAtMs: nullMillis(codec.Millis(e.At)), RevokedBy: e.Subject, ID: id,
		})
		if err != nil {
			return fmt.Errorf("revoke grant: %w", err)
		}

		if n == 0 {
			return access.ErrNotFound
		}

		return nil
	})
}

// ListGrantsOn implements access.GrantStore. It reads the page and the
// total in one transaction.
func (s *Store) ListGrantsOn(
	ctx context.Context, obj identity.Object, gq access.GrantQuery,
) ([]access.ListedGrant, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var (
		out   []access.ListedGrant
		total int64
	)

	target, targetErr := targetOf(obj)
	if targetErr != nil {
		return nil, 0, targetErr
	}

	err := s.inTx(ctx, func(q *db.Queries) error {
		rows, err := q.ListGrantsOn(ctx, db.ListGrantsOnParams{
			Target: target, Subject: gq.Subject, Role: gq.Role, AfterSubject: gq.AfterSubject, AfterRole: gq.AfterRole,
			RowLimit: int64(gq.Limit),
		})
		if err != nil {
			return fmt.Errorf("select grants on object: %w", err)
		}

		if out, err = listedGrants(rows); err != nil {
			return err
		}

		total, err = q.CountGrantsOn(ctx, db.CountGrantsOnParams{Target: target, Subject: gq.Subject, Role: gq.Role})
		if err != nil {
			return fmt.Errorf("count grants on object: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	return out, total, nil
}

// GrantsOn implements access.GrantStore.
func (s *Store) GrantsOn(
	ctx context.Context, subjects []access.Subject, objects []identity.Object,
) ([]access.Grant, error) {
	if len(subjects) == 0 || len(objects) == 0 {
		return nil, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.queries.GrantsOn(ctx, db.GrantsOnParams{
		Subjects: subjectStrings(subjects),
		Targets:  targetStrings(objects),
	})
	if err != nil {
		return nil, fmt.Errorf("select grants on objects: %w", err)
	}

	return fromGrantRows(rows)
}

// GrantsOf implements access.GrantStore.
func (s *Store) GrantsOf(ctx context.Context, subject access.Subject) ([]access.Grant, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.queries.GrantsOfSubject(ctx, db.GrantsOfSubjectParams{
		SubjectKind: string(subject.Kind), SubjectID: subject.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("select grants of subject: %w", err)
	}

	return fromGrantRows(rows)
}

// RoleGrantedBelowInstallation implements access.GrantStore.
func (s *Store) RoleGrantedBelowInstallation(ctx context.Context, role string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n, err := s.queries.CountRoleGrantsBelowInstallation(ctx, role)
	if err != nil {
		return false, fmt.Errorf("count grants of role: %w", err)
	}

	return n > 0, nil
}

// RoleGrantedOnPrincipal implements access.GrantStore.
func (s *Store) RoleGrantedOnPrincipal(ctx context.Context, role string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	n, err := s.queries.CountRoleGrantsOnPrincipal(ctx, role)
	if err != nil {
		return false, fmt.Errorf("count grants of role: %w", err)
	}

	return n > 0, nil
}

// GrantsWithRoles implements access.GrantStore.
func (s *Store) GrantsWithRoles(
	ctx context.Context,
	subjects []access.Subject,
	roles []string,
) ([]access.Grant, error) {
	if len(subjects) == 0 || len(roles) == 0 {
		return nil, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.queries.GrantsWithRoles(ctx, db.GrantsWithRolesParams{
		Subjects: subjectStrings(subjects),
		Roles:    roles,
	})
	if err != nil {
		return nil, fmt.Errorf("select grants with roles: %w", err)
	}

	return fromGrantRows(rows)
}

// CreateNamespaceWithGrant implements access.GrantStore: the namespace, the
// grant of its creator and the entry commit together. A grant with no ID
// stores the namespace and the entry alone.
func (s *Store) CreateNamespaceWithGrant(
	ctx context.Context, ns namespace.Namespace, g access.Grant, e audit.Entry,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := codec.ToNamespaceRow(ns)

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		n, err := q.CreateNamespace(ctx, db.CreateNamespaceParams{
			Name:            row.Name,
			HomeRegion:      row.HomeRegion,
			FailoverVersion: row.FailoverVersion,
			CreatedAtMs:     row.CreatedAtMs,
		})
		if err != nil {
			return fmt.Errorf("insert namespace: %w", err)
		}

		if n == 0 {
			return namespace.ErrAlreadyExists
		}

		if g.ID == "" {
			return nil
		}

		return insertGrant(ctx, q, g)
	})
}

// SubjectsOf implements access.Callers: the principal with the subject and
// its current groups, in one query.
func (s *Store) SubjectsOf(ctx context.Context, subject string) ([]access.Subject, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.queries.SubjectsOfCaller(ctx, subject)
	if err != nil {
		return nil, fmt.Errorf("select subjects of caller: %w", err)
	}

	return callerSubjects(rows), nil
}

// callerSubjects maps the rows of SubjectsOfCaller to subjects: the
// principal once, then each group.
func callerSubjects(rows []db.SubjectsOfCallerRow) []access.Subject {
	if len(rows) == 0 {
		return nil
	}

	out := make([]access.Subject, 0, len(rows)+1)
	out = append(out, access.Subject{Kind: access.SubjectPrincipal, ID: rows[0].PrincipalID})

	for i := range rows {
		if rows[i].GroupID != "" {
			out = append(out, access.Subject{Kind: access.SubjectGroup, ID: rows[i].GroupID})
		}
	}

	return out
}

// CreateRole implements access.RoleStore.
func (s *Store) CreateRole(ctx context.Context, r access.Role, e audit.Entry) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		n, err := q.CreateRole(ctx, db.CreateRoleParams{
			ID:          r.ID,
			Name:        r.Name,
			NameKey:     strings.ToLower(r.Name),
			Description: r.Description,
			Actions:     encodeActions(r.Actions),
			CreatedAtMs: codec.Millis(r.CreatedAt),
			UpdatedAtMs: codec.Millis(r.UpdatedAt),
		})
		if err != nil {
			return fmt.Errorf("insert role: %w", err)
		}

		if n == 0 {
			return access.ErrAlreadyExists
		}

		return nil
	})
}

// GetRole implements access.RoleStore.
func (s *Store) GetRole(ctx context.Context, id string) (access.Role, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetRole(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return access.Role{}, access.ErrNotFound
	}

	if err != nil {
		return access.Role{}, fmt.Errorf("select role: %w", err)
	}

	return fromRoleRow(roleRow(row)), nil
}

// GetRoles implements access.RoleStore.
func (s *Store) GetRoles(ctx context.Context, ids []string) ([]access.Role, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.queries.RolesByIDs(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("select roles by ID: %w", err)
	}

	return mapRows(rows, func(r db.RolesByIDsRow) access.Role { return fromRoleRow(roleRow(r)) }), nil
}

// ListRoles implements access.RoleStore.
func (s *Store) ListRoles(ctx context.Context) ([]access.Role, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.queries.ListRoles(ctx)
	if err != nil {
		return nil, fmt.Errorf("select roles: %w", err)
	}

	return mapRows(rows, func(r db.ListRolesRow) access.Role { return fromRoleRow(roleRow(r)) }), nil
}

// UpdateRole implements access.RoleStore. It refuses a name that another
// role holds without regard to case, in the same transaction as the write.
func (s *Store) UpdateRole(ctx context.Context, r access.Role, e audit.Entry) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := strings.ToLower(r.Name)

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		holder, err := q.RoleIDByNameKey(ctx, key)

		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return fmt.Errorf("select role by name: %w", err)
		case holder != r.ID:
			return access.ErrAlreadyExists
		default:
		}

		n, err := q.UpdateRole(ctx, db.UpdateRoleParams{
			Name:        r.Name,
			NameKey:     key,
			Description: r.Description,
			Actions:     encodeActions(r.Actions),
			UpdatedAtMs: codec.Millis(r.UpdatedAt),
			ID:          r.ID,
		})
		if isUniqueViolation(err) {
			return access.ErrAlreadyExists
		}

		if err != nil {
			return fmt.Errorf("update role: %w", err)
		}

		if n == 0 {
			return access.ErrNotFound
		}

		return nil
	})
}

// DeleteRole implements access.RoleStore. It refuses a role that a current
// grant holds, and ends the role, in the transaction of the entry.
func (s *Store) DeleteRole(ctx context.Context, id string, e audit.Entry) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		held, err := q.CountGrantsWithRole(ctx, id)
		if err != nil {
			return fmt.Errorf("count grants with role: %w", err)
		}

		if held > 0 {
			return fmt.Errorf("%w: %d grants hold it", access.ErrInUse, held)
		}

		n, err := q.DeleteRole(ctx, db.DeleteRoleParams{DeletedAtMs: nullMillis(codec.Millis(e.At)), ID: id})
		if err != nil {
			return fmt.Errorf("end role: %w", err)
		}

		if n == 0 {
			return access.ErrNotFound
		}

		return nil
	})
}

// revokeGrantsOf ends every current grant of subject with q, at the time of
// e and by its author, in the transaction that ends the subject.
func revokeGrantsOf(ctx context.Context, q *db.Queries, subject access.Subject, e audit.Entry) error {
	err := q.RevokeGrantsOfSubject(ctx, db.RevokeGrantsOfSubjectParams{
		RevokedAtMs: nullMillis(codec.Millis(e.At)),
		RevokedBy:   e.Subject,
		SubjectKind: string(subject.Kind),
		SubjectID:   subject.ID,
	})
	if err != nil {
		return fmt.Errorf("revoke grants of %s: %w", subject.String(), err)
	}

	if subject.Kind != access.SubjectPrincipal {
		return nil
	}

	// A grant on the principal, such as the right to make its tokens, ends
	// with it too.
	_, err = q.RevokeGrantsOnTarget(ctx, db.RevokeGrantsOnTargetParams{
		RevokedAtMs: nullMillis(codec.Millis(e.At)),
		RevokedBy:   e.Subject,
		Target:      identity.PrincipalObject(subject.ID).String(),
	})
	if err != nil {
		return fmt.Errorf("revoke grants on %s: %w", subject.String(), err)
	}

	return nil
}

// grantReadRow is each generated grant row. The grant reads select the same
// columns, so sqlc generates one type for each of them.
type grantReadRow interface {
	db.GetGrantRow | db.GrantsOnRow | db.GrantsWithRolesRow | db.GrantsOfSubjectRow
}

// listedGrants maps the rows of a grant list, with their holders.
func listedGrants(rows []db.ListGrantsOnRow) ([]access.ListedGrant, error) {
	out := make([]access.ListedGrant, 0, len(rows))

	for i := range rows {
		r := &rows[i]

		g, err := fromGrantRow(db.GetGrantRow{
			ID:          r.ID,
			Subject:     r.Subject,
			SubjectKind: r.SubjectKind,
			SubjectID:   r.SubjectID,
			Role:        r.Role,
			Object:      r.Object,
			Target:      r.Target,
			ObjectKind:  r.ObjectKind,
			Namespace:   r.Namespace,
			Condition:   r.Condition,
			CreatedAtMs: r.CreatedAtMs,
			CreatedBy:   r.CreatedBy,
		})
		if err != nil {
			return nil, err
		}

		out = append(out, access.ListedGrant{Holder: access.Holder{Name: r.HolderName, Kind: r.HolderKind}, Grant: g})
	}

	return out, nil
}

// grantRow maps any generated grant row to the row of GetGrant.
func grantRow[T grantReadRow](row T) db.GetGrantRow {
	return db.GetGrantRow(row)
}

// fromGrantRows maps grant rows to grants.
func fromGrantRows[T grantReadRow](rows []T) ([]access.Grant, error) {
	out := make([]access.Grant, 0, len(rows))

	for i := range rows {
		g, err := fromGrantRow(grantRow(rows[i]))
		if err != nil {
			return nil, err
		}

		out = append(out, g)
	}

	return out, nil
}

// fromGrantRow maps one grant row to a grant.
func fromGrantRow(row db.GetGrantRow) (access.Grant, error) {
	obj, err := identity.ParseObject(row.Object)
	if err != nil {
		return access.Grant{}, fmt.Errorf("decode grant object: %w", err)
	}

	if id, isUnit := strings.CutPrefix(row.Target, unitTargetPrefix); isUnit {
		obj = obj.WithUnitID(id)
	}

	var condition *string
	if row.Condition.Valid {
		c := row.Condition.String
		condition = &c
	}

	return access.Grant{
		CreatedAt: codec.FromMillis(row.CreatedAtMs),
		Condition: condition,
		Object:    obj,
		ID:        row.ID,
		Role:      row.Role,
		CreatedBy: row.CreatedBy,
		Subject:   access.Subject{Kind: access.SubjectKind(row.SubjectKind), ID: row.SubjectID},
	}, nil
}

// roleRow maps any generated role row to the row of GetRole.
func roleRow[T db.GetRoleRow | db.RolesByIDsRow | db.ListRolesRow](row T) db.GetRoleRow {
	return db.GetRoleRow(row)
}

// fromRoleRow maps one roles row to a custom role.
func fromRoleRow(row db.GetRoleRow) access.Role {
	return access.Role{
		CreatedAt:   codec.FromMillis(row.CreatedAtMs),
		UpdatedAt:   codec.FromMillis(row.UpdatedAtMs),
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description,
		Actions:     decodeActions(row.Actions),
		BuiltIn:     false,
	}
}

// encodeActions stores actions as one space-separated text.
func encodeActions(actions []identity.Action) string {
	parts := make([]string, 0, len(actions))
	for _, a := range actions {
		parts = append(parts, string(a))
	}

	return strings.Join(parts, " ")
}

// decodeActions reads the text of encodeActions.
func decodeActions(text string) []identity.Action {
	fields := strings.Fields(text)

	out := make([]identity.Action, 0, len(fields))
	for _, f := range fields {
		out = append(out, identity.Action(f))
	}

	return out
}

func subjectStrings(subjects []access.Subject) []string {
	out := make([]string, 0, len(subjects))
	for _, s := range subjects {
		out = append(out, s.String())
	}

	return out
}

// targetStrings returns the targets of objects. A unit with no stable ID
// has no target and is left out: it matches no grant.
func targetStrings(objects []identity.Object) []string {
	out := make([]string, 0, len(objects))

	for _, o := range objects {
		if t, err := targetOf(o); err == nil {
			out = append(out, t)
		}
	}

	return out
}

// unitTargetPrefix starts the target of a unit grant: the stable ID of the
// unit follows it.
const unitTargetPrefix = "unit:"

// targetOf returns what a grant on obj matches: unit:<stable ID> for a unit,
// and the wire form for every other object. A unit with no stable ID
// returns access.ErrInvalid.
func targetOf(obj identity.Object) (string, error) {
	if !obj.IsUnit() {
		return obj.String(), nil
	}

	if obj.UnitID() == "" {
		return "", fmt.Errorf("%w: unit %q has no stable ID", access.ErrInvalid, obj.String())
	}

	return unitTargetPrefix + obj.UnitID(), nil
}

// toNullString maps an optional string to a nullable column.
func toNullString(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{String: "", Valid: false}
	}

	return sql.NullString{String: *s, Valid: true}
}

// inGrantTx runs fn, a grant write on an object in the namespace ns, with
// its entry e. A write on the installation (ns "") takes the zero fence and
// appends e in the scope @server. Any other write takes the fence of ns, and
// appends e to the chain of ns in the same fenced transaction. Writes are
// serialized (_txlock=immediate), so a delete of a subject or a role that
// commits first wins.
func (s *Store) inGrantTx(
	ctx context.Context, ns namespace.Name, fence namespace.Fence, e audit.Entry, fn func(q *db.Queries) error,
) error {
	if err := grantFence(ns, fence); err != nil {
		return err
	}

	if ns == "" {
		return s.inAuditedTx(ctx, e, fn)
	}

	return s.inFencedAuditedTx(ctx, fence, &e, fn)
}

// grantInNamespace returns access.ErrNotFound when no current grant with
// the ID id is in the namespace ns ("" for the installation).
func grantInNamespace(ctx context.Context, q *db.Queries, id string, ns namespace.Name) error {
	row, err := q.GetGrant(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return access.ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("select grant: %w", err)
	}

	if row.Namespace != ns.String() {
		return access.ErrNotFound
	}

	return nil
}

// grantFence returns access.ErrInvalid when fence is not the fence of a
// grant write in the namespace ns: the zero fence for the installation (ns
// ""), and a fence of ns for any other object.
func grantFence(ns namespace.Name, fence namespace.Fence) error {
	if ns == "" && fence != (namespace.Fence{Namespace: "", FailoverVersion: 0}) {
		return fmt.Errorf("%w: a grant on the installation takes no fence", access.ErrInvalid)
	}

	if ns != "" && fence.Namespace != ns {
		return fmt.Errorf("%w: the fence names %q, the grant is in %q", access.ErrInvalid, fence.Namespace, ns)
	}

	return nil
}
