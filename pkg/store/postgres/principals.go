package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ principal.Store = (*Store)(nil)

// CreatePrincipal implements principal.Store.
func (s *Store) CreatePrincipal(ctx context.Context, p principal.Principal) error {
	n, err := s.queries.CreatePrincipal(ctx, db.CreatePrincipalParams{
		ID:          p.ID,
		Subject:     p.Subject,
		DisplayName: p.DisplayName,
		Kind:        string(p.Kind),
		Disabled:    p.Disabled,
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

	return fromPrincipalRow(row), nil
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

	return fromPrincipalRow(row), nil
}

// ListPrincipals implements principal.Store.
func (s *Store) ListPrincipals(ctx context.Context) ([]principal.Principal, error) {
	rows, err := s.queries.ListPrincipals(ctx)
	if err != nil {
		return nil, fmt.Errorf("select principals: %w", err)
	}

	out := make([]principal.Principal, 0, len(rows))
	for _, row := range rows {
		out = append(out, fromPrincipalRow(row))
	}

	return out, nil
}

// UpdatePrincipal implements principal.Store.
func (s *Store) UpdatePrincipal(ctx context.Context, p principal.Principal) error {
	n, err := s.queries.UpdatePrincipal(ctx, db.UpdatePrincipalParams{
		DisplayName: p.DisplayName,
		Disabled:    p.Disabled,
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
}

// DeletePrincipal implements principal.Store. The foreign keys delete the
// memberships of the principal in the same statement.
func (s *Store) DeletePrincipal(ctx context.Context, id string) error {
	n, err := s.queries.DeletePrincipal(ctx, id)
	if err != nil {
		return fmt.Errorf("delete principal: %w", err)
	}

	if n == 0 {
		return principal.ErrNotFound
	}

	return nil
}

// CreateGroup implements principal.Store.
func (s *Store) CreateGroup(ctx context.Context, g principal.Group) error {
	n, err := s.queries.CreateGroup(ctx, db.CreateGroupParams{
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

	return principal.Group{
		CreatedAt:   codec.FromMillis(row.CreatedAtMs),
		UpdatedAt:   codec.FromMillis(row.UpdatedAtMs),
		ID:          row.ID,
		Name:        row.Name,
		Description: row.Description,
	}, nil
}

// ListGroups implements principal.Store.
func (s *Store) ListGroups(ctx context.Context) ([]principal.Group, error) {
	rows, err := s.queries.ListGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("select groups: %w", err)
	}

	out := make([]principal.Group, 0, len(rows))
	for _, row := range rows {
		out = append(out, principal.Group{
			CreatedAt:   codec.FromMillis(row.CreatedAtMs),
			UpdatedAt:   codec.FromMillis(row.UpdatedAtMs),
			ID:          row.ID,
			Name:        row.Name,
			Description: row.Description,
		})
	}

	return out, nil
}

// UpdateGroup implements principal.Store. It refuses a name that another
// group holds without regard to case, in the same transaction as the write.
// The unique index on name_key also refuses it.
func (s *Store) UpdateGroup(ctx context.Context, g principal.Group) error {
	key := groupNameKey(g.Name)

	return s.inTx(ctx, func(q *db.Queries) error {
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

// DeleteGroup implements principal.Store. The foreign keys delete the
// memberships of the group in the same statement.
func (s *Store) DeleteGroup(ctx context.Context, id string) error {
	n, err := s.queries.DeleteGroup(ctx, id)
	if err != nil {
		return fmt.Errorf("delete group: %w", err)
	}

	if n == 0 {
		return principal.ErrNotFound
	}

	return nil
}

// AddMember implements principal.Store. It checks the group and the
// principal in the same transaction as the insert.
func (s *Store) AddMember(ctx context.Context, m principal.Membership) error {
	return s.inTx(ctx, func(q *db.Queries) error {
		if err := groupAndPrincipalExist(ctx, q, m.GroupID, m.PrincipalID); err != nil {
			return err
		}

		n, err := q.AddGroupMember(ctx, db.AddGroupMemberParams{
			GroupID:     m.GroupID,
			PrincipalID: m.PrincipalID,
			AddedAtMs:   codec.Millis(m.AddedAt),
		})
		if isForeignKeyViolation(err) {
			return principal.ErrNotFound
		}

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

// RemoveMember implements principal.Store.
func (s *Store) RemoveMember(ctx context.Context, groupID, principalID string) error {
	n, err := s.queries.RemoveGroupMember(ctx, db.RemoveGroupMemberParams{
		GroupID:     groupID,
		PrincipalID: principalID,
	})
	if err != nil {
		return fmt.Errorf("delete group member: %w", err)
	}

	if n == 0 {
		return principal.ErrNotFound
	}

	return nil
}

// ListMembers implements principal.Store. It reads the group and its
// members in one transaction.
func (s *Store) ListMembers(ctx context.Context, groupID string) ([]principal.Member, error) {
	var out []principal.Member

	err := s.inTx(ctx, func(q *db.Queries) error {
		groups, err := q.GroupExists(ctx, groupID)
		if err != nil {
			return fmt.Errorf("count groups: %w", err)
		}

		if groups == 0 {
			return principal.ErrNotFound
		}

		rows, err := q.ListGroupMembers(ctx, groupID)
		if err != nil {
			return fmt.Errorf("select group members: %w", err)
		}

		out = make([]principal.Member, 0, len(rows))
		for _, row := range rows {
			out = append(out, principal.Member{
				AddedAt: codec.FromMillis(row.AddedAtMs),
				Principal: fromPrincipalRow(db.Principal{
					ID:          row.ID,
					Subject:     row.Subject,
					DisplayName: row.DisplayName,
					Kind:        row.Kind,
					Disabled:    row.Disabled,
					CreatedAtMs: row.CreatedAtMs,
					UpdatedAtMs: row.UpdatedAtMs,
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
func (s *Store) ListGroupsOf(ctx context.Context, principalID string) ([]principal.Group, error) {
	var out []principal.Group

	err := s.inTx(ctx, func(q *db.Queries) error {
		principals, err := q.PrincipalExists(ctx, principalID)
		if err != nil {
			return fmt.Errorf("count principals: %w", err)
		}

		if principals == 0 {
			return principal.ErrNotFound
		}

		rows, err := q.ListGroupsOfPrincipal(ctx, principalID)
		if err != nil {
			return fmt.Errorf("select groups of principal: %w", err)
		}

		out = make([]principal.Group, 0, len(rows))
		for _, row := range rows {
			out = append(out, principal.Group{
				CreatedAt:   codec.FromMillis(row.CreatedAtMs),
				UpdatedAt:   codec.FromMillis(row.UpdatedAtMs),
				ID:          row.ID,
				Name:        row.Name,
				Description: row.Description,
			})
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// fromPrincipalRow maps a principals row to a principal.
func fromPrincipalRow(row db.Principal) principal.Principal {
	return principal.Principal{
		CreatedAt:   codec.FromMillis(row.CreatedAtMs),
		UpdatedAt:   codec.FromMillis(row.UpdatedAtMs),
		ID:          row.ID,
		Subject:     row.Subject,
		DisplayName: row.DisplayName,
		Kind:        principal.Kind(row.Kind),
		Disabled:    row.Disabled,
	}
}

// groupNameKey returns the key that makes group names unique without regard
// to case. Group names hold ASCII only (principal.ValidateGroupName).
func groupNameKey(name string) string {
	return strings.ToLower(name)
}

// uniqueViolation is the SQLSTATE of a PostgreSQL unique violation.
const uniqueViolation = "23505"

// isUniqueViolation reports whether err wraps a PostgreSQL unique
// violation.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError

	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolation
}
