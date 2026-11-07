package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var _ access.ReviewStore = (*Store)(nil)

// ReviewGrants implements access.ReviewStore. It reads the page and, when
// asked, the count in one REPEATABLE READ read-only transaction, so they
// agree.
func (s *Store) ReviewGrants(ctx context.Context, rq access.ReviewQuery) ([]access.ReviewRow, int64, error) {
	p := db.ReviewGrantsParams{
		Principal:           rq.Principal,
		History:             rq.History,
		ToMs:                rq.ToMs,
		FromMs:              rq.FromMs,
		AllGrants:           rq.Scope.All,
		Namespace:           rq.Scope.Namespace,
		Target:              scopeTarget(rq.Scope.Object),
		InsideTargets:       nonNil(rq.Scope.Inside),
		Containers:          nonNil(rq.Scope.Containers),
		ContainerRoles:      nonNil(rq.Scope.ContainerRoles),
		TokenNamespace:      rq.Scope.TokenNamespace,
		TokenNamespaceRoles: nonNil(rq.Scope.TokenNamespaceRoles),
		Subject:             rq.Subject,
		AfterAtMs:           rq.After.AtMs,
		AfterID:             rq.After.ID,
		AfterMemberAtMs:     rq.After.MemberAtMs,
		AfterMemberID:       rq.After.MemberID,
		AsOfMs:              rq.AsOfMs,
		Expand:              rq.Expand,
		RowLimit:            int64(rq.Limit),
	}

	var (
		out   []access.ReviewRow
		total int64
	)

	err := s.inSnapshotTx(ctx, func(q *db.Queries) error {
		rows, err := q.ReviewGrants(ctx, p)
		if err != nil {
			return fmt.Errorf("select review rows: %w", err)
		}

		if out, err = reviewRows(rows); err != nil {
			return err
		}

		if !rq.Count {
			return nil
		}

		total, err = q.CountReviewGrants(ctx, db.CountReviewGrantsParams{
			Principal:           p.Principal,
			Expand:              p.Expand,
			History:             p.History,
			ToMs:                p.ToMs,
			FromMs:              p.FromMs,
			AsOfMs:              p.AsOfMs,
			AllGrants:           p.AllGrants,
			Namespace:           p.Namespace,
			Target:              p.Target,
			InsideTargets:       p.InsideTargets,
			Containers:          p.Containers,
			ContainerRoles:      p.ContainerRoles,
			TokenNamespace:      p.TokenNamespace,
			TokenNamespaceRoles: p.TokenNamespaceRoles,
			Subject:             p.Subject,
		})
		if err != nil {
			return fmt.Errorf("count review rows: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	return out, total, nil
}

// CallerGrants implements access.ReviewStore. It reads the page and, when
// asked, the count in one REPEATABLE READ read-only transaction, so they
// agree.
func (s *Store) CallerGrants(ctx context.Context, cq access.CallerGrantsQuery) ([]access.ReviewRow, int64, error) {
	p := db.CallerGrantsParams{
		AsOfMs: cq.AsOfMs, Principal: cq.Principal, Groups: nonNil(cq.Groups), AfterAtMs: cq.After.AtMs,
		AfterID: cq.After.ID, AfterMemberAtMs: cq.After.MemberAtMs, AfterMemberID: cq.After.MemberID,
		RowLimit: int64(cq.Limit),
	}

	var (
		out   []access.ReviewRow
		total int64
	)

	err := s.inSnapshotTx(ctx, func(q *db.Queries) error {
		rows, listErr := q.CallerGrants(ctx, p)
		if listErr != nil {
			return fmt.Errorf("select caller grants: %w", listErr)
		}

		review := make([]db.ReviewGrantsRow, 0, len(rows))
		for i := range rows {
			review = append(review, db.ReviewGrantsRow(rows[i]))
		}

		if out, listErr = reviewRows(review); listErr != nil {
			return listErr
		}

		if !cq.Count {
			return nil
		}

		total, listErr = q.CountCallerGrants(ctx, db.CountCallerGrantsParams{
			Principal: p.Principal, AsOfMs: p.AsOfMs, Groups: p.Groups,
		})
		if listErr != nil {
			return fmt.Errorf("count caller grants: %w", listErr)
		}

		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	return out, total, nil
}

// reviewRows maps the rows of one export page.
func reviewRows(rows []db.ReviewGrantsRow) ([]access.ReviewRow, error) {
	out := make([]access.ReviewRow, 0, len(rows))

	for i := range rows {
		r := &rows[i]

		g, err := fromGrantRow(db.GetGrantRow{
			ID: r.ID, Subject: r.Subject, SubjectKind: r.SubjectKind, SubjectID: r.SubjectID,
			Role: r.Role, Object: r.Object, Target: r.Target, ObjectKind: "", Namespace: "", Condition: r.Condition,
			CreatedAtMs: r.CreatedAtMs, CreatedBy: r.CreatedBy,
		})
		if err != nil {
			return nil, err
		}

		row := access.ReviewRow{
			RevokedAt:    optionalTime(r.RevokedAtMs),
			LastAccessAt: nil,
			MemberCount:  nil,
			Via:          nil,
			SubjectName:  r.SubjectName,
			RevokedBy:    r.RevokedBy,
			Grant:        g,
			// The service fills the last access with one lookup for the
			// page.
			LastAccessTracked: false,
		}

		if g.Subject.Kind == access.SubjectGroup {
			n := r.MemberCount
			row.MemberCount = &n
		}

		if r.MemberID != "" {
			// Defense in depth: the write path validates the source too; this check stays so that
			// a row written by another version or by hand cannot show as a known source.
			source, parseErr := principal.ParseMembershipSource(r.MemberSource)
			if parseErr != nil {
				return nil, fmt.Errorf("%w: group %q", access.ErrStoredSource, r.SubjectID)
			}

			row.Via = &access.Membership{
				AddedAt: codec.FromMillis(r.MemberAddedAtMs), RemovedAt: optionalTime(r.MemberRemovedAtMs),
				PrincipalID: r.MemberID, PrincipalSubject: r.MemberSubject,
				Source: string(source), SourceID: r.MemberSourceID, Suspended: r.MemberSuspended != 0,
			}
		}

		out = append(out, row)
	}

	return out, nil
}

// optionalTime maps a nullable time in Unix milliseconds.
func optionalTime(ms pgtype.Int8) *time.Time {
	if !ms.Valid {
		return nil
	}

	t := codec.FromMillis(ms.Int64)

	return &t
}

// nonNil returns items, or an empty list for nil: ANY of a NULL array
// matches nothing, but an empty array says so on its face.
func nonNil(items []string) []string {
	if items == nil {
		return []string{}
	}

	return items
}

// scopeTarget returns the target of the object of a review scope, or ""
// for no object, or for a unit with no stable ID: "" matches no grant.
func scopeTarget(obj identity.Object) string {
	if obj.String() == "" {
		return ""
	}

	target, err := targetOf(obj)
	if err != nil {
		return ""
	}

	return target
}

// ReviewUnits implements access.ReviewStore: the stacks and the workspaces
// of ids, deleted ones included.
func (s *Store) ReviewUnits(ctx context.Context, ids []string) (map[string]access.UnitState, error) {
	split := splitUnitIDs(ids)
	out := make(map[string]access.UnitState, len(ids))

	if err := s.reviewStacks(ctx, split.stacks, out); err != nil {
		return nil, err
	}

	if err := s.reviewWorkspaces(ctx, split.workspaces, out); err != nil {
		return nil, err
	}

	return out, nil
}

// reviewStacks puts the state of each stack with an ID in ids in out.
func (s *Store) reviewStacks(ctx context.Context, ids []string, out map[string]access.UnitState) error {
	if len(ids) == 0 {
		return nil
	}

	rows, err := s.queries.ReviewStacksByIDs(ctx, ids)
	if err != nil {
		return fmt.Errorf("read stacks of grants: %w", err)
	}

	for i := range rows {
		obj, objErr := unitObject(pulumiTool, rows[i].Namespace, rows[i].Project+"/"+rows[i].Name)
		if objErr != nil {
			return objErr
		}

		out[rows[i].ID] = access.UnitState{Object: obj.WithUnitID(rows[i].ID), Deleted: rows[i].DeletedAtMs.Valid}
	}

	return nil
}

// reviewWorkspaces puts the state of each workspace with an ID in ids in
// out.
func (s *Store) reviewWorkspaces(ctx context.Context, ids []string, out map[string]access.UnitState) error {
	if len(ids) == 0 {
		return nil
	}

	rows, err := s.queries.ReviewWorkspacesByIDs(ctx, ids)
	if err != nil {
		return fmt.Errorf("read workspaces of grants: %w", err)
	}

	for i := range rows {
		obj, objErr := unitObject(opentofuTool, rows[i].Namespace, rows[i].Name)
		if objErr != nil {
			return objErr
		}

		out[rows[i].ID] = access.UnitState{Object: obj.WithUnitID(rows[i].ID), Deleted: rows[i].DeletedAtMs.Valid}
	}

	return nil
}

// LastAccess implements access.ReviewStore: the latest last-use time of
// the access tokens of each given principal. A principal with no used
// token is not in the map.
func (s *Store) LastAccess(ctx context.Context, principalIDs []string) (map[string]time.Time, error) {
	out := make(map[string]time.Time, len(principalIDs))
	if len(principalIDs) == 0 {
		return out, nil
	}

	rows, err := s.queries.LastAccessByPrincipals(ctx, principalIDs)
	if err != nil {
		return nil, fmt.Errorf("read last access: %w", err)
	}

	for i := range rows {
		out[rows[i].PrincipalID] = codec.FromMillis(rows[i].LastUsedAtMs)
	}

	return out, nil
}
