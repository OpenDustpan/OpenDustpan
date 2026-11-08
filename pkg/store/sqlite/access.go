package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var (
	_ access.Store   = (*Store)(nil)
	_ access.Callers = (*Store)(nil)
)

// CreateGrant implements access.GrantStore. It runs check in the
// transaction of the write, after the fence check.
func (s *Store) CreateGrant(
	ctx context.Context, g access.Grant, fence namespace.Fence, e audit.Entry, check access.GrantCheck,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inGrantTx(ctx, g.Object.Namespace(), fence, e, func(q *db.Queries) error {
		if err := runCheck(ctx, q, check); err != nil {
			return err
		}

		return insertGrant(ctx, q, g)
	})
}

// runCheck runs check, when it is not nil, with reads in the transaction of
// q. The transaction is IMMEDIATE, so it holds the write lock of the
// database.
func runCheck(ctx context.Context, q *db.Queries, check access.GrantCheck) error {
	if check == nil {
		return nil
	}

	return check(ctx, grantReads{q: q})
}

// grantReads implements access.GrantReads with the queries of one
// transaction, or of the database. It takes no lock of the Store: the
// caller holds it.
type grantReads struct {
	q *db.Queries
}

var (
	_ access.GrantReads    = grantReads{q: nil}
	_ principal.GroupReads = grantReads{q: nil}
)

// ActsAs implements principal.GroupReads with the rule of access.ActsAs.
func (r grantReads) ActsAs(ctx context.Context, groupID string) ([]string, error) {
	ids, err := access.ActsAs(ctx, r, access.Subject{Kind: access.SubjectGroup, ID: groupID})
	if err != nil {
		return nil, fmt.Errorf("read what group %q acts as: %w", groupID, err)
	}

	return ids, nil
}

// UnitCollectionID implements access.GrantReads.
func (r grantReads) UnitCollectionID(ctx context.Context, unitID string) (string, bool, error) {
	id, err := r.q.UnitCollectionID(ctx, unitID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("select collection of unit: %w", err)
	}

	return id, true, nil
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

	if fitErr := subjectFitsObject(ctx, q, g); fitErr != nil {
		return fitErr
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

// subjectFitsObject returns ErrInvalid when the subject of g is a principal
// of a namespace and the object of g is not that namespace or an object
// inside it. It reads the namespace of the principal in the transaction of
// the grant, so a grant cannot race the record it checks.
func subjectFitsObject(ctx context.Context, q *db.Queries, g access.Grant) error {
	if g.Subject.Kind != access.SubjectPrincipal {
		return nil
	}

	ns, err := q.GetPrincipalNamespace(ctx, g.Subject.ID)
	if errors.Is(err, sql.ErrNoRows) {
		// The Service checks that the subject exists.
		return nil
	}

	if err != nil {
		return fmt.Errorf("select namespace of principal: %w", err)
	}

	if ns != "" && ns != g.Object.Namespace().String() {
		return fmt.Errorf("%w: %s is a principal of the namespace %q, and can only hold grants inside it",
			access.ErrInvalid, g.Subject.String(), ns)
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

// RevokeGrant implements access.GrantStore. It runs check, then ends the grant in the
// transaction of the entry, after it checks that the grant is in the
// namespace of fence.
//
// It runs on the control path, so it works in maintenance mode: a grant only
// allows, and a limit is a ceiling, so ending a grant never widens access.
// The check, the fence and the entry stay. The transaction journals the
// revocation in maintenance mode, and a restore applies it again.
func (s *Store) RevokeGrant(
	ctx context.Context, id string, fence namespace.Fence, e audit.Entry, check access.GrantCheck,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rev := revocation{kind: maintenance.RevokedGrant, target: id, endReason: ""}

	return s.inRevokingGrantTx(ctx, fence, e, rev, guardSiteAdministrator(ctx, func(q *db.Queries) error {
		if err := runCheck(ctx, q, check); err != nil {
			return err
		}

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
	}))
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

	err := s.inReadTx(ctx, func(q *db.Queries) error {
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

	return grantReads{q: s.queries}.GrantsOn(ctx, subjects, objects)
}

// GrantsOn implements access.GrantReads.
func (r grantReads) GrantsOn(
	ctx context.Context, subjects []access.Subject, objects []identity.Object,
) ([]access.Grant, error) {
	if len(subjects) == 0 || len(objects) == 0 {
		return nil, nil
	}

	rows, err := r.q.GrantsOn(ctx, db.GrantsOnParams{
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

	return grantReads{q: s.queries}.GrantsOf(ctx, subject)
}

// GrantsOf implements access.GrantReads.
func (r grantReads) GrantsOf(ctx context.Context, subject access.Subject) ([]access.Grant, error) {
	rows, err := r.q.GrantsOfSubject(ctx, db.GrantsOfSubjectParams{
		SubjectKind: string(subject.Kind), SubjectID: subject.ID,
	})
	if err != nil {
		return nil, fmt.Errorf("select grants of subject: %w", err)
	}

	return fromGrantRows(rows)
}

// GrantsOfSubjects implements access.GrantLookups. It reads the grants of
// every subject of subjects with one query.
func (s *Store) GrantsOfSubjects(ctx context.Context, subjects []access.Subject) ([]access.Grant, error) {
	if len(subjects) == 0 {
		return nil, nil
	}

	// Defense in depth: ProtectedGroups and the list routes refuse more than
	// one page of IDs. This check stays so that a new caller cannot send an
	// unbounded list, which would also pass the variable limit of SQLite.
	if len(subjects) > principal.MaxLimit {
		return nil, fmt.Errorf("%w: %d subjects, at most %d", access.ErrInvalid, len(subjects), principal.MaxLimit)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.queries.GrantsOfSubjects(ctx, subjectStrings(subjects))
	if err != nil {
		return nil, fmt.Errorf("select grants of subjects: %w", err)
	}

	return fromGrantRows(rows)
}

// RoleGrantedBelowInstallation implements access.GrantStore.
func (s *Store) RoleGrantedBelowInstallation(ctx context.Context, role string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return grantReads{q: s.queries}.RoleGrantedBelowInstallation(ctx, role)
}

// RoleGrantedBelowInstallation implements access.GrantReads.
func (r grantReads) RoleGrantedBelowInstallation(ctx context.Context, role string) (bool, error) {
	n, err := r.q.CountRoleGrantsBelowInstallation(ctx, role)
	if err != nil {
		return false, fmt.Errorf("count grants of role: %w", err)
	}

	return n > 0, nil
}

// RoleGrantedOnPrincipal implements access.GrantStore.
func (s *Store) RoleGrantedOnPrincipal(ctx context.Context, role string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return grantReads{q: s.queries}.RoleGrantedOnPrincipal(ctx, role)
}

// RoleGrantedOnPrincipal implements access.GrantReads.
func (r grantReads) RoleGrantedOnPrincipal(ctx context.Context, role string) (bool, error) {
	n, err := r.q.CountRoleGrantsOnPrincipal(ctx, role)
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
// grant of its creator and the entries commit together. A grant with no ID
// stores the namespace and the entry alone. With a grant it stores the entry
// in the scope @server and the same grant entry in the chain of the new
// namespace, so the chain starts with the grant of its creator. The namespace
// did not exist before, so no fence check runs.
func (s *Store) CreateNamespaceWithGrant(
	ctx context.Context, ns namespace.Namespace, g access.Grant, e audit.Entry,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row := codec.ToNamespaceRow(ns)

	grantEntry, err := validateCreatorGrantEntries(ns, g, e)
	if err != nil {
		return err
	}

	return s.inTx(ctx, func(q *db.Queries) error {
		if err := insertNamespaceRow(ctx, q, row); err != nil {
			return err
		}

		return appendCreatorGrantEntries(ctx, q, g, e, grantEntry)
	})
}

// validateCreatorGrantEntries checks the entries of CreateNamespaceWithGrant:
// e lives in the scope @server, and the copy of e in the chain of the new
// namespace is valid when the grant has an ID.
func validateCreatorGrantEntries(
	ns namespace.Namespace, g access.Grant, e audit.Entry,
) (audit.Entry, error) {
	if err := validServerEntry(e); err != nil {
		return audit.Entry{}, err
	}

	grantEntry := e
	grantEntry.Namespace = ns.Name

	if g.ID == "" {
		return grantEntry, nil
	}

	if err := grantEntry.Validate(); err != nil {
		return audit.Entry{}, fmt.Errorf("audit entry: %w", err)
	}

	return grantEntry, nil
}

// insertNamespaceRow inserts the namespace row. The namespace did not exist
// before, so no fence check runs. A row without a valid ID is
// namespace.ErrInvalidID, and a taken name or ID is
// namespace.ErrAlreadyExists.
func insertNamespaceRow(ctx context.Context, q *db.Queries, row codec.NamespaceRow) error {
	if _, err := namespace.ParseID(row.ID); err != nil {
		return err
	}

	n, err := q.CreateNamespace(ctx, db.CreateNamespaceParams{
		Name:                row.Name,
		PrimaryInstallation: row.PrimaryInstallation,
		FailoverVersion:     row.FailoverVersion,
		CreatedAtMs:         row.CreatedAtMs,
		ID:                  row.ID,
	})
	if err != nil {
		return fmt.Errorf("insert namespace: %w", err)
	}

	if n == 0 {
		return namespace.ErrAlreadyExists
	}

	return nil
}

// appendCreatorGrantEntries stores the entries of CreateNamespaceWithGrant.
// With no grant (g.ID empty) it appends only the create entry e. With a
// grant it inserts the grant, appends e in @server, and appends the grant
// entry in the new namespace chain.
func appendCreatorGrantEntries(
	ctx context.Context,
	q *db.Queries,
	g access.Grant,
	e audit.Entry,
	grantEntry audit.Entry,
) error {
	if g.ID == "" {
		if _, err := appendAuditEntry(ctx, q, e); err != nil {
			return fmt.Errorf("append audit entry: %w", err)
		}

		return nil
	}

	if err := insertGrant(ctx, q, g); err != nil {
		return err
	}

	if _, err := appendAuditEntry(ctx, q, e); err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}

	if _, err := appendAuditEntry(ctx, q, grantEntry); err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}

	return nil
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

// SubjectsOf implements access.GrantReads. A disabled principal has no
// subjects, so a disable that commits before the check refuses the write.
func (r grantReads) SubjectsOf(ctx context.Context, subject string) ([]access.Subject, error) {
	rows, err := r.q.SubjectsOfEnabledCaller(ctx, subject)
	if err != nil {
		return nil, fmt.Errorf("select subjects of caller: %w", err)
	}

	return callerSubjects(mapRows(rows, func(r db.SubjectsOfEnabledCallerRow) db.SubjectsOfCallerRow {
		return db.SubjectsOfCallerRow(r)
	})), nil
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

	return grantReads{q: s.queries}.GetRoles(ctx, ids)
}

// GetRoles implements access.GrantReads.
func (r grantReads) GetRoles(ctx context.Context, ids []string) ([]access.Role, error) {
	if len(ids) == 0 {
		return nil, nil
	}

	rows, err := r.q.RolesByIDs(ctx, ids)
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

// UpdateRole implements access.RoleStore. It runs check, then refuses a
// name that another role holds without regard to case, in the transaction
// of the write.
func (s *Store) UpdateRole(ctx context.Context, r access.Role, e audit.Entry, check access.GrantCheck) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := strings.ToLower(r.Name)

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		if err := runCheck(ctx, q, check); err != nil {
			return err
		}

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
// e and by its author, in the transaction that ends the subject. For a
// delete in the scope @server it also writes one audit entry in each
// namespace that holds an ended grant, so a namespace auditor sees that the
// access ended. A delete in a namespace scope writes no such entry: its own
// entry is already in that namespace.
func revokeGrantsOf(ctx context.Context, q *db.Queries, subject access.Subject, e audit.Entry) error {
	rows, err := q.GrantsOfSubject(ctx, db.GrantsOfSubjectParams{
		SubjectKind: string(subject.Kind), SubjectID: subject.ID,
	})
	if err != nil {
		return fmt.Errorf("select grants of %s: %w", subject.String(), err)
	}

	err = q.RevokeGrantsOfSubject(ctx, db.RevokeGrantsOfSubjectParams{
		RevokedAtMs: nullMillis(codec.Millis(e.At)),
		RevokedBy:   e.Subject,
		SubjectKind: string(subject.Kind),
		SubjectID:   subject.ID,
	})
	if err != nil {
		return fmt.Errorf("revoke grants of %s: %w", subject.String(), err)
	}

	if subject.Kind != access.SubjectPrincipal {
		return auditSubjectRevokes(ctx, q, rows, subject, e)
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

	return auditSubjectRevokes(ctx, q, rows, subject, e)
}

// Change keys of the audit entry that the delete of a subject writes for
// each grant it ends in a namespace: the grant, and the delete that ended
// it.
const (
	revokeGrantKey   = "grant"
	revokeSubjectKey = "subject"
	revokeRoleKey    = "role"
	revokeObjectKey  = "object"
	revokeEndedByKey = "endedBy"
)

// grantRevokeAction returns the manage-access action that a manual revoke of
// a grant on an object of kind k writes in its audit entry. A grant on the
// installation or on a principal takes no namespace entry.
func grantRevokeAction(k access.ObjectKind) (identity.Action, bool) {
	if k == access.ObjectNamespace {
		return identity.ActionNamespaceManageAccess, true
	}

	if k == access.ObjectCollection {
		return identity.ActionCollectionManageAccess, true
	}

	if k == access.ObjectUnit {
		return identity.ActionUnitManageAccess, true
	}

	return "", false
}

// subjectNamespaceGrants decodes rows and keeps the grants in a namespace,
// ordered by namespace then ID, so the audit entries follow one order.
func subjectNamespaceGrants(rows []db.GrantsOfSubjectRow) ([]access.Grant, error) {
	grants := make([]access.Grant, 0, len(rows))

	for i := range rows {
		g, err := fromGrantRow(grantRow(rows[i]))
		if err != nil {
			return nil, fmt.Errorf("decode grant %s: %w", rows[i].ID, err)
		}

		if g.Object.Namespace() == "" {
			continue
		}

		grants = append(grants, g)
	}

	slices.SortFunc(grants, func(a, b access.Grant) int {
		if a.Object.Namespace() != b.Object.Namespace() {
			return strings.Compare(a.Object.Namespace().String(), b.Object.Namespace().String())
		}

		return strings.Compare(a.ID, b.ID)
	})

	return grants, nil
}

// checkGrantNamespace reads the namespace ns in the transaction, as a grant
// revoke checks its fence there. It returns namespace.ErrNotFound when no
// such namespace exists.
func checkGrantNamespace(ctx context.Context, q *db.Queries, ns namespace.Name) error {
	if _, err := q.GetFailoverVersion(ctx, ns.String()); errors.Is(err, sql.ErrNoRows) {
		return namespace.ErrNotFound
	} else if err != nil {
		return fmt.Errorf("read failover version of %q: %w", ns, err)
	}

	return nil
}

// auditSubjectRevokes writes one audit entry in each namespace that holds a
// grant of rows, the grants that the delete of subject ended. Each entry
// reuses the action and the record of a manual grant revoke, and names the
// delete that ended the grant. Grants on the installation take no entry. The
// write lock of the transaction orders the appends, and the fence check
// reads the namespace in the same transaction, as a grant revoke does.
func auditSubjectRevokes(
	ctx context.Context, q *db.Queries, rows []db.GrantsOfSubjectRow, subject access.Subject, e audit.Entry,
) error {
	if e.Namespace != audit.ServerScope {
		return nil
	}

	grants, err := subjectNamespaceGrants(rows)
	if err != nil {
		return err
	}

	endedBy := string(subject.Kind) + " delete " + subject.ID

	var checked namespace.Name

	for i := range grants {
		if grants[i].Object.Namespace() != checked {
			checked = grants[i].Object.Namespace()

			if err := checkGrantNamespace(ctx, q, checked); err != nil {
				return err
			}
		}

		if _, err := appendAuditEntry(ctx, q, subjectRevokeEntry(e, grants[i], endedBy)); err != nil {
			return fmt.Errorf("audit revoke of grant %s: %w", grants[i].ID, err)
		}
	}

	return nil
}

// subjectRevokeEntry returns the audit entry of the grant g that the delete
// endedBy ended. It reuses the action and the record of a manual grant
// revoke, and its changes name the grant and the delete.
func subjectRevokeEntry(e audit.Entry, g access.Grant, endedBy string) audit.Entry {
	kind, _ := access.KindOf(g.Object)
	action, _ := grantRevokeAction(kind)

	return audit.Entry{
		At:         e.At,
		Namespace:  g.Object.Namespace(),
		Subject:    e.Subject,
		IDPSubject: e.IDPSubject,
		TokenID:    e.TokenID,
		Action:     string(action),
		Object:     access.GrantRef(g),
		Version:    "",
		ValuePath:  "",
		ValueCount: nil,
		Changes: []audit.Change{
			{Key: revokeEndedByKey, Old: nil, New: endedBy, Layer: "", TakesEffect: ""},
			{Key: revokeGrantKey, Old: nil, New: g.ID, Layer: "", TakesEffect: ""},
			{Key: revokeObjectKey, Old: nil, New: g.Object.String(), Layer: "", TakesEffect: ""},
			{Key: revokeRoleKey, Old: nil, New: g.Role, Layer: "", TakesEffect: ""},
			{Key: revokeSubjectKey, Old: nil, New: g.Subject.String(), Layer: "", TakesEffect: ""},
		},
		SourceIP:      e.SourceIP,
		UserAgent:     e.UserAgent,
		Channel:       e.Channel,
		Kind:          audit.KindChange,
		Outcome:       audit.OutcomeSuccess,
		RequestID:     e.RequestID,
		ObjectID:      g.ID,
		PrevHash:      "",
		Hash:          "",
		Sequence:      0,
		FenceVersion:  0,
		Reason:        "",
		PrincipalKind: e.PrincipalKind,
		JobID:         e.JobID,
	}
}

// grantReadRow is each generated grant row. The grant reads select the same
// columns, so sqlc generates one type for each of them.
type grantReadRow interface {
	db.GetGrantRow | db.GrantsOnRow | db.GrantsWithRolesRow | db.GrantsOfSubjectRow | db.GrantsOfSubjectsRow
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

	if id, isCollection := strings.CutPrefix(row.Target, collectionTargetPrefix); isCollection {
		obj = obj.WithCollectionID(id)
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

	for i := range objects {
		o := objects[i]
		if t, err := targetOf(o); err == nil {
			out = append(out, t)
		}
	}

	return out
}

// unitTargetPrefix starts the target of a unit grant: the stable ID of the
// unit follows it.
const unitTargetPrefix = "unit:"

// collectionTargetPrefix starts the target of a collection grant: the stable ID
// of the collection follows it (ADR 52).
const collectionTargetPrefix = "collection:"

// targetOf returns what a grant on obj matches: unit:<stable ID> for a unit,
// and the wire form for every other object. A unit with no stable ID
// returns access.ErrInvalid.
func targetOf(obj identity.Object) (string, error) {
	if obj.IsCollection() {
		if obj.CollectionID() == "" {
			return "", fmt.Errorf("%w: collection %q has no stable ID", access.ErrInvalid, obj.String())
		}

		return collectionTargetPrefix + obj.CollectionID(), nil
	}

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

// inRevokingGrantTx is inGrantTx on the control path, for the revocation of a
// grant: a write on the installation (ns "") takes the zero fence and appends
// e in the scope @server, and any other write takes the fence of ns and
// appends e to the chain of ns. Neither checks the installation state, and
// both journal rev in maintenance mode.
func (s *Store) inRevokingGrantTx(
	ctx context.Context, fence namespace.Fence, e audit.Entry, rev revocation, fn func(q *db.Queries) error,
) error {
	ns := fence.Namespace

	if err := grantFence(ns, fence); err != nil {
		return err
	}

	if ns == "" {
		return s.inRevokingTx(ctx, e, rev, fn)
	}

	return s.inRevokingFencedTx(ctx, fence, e, rev, fn)
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
