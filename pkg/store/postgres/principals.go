package postgres

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ principal.Store = (*Store)(nil)

// CreatePrincipal implements principal.Store.
func (s *Store) CreatePrincipal(ctx context.Context, p principal.Principal, e audit.Entry) error {
	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		n, err := q.CreatePrincipal(ctx, db.CreatePrincipalParams{
			ID:          p.ID,
			Subject:     p.Subject,
			DisplayName: p.DisplayName,
			Kind:        string(p.Kind),
			CreatedAtMs: codec.Millis(p.CreatedAt),
			UpdatedAtMs: codec.Millis(p.UpdatedAt),
		})
		if err != nil {
			return fmt.Errorf("insert principal: %w", err)
		}

		if n == 0 {
			return principal.ErrAlreadyExists
		}

		return nil
	})
}

// GetPrincipal implements principal.Store.
func (s *Store) GetPrincipal(ctx context.Context, id string) (principal.Principal, error) {
	row, err := s.queries.GetPrincipal(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return principal.Principal{}, principal.ErrNotFound
	}

	if err != nil {
		return principal.Principal{}, fmt.Errorf("select principal: %w", err)
	}

	return principalOf(principalRow(row)), nil
}

// GetPrincipalBySubject implements principal.Store.
func (s *Store) GetPrincipalBySubject(ctx context.Context, subject string) (principal.Principal, error) {
	row, err := s.queries.GetPrincipalBySubject(ctx, subject)
	if errors.Is(err, pgx.ErrNoRows) {
		return principal.Principal{}, principal.ErrNotFound
	}

	if err != nil {
		return principal.Principal{}, fmt.Errorf("select principal by subject: %w", err)
	}

	return principalOf(principalRow(row)), nil
}

// ListPrincipals implements principal.Store.
func (s *Store) ListPrincipals(ctx context.Context, pq principal.Query) ([]principal.Principal, error) {
	rows, err := s.queries.ListPrincipals(ctx, db.ListPrincipalsParams{
		After: pq.After, Low: pq.Low, High: pq.High, RowLimit: rowLimit(pq.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("select principals: %w", err)
	}

	return mapRows(rows, func(r db.ListPrincipalsRow) principal.Principal { return principalOf(principalRow(r)) }), nil
}

// UpdatePrincipal implements principal.Store.
func (s *Store) UpdatePrincipal(ctx context.Context, p principal.Principal, e audit.Entry) error {
	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		n, err := q.UpdatePrincipal(ctx, db.UpdatePrincipalParams{
			DisplayName: p.DisplayName,
			UpdatedAtMs: codec.Millis(p.UpdatedAt),
			ID:          p.ID,
		})
		if err != nil {
			return fmt.Errorf("update principal: %w", err)
		}

		if n == 0 {
			return principal.ErrNotFound
		}

		return nil
	})
}

// DeletePrincipal implements principal.Store. It ends the principal and its
// memberships in the transaction of the entry.
func (s *Store) DeletePrincipal(ctx context.Context, id string, e audit.Entry) error {
	return s.endRecord(ctx, e,
		func(q *db.Queries, at int64) (int64, error) {
			return q.DeletePrincipal(ctx, db.DeletePrincipalParams{DeletedAtMs: nullMillis(at), ID: id})
		},
		func(q *db.Queries, at int64) error {
			return q.EndMembershipsOfPrincipal(ctx, db.EndMembershipsOfPrincipalParams{
				RemovedAtMs: nullMillis(at), PrincipalID: id,
			})
		})
}

// endRecord ends one record with end and then its memberships with
// endMembers, at the time of e and in the transaction of e. It returns
// ErrNotFound when end ends no row.
func (s *Store) endRecord(
	ctx context.Context, e audit.Entry,
	end func(q *db.Queries, at int64) (int64, error), endMembers func(q *db.Queries, at int64) error,
) error {
	at := codec.Millis(e.At)

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		n, err := end(q, at)
		if err != nil {
			return fmt.Errorf("end record: %w", err)
		}

		if n == 0 {
			return principal.ErrNotFound
		}

		if err := endMembers(q, at); err != nil {
			return fmt.Errorf("end memberships: %w", err)
		}

		return nil
	})
}

// CreateGroup implements principal.Store.
func (s *Store) CreateGroup(ctx context.Context, g principal.Group, e audit.Entry) error {
	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		n, err := q.CreateGroup(ctx, db.CreateGroupParams{
			ID:          g.ID,
			Name:        g.Name,
			NameKey:     groupNameKey(g.Name),
			Description: g.Description,
			CreatedAtMs: codec.Millis(g.CreatedAt),
			UpdatedAtMs: codec.Millis(g.UpdatedAt),
		})
		if err != nil {
			return fmt.Errorf("insert group: %w", err)
		}

		if n == 0 {
			return principal.ErrAlreadyExists
		}

		return nil
	})
}

// GetGroup implements principal.Store.
func (s *Store) GetGroup(ctx context.Context, id string) (principal.Group, error) {
	row, err := s.queries.GetGroup(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return principal.Group{}, principal.ErrNotFound
	}

	if err != nil {
		return principal.Group{}, fmt.Errorf("select group: %w", err)
	}

	return groupOf(groupRow(row)), nil
}

// ListGroups implements principal.Store.
func (s *Store) ListGroups(ctx context.Context, pq principal.Query) ([]principal.Group, error) {
	rows, err := s.queries.ListGroups(ctx, db.ListGroupsParams{
		After: pq.After, Low: pq.Low, High: pq.High, RowLimit: rowLimit(pq.Limit),
	})
	if err != nil {
		return nil, fmt.Errorf("select groups: %w", err)
	}

	return mapRows(rows, func(r db.ListGroupsRow) principal.Group { return groupOf(groupRow(r)) }), nil
}

// UpdateGroup implements principal.Store. It refuses a name that another
// group holds without regard to case, in the same transaction as the write.
func (s *Store) UpdateGroup(ctx context.Context, g principal.Group, e audit.Entry) error {
	key := groupNameKey(g.Name)

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		holder, err := q.GroupIDByNameKey(ctx, key)

		switch {
		case errors.Is(err, pgx.ErrNoRows):
		case err != nil:
			return fmt.Errorf("select group by name: %w", err)
		case holder != g.ID:
			return principal.ErrAlreadyExists
		default:
		}

		n, err := q.UpdateGroup(ctx, db.UpdateGroupParams{
			Name:        g.Name,
			NameKey:     key,
			Description: g.Description,
			UpdatedAtMs: codec.Millis(g.UpdatedAt),
			ID:          g.ID,
		})
		if isUniqueViolation(err) {
			return principal.ErrAlreadyExists
		}

		if err != nil {
			return fmt.Errorf("update group: %w", err)
		}

		if n == 0 {
			return principal.ErrNotFound
		}

		return nil
	})
}

// DeleteGroup implements principal.Store. It ends the group and its
// memberships in the transaction of the entry.
func (s *Store) DeleteGroup(ctx context.Context, id string, e audit.Entry) error {
	return s.endRecord(ctx, e,
		func(q *db.Queries, at int64) (int64, error) {
			return q.DeleteGroup(ctx, db.DeleteGroupParams{DeletedAtMs: nullMillis(at), ID: id})
		},
		func(q *db.Queries, at int64) error {
			return q.EndMembershipsInGroup(
				ctx,
				db.EndMembershipsInGroupParams{RemovedAtMs: nullMillis(at), GroupID: id},
			)
		})
}

// AddMember implements principal.Store. It checks the group and the
// principal in the same transaction as the insert.
func (s *Store) AddMember(ctx context.Context, m principal.Membership, e audit.Entry) error {
	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		if err := groupAndPrincipalExist(ctx, q, m.GroupID, m.PrincipalID); err != nil {
			return err
		}

		n, err := q.AddGroupMember(ctx, db.AddGroupMemberParams{
			GroupID:     m.GroupID,
			PrincipalID: m.PrincipalID,
			AddedAtMs:   codec.Millis(m.AddedAt),
		})
		if err != nil {
			return fmt.Errorf("insert group member: %w", err)
		}

		if n == 0 {
			return principal.ErrAlreadyExists
		}

		return nil
	})
}

// groupAndPrincipalExist returns ErrNotFound when the group or the principal
// does not exist.
func groupAndPrincipalExist(ctx context.Context, q *db.Queries, groupID, principalID string) error {
	groups, err := q.GroupExists(ctx, groupID)
	if err != nil {
		return fmt.Errorf("count groups: %w", err)
	}

	if groups == 0 {
		return fmt.Errorf("group: %w", principal.ErrNotFound)
	}

	principals, err := q.PrincipalExists(ctx, principalID)
	if err != nil {
		return fmt.Errorf("count principals: %w", err)
	}

	if principals == 0 {
		return fmt.Errorf("principal: %w", principal.ErrNotFound)
	}

	return nil
}

// RemoveMember implements principal.Store. It ends the membership in the
// transaction of the entry.
func (s *Store) RemoveMember(ctx context.Context, groupID, principalID string, e audit.Entry) error {
	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		n, err := q.RemoveGroupMember(ctx, db.RemoveGroupMemberParams{
			RemovedAtMs: nullMillis(codec.Millis(e.At)),
			GroupID:     groupID,
			PrincipalID: principalID,
		})
		if err != nil {
			return fmt.Errorf("end group member: %w", err)
		}

		if n == 0 {
			return principal.ErrNotFound
		}

		return nil
	})
}

// ListMembers implements principal.Store. It reads the group and its
// members in one transaction.
func (s *Store) ListMembers(ctx context.Context, groupID string, pq principal.Query) ([]principal.Member, error) {
	var out []principal.Member

	err := s.inTx(ctx, func(q *db.Queries) error {
		groups, err := q.GroupExists(ctx, groupID)
		if err != nil {
			return fmt.Errorf("count groups: %w", err)
		}

		if groups == 0 {
			return principal.ErrNotFound
		}

		rows, err := q.ListGroupMembers(ctx, db.ListGroupMembersParams{
			GroupID: groupID, After: pq.After, Low: pq.Low, High: pq.High, RowLimit: rowLimit(pq.Limit),
		})
		if err != nil {
			return fmt.Errorf("select group members: %w", err)
		}

		out = make([]principal.Member, 0, len(rows))
		for i := range rows {
			row := &rows[i]
			out = append(out, principal.Member{
				AddedAt: codec.FromMillis(row.AddedAtMs),
				Principal: principalOf(principalFields{
					id: row.ID, subject: row.Subject, displayName: row.DisplayName, kind: row.Kind,
					createdAtMs: row.CreatedAtMs, updatedAtMs: row.UpdatedAtMs,
				}),
			})
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// ListGroupsOf implements principal.Store. It reads the principal and its
// groups in one transaction.
func (s *Store) ListGroupsOf(ctx context.Context, principalID string, pq principal.Query) ([]principal.Group, error) {
	var out []principal.Group

	err := s.inTx(ctx, func(q *db.Queries) error {
		principals, err := q.PrincipalExists(ctx, principalID)
		if err != nil {
			return fmt.Errorf("count principals: %w", err)
		}

		if principals == 0 {
			return principal.ErrNotFound
		}

		rows, err := q.ListGroupsOfPrincipal(ctx, db.ListGroupsOfPrincipalParams{
			PrincipalID: principalID, After: pq.After, Low: pq.Low, High: pq.High, RowLimit: rowLimit(pq.Limit),
		})
		if err != nil {
			return fmt.Errorf("select groups of principal: %w", err)
		}

		out = mapRows(rows, func(r db.ListGroupsOfPrincipalRow) principal.Group { return groupOf(groupRow(r)) })

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// principalFields are the columns of a principal that every read selects.
type principalFields struct {
	id, subject, displayName, kind string
	createdAtMs, updatedAtMs       int64
}

// principalRow maps any generated principal row to its fields. The reads
// select the same columns, so sqlc generates one type for each of them.
func principalRow[T db.GetPrincipalRow | db.GetPrincipalBySubjectRow | db.ListPrincipalsRow](row T) principalFields {
	r := db.GetPrincipalRow(row)

	return principalFields{
		id: r.ID, subject: r.Subject, displayName: r.DisplayName, kind: r.Kind,
		createdAtMs: r.CreatedAtMs, updatedAtMs: r.UpdatedAtMs,
	}
}

// principalOf maps the columns of a principal to a principal.
func principalOf(f principalFields) principal.Principal {
	return principal.Principal{
		CreatedAt:   codec.FromMillis(f.createdAtMs),
		UpdatedAt:   codec.FromMillis(f.updatedAtMs),
		ID:          f.id,
		Subject:     f.subject,
		DisplayName: f.displayName,
		Kind:        principal.Kind(f.kind),
	}
}

// groupRow maps any generated group row to the row of GetGroup.
func groupRow[T db.GetGroupRow | db.ListGroupsRow | db.ListGroupsOfPrincipalRow](row T) db.GetGroupRow {
	return db.GetGroupRow(row)
}

// groupOf maps the columns of a group to a group.
func groupOf(row db.GetGroupRow) principal.Group {
	return principal.Group{
		CreatedAt:   codec.FromMillis(row.CreatedAtMs),
		UpdatedAt:   codec.FromMillis(row.UpdatedAtMs),
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description,
	}
}

// mapRows maps each generated row to a domain value.
func mapRows[R, T any](rows []R, f func(R) T) []T {
	out := make([]T, 0, len(rows))
	for i := range rows {
		out = append(out, f(rows[i]))
	}

	return out
}

// nullMillis maps a time in Unix milliseconds to a nullable column.
func nullMillis(ms int64) pgtype.Int8 {
	return pgtype.Int8{Int64: ms, Valid: true}
}

// rowLimit maps the row limit of a page query to the LIMIT parameter. A
// page holds at most principal.MaxLimit+1 rows, so the cap never applies.
func rowLimit(limit int) int32 {
	if limit > math.MaxInt32 {
		return math.MaxInt32
	}

	return int32(limit) //nolint:gosec // The branch above bounds the value.
}

// groupNameKey returns the key that makes group names unique without regard
// to case. Group names hold ASCII only (principal.ValidateGroupName).
func groupNameKey(name string) string {
	return strings.ToLower(name)
}

// uniqueViolation is the SQLSTATE of a PostgreSQL unique violation.
const uniqueViolation = "23505"

// isUniqueViolation reports whether err is a unique violation of the
// driver.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError

	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}
