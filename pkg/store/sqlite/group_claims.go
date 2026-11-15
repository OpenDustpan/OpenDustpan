package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/internal/groupclaims"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ principal.GroupMappingStore = (*Store)(nil)

// GroupMapper returns the principal.GroupMapper over the explicit group
// mappings of the store.
func (s *Store) GroupMapper() *groupclaims.Mapper {
	return groupclaims.NewMapper(claimRunner{s: s})
}

// GroupClaimApplier returns the principal.GroupClaimApplier over the
// memberships of the store. It reads the mapping with mapper. A nil mapper
// is the one of GroupMapper. It applies no rule for protected groups: the
// store keeps no data for those rules yet.
func (s *Store) GroupClaimApplier(mapper principal.GroupMapper) *groupclaims.Applier {
	if mapper == nil {
		mapper = s.GroupMapper()
	}

	return groupclaims.NewApplier(claimRunner{s: s}, mapper)
}

// PutGroupMapping implements principal.GroupMappingStore.
func (s *Store) PutGroupMapping(ctx context.Context, m principal.GroupMapping, e audit.Entry) error {
	if err := m.Validate(); err != nil {
		return err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		if err := localGroup(ctx, q, m.GroupID); err != nil {
			return err
		}

		n, err := q.PutGroupMapping(ctx, db.PutGroupMappingParams{
			Source:      string(m.Source),
			SourceID:    m.SourceID,
			ClaimValue:  m.ClaimValue,
			GroupID:     m.GroupID,
			CreatedBy:   m.CreatedBy,
			CreatedAtMs: codec.Millis(m.CreatedAt),
		})
		if err != nil {
			return fmt.Errorf("insert group mapping: %w", err)
		}

		if n == 0 {
			return principal.ErrAlreadyExists
		}

		return nil
	})
}

// DeleteGroupMapping implements principal.GroupMappingStore.
func (s *Store) DeleteGroupMapping(ctx context.Context, key principal.GroupMappingKey, e audit.Entry) error {
	if err := key.Validate(); err != nil {
		return err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		n, err := q.DeleteGroupMapping(ctx, db.DeleteGroupMappingParams{
			Source: string(key.Source), SourceID: key.SourceID, ClaimValue: key.ClaimValue, GroupID: key.GroupID,
		})
		if err != nil {
			return fmt.Errorf("delete group mapping: %w", err)
		}

		if n == 0 {
			return fmt.Errorf("group mapping: %w", principal.ErrNotFound)
		}

		return nil
	})
}

// ListGroupMappings implements principal.GroupMappingStore.
func (s *Store) ListGroupMappings(
	ctx context.Context, source principal.MembershipSource, sourceID string,
) ([]principal.GroupMapping, error) {
	if err := principal.ValidateGroupSource(source, sourceID); err != nil {
		return nil, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.queries.ListGroupMappings(
		ctx,
		db.ListGroupMappingsParams{Source: string(source), SourceID: sourceID},
	)
	if err != nil {
		return nil, fmt.Errorf("select group mappings: %w", err)
	}

	out := make([]principal.GroupMapping, 0, len(rows))
	for _, r := range rows {
		out = append(out, principal.GroupMapping{
			CreatedAt:  codec.FromMillis(r.CreatedAtMs),
			Source:     principal.MembershipSource(r.Source),
			SourceID:   r.SourceID,
			ClaimValue: r.ClaimValue,
			GroupID:    r.GroupID,
			CreatedBy:  r.CreatedBy,
		})
	}

	return out, nil
}

// claimRunner is the groupclaims.Runner and the groupclaims.MappingReads of
// the store. Its reads run outside a transaction.
type claimRunner struct {
	s *Store
}

var (
	_ groupclaims.Runner       = claimRunner{s: nil}
	_ groupclaims.MappingReads = claimRunner{s: nil}
)

// Principal implements groupclaims.Reads.
func (r claimRunner) Principal(ctx context.Context, id string) (groupclaims.Target, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()

	return claimReads{q: r.s.queries}.Principal(ctx, id)
}

// IsTempAdmin implements groupclaims.Reads.
func (r claimRunner) IsTempAdmin(ctx context.Context, id string) (bool, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()

	return claimReads{q: r.s.queries}.IsTempAdmin(ctx, id)
}

// Mappings implements groupclaims.MappingReads.
func (r claimRunner) Mappings(
	ctx context.Context, source principal.MembershipSource, sourceID string,
) (int, []groupclaims.Pair, error) {
	r.s.mu.RLock()
	defer r.s.mu.RUnlock()

	all, err := r.s.queries.ListGroupMappings(ctx, db.ListGroupMappingsParams{
		Source: string(source), SourceID: sourceID,
	})
	if err != nil {
		return 0, nil, fmt.Errorf("select group mappings: %w", err)
	}

	rows, err := r.s.queries.ListLiveGroupMappings(ctx, db.ListLiveGroupMappingsParams{
		Source: string(source), SourceID: sourceID,
	})
	if err != nil {
		return 0, nil, fmt.Errorf("select live group mappings: %w", err)
	}

	live := make([]groupclaims.Pair, 0, len(rows))
	for _, row := range rows {
		live = append(live, groupclaims.Pair{Value: row.ClaimValue, GroupID: row.GroupID})
	}

	return len(all), live, nil
}

// InTx implements groupclaims.Runner.
func (r claimRunner) InTx(ctx context.Context, e audit.Entry, fn func(groupclaims.Tx) error) error {
	if err := validServerEntry(e); err != nil {
		return err
	}

	r.s.mu.RLock()
	defer r.s.mu.RUnlock()

	return r.s.inTx(ctx, func(q *db.Queries) error {
		return fn(claimTx{claimReads{q: q}})
	})
}

// claimReads reads with one set of queries: the store, or one transaction.
type claimReads struct {
	q *db.Queries
}

// Principal implements groupclaims.Reads.
func (r claimReads) Principal(ctx context.Context, id string) (groupclaims.Target, error) {
	row, err := r.q.GetPrincipal(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return groupclaims.Target{}, principal.ErrNotFound
	}

	if err != nil {
		return groupclaims.Target{}, fmt.Errorf("select principal: %w", err)
	}

	return groupclaims.Target{
		Kind: principal.Kind(row.Kind), Namespace: row.Namespace, BreakGlass: row.BreakGlass,
		Disabled: row.DisabledAtMs.Valid,
	}, nil
}

// IsTempAdmin implements groupclaims.Reads.
func (r claimReads) IsTempAdmin(ctx context.Context, id string) (bool, error) {
	_, err := r.q.GetTempAdmin(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("select temporary administrator: %w", err)
	}

	return true, nil
}

// claimTx is the groupclaims.Tx of one transaction.
type claimTx struct {
	claimReads
}

// GroupSource implements groupclaims.Tx.
func (t claimTx) GroupSource(ctx context.Context, groupID string) (string, bool, error) {
	source, err := t.q.GroupSource(ctx, groupID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("select group source: %w", err)
	}

	return source, true, nil
}

// Memberships implements groupclaims.Tx.
func (t claimTx) Memberships(ctx context.Context, principalID string) ([]groupclaims.Membership, error) {
	rows, err := t.q.ListCurrentMembershipsOfPrincipal(ctx, principalID)
	if err != nil {
		return nil, fmt.Errorf("select memberships: %w", err)
	}

	out := make([]groupclaims.Membership, 0, len(rows))
	for _, row := range rows {
		out = append(out, groupclaims.Membership{
			GroupID: row.GroupID, Source: principal.MembershipSource(row.Source), SourceID: row.SourceID,
			State: principal.MembershipState(row.State), Pinned: row.Pinned,
		})
	}

	return out, nil
}

// AddMember implements groupclaims.Tx.
func (t claimTx) AddMember(ctx context.Context, m principal.Membership) (bool, error) {
	if err := m.Validate(); err != nil {
		return false, err
	}

	n, err := t.q.AddGroupMember(ctx, db.AddGroupMemberParams{
		GroupID:       m.GroupID,
		PrincipalID:   m.PrincipalID,
		AddedAtMs:     codec.Millis(m.AddedAt),
		Source:        string(m.Source),
		SourceID:      m.SourceID,
		State:         string(m.State),
		SuspendedAtMs: codec.OptionalMillis(m.SuspendedAt),
		Pinned:        m.Pinned,
	})
	if err != nil {
		return false, fmt.Errorf("insert group member: %w", err)
	}

	return n > 0, nil
}

// EndMember implements groupclaims.Tx.
//
// It uses the guard of RemoveMember. When the removal would leave the
// installation with no site administrator, it undoes the removal in the same
// transaction and returns access.ErrLastSiteAdministrator, so the caller can
// keep the membership and carry on.
func (t claimTx) EndMember(ctx context.Context, groupID, principalID string, at time.Time) (bool, error) {
	removedAt := nullMillis(codec.Millis(at))

	var ended bool

	err := guardSiteAdministrator(ctx, func(q *db.Queries) error {
		n, removeErr := q.RemoveGroupMember(ctx, db.RemoveGroupMemberParams{
			RemovedAtMs: removedAt, GroupID: groupID, PrincipalID: principalID,
		})
		if removeErr != nil {
			return fmt.Errorf("end group member: %w", removeErr)
		}

		ended = n > 0

		return nil
	})(t.q)

	if errors.Is(err, access.ErrLastSiteAdministrator) {
		_, undoErr := t.q.RestoreGroupMember(ctx, db.RestoreGroupMemberParams{
			GroupID: groupID, PrincipalID: principalID, RemovedAtMs: removedAt,
		})
		if undoErr != nil {
			return false, fmt.Errorf("undo the end of a group member: %w", undoErr)
		}

		return false, err
	}

	if err != nil {
		return false, err
	}

	return ended, nil
}

// Append implements groupclaims.Tx.
func (t claimTx) Append(ctx context.Context, e audit.Entry) error {
	return appendMemberAudit(ctx, t.q, e)
}
