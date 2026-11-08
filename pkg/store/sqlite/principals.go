package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"opendustpan.org/opendustpan/pkg/access"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/maintenance"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/principal"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ principal.Store = (*Store)(nil)

// CreatePrincipal implements principal.Store.
func (s *Store) CreatePrincipal(ctx context.Context, p principal.Principal, e audit.Entry) error {
	if p.Namespace != "" {
		return fmt.Errorf("%w: a principal of a namespace goes through CreateNamespacePrincipal", principal.ErrInvalid)
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		return insertPrincipal(ctx, q, p)
	})
}

// insertPrincipal inserts p with q, and returns ErrAlreadyExists when its
// ID or its subject is taken. It returns ErrBreakGlassKind when a principal
// that is not a person is marked as a break-glass account.
func insertPrincipal(ctx context.Context, q *db.Queries, p principal.Principal) error {
	if p.BreakGlass && p.Kind != principal.KindPerson {
		return fmt.Errorf("%w: kind %q", principal.ErrBreakGlassKind, p.Kind)
	}

	n, err := q.CreatePrincipal(ctx, db.CreatePrincipalParams{
		ID:          p.ID,
		Subject:     p.Subject,
		DisplayName: p.DisplayName,
		Kind:        string(p.Kind),
		Source:      string(p.Source),
		CreatedAtMs: codec.Millis(p.CreatedAt),
		UpdatedAtMs: codec.Millis(p.UpdatedAt),
		Namespace:   p.Namespace.String(),
		BreakGlass:  p.BreakGlass,
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
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetPrincipal(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return principal.Principal{}, principal.ErrNotFound
	}

	if err != nil {
		return principal.Principal{}, fmt.Errorf("select principal: %w", err)
	}

	return principalOf(principalRow(row)), nil
}

// GetPrincipalBySubject implements principal.Store.
func (s *Store) GetPrincipalBySubject(ctx context.Context, subject string) (principal.Principal, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetPrincipalBySubject(ctx, subject)
	if errors.Is(err, sql.ErrNoRows) {
		return principal.Principal{}, principal.ErrNotFound
	}

	if err != nil {
		return principal.Principal{}, fmt.Errorf("select principal by subject: %w", err)
	}

	return principalOf(principalRow(row)), nil
}

// ListPrincipals implements principal.Store. It reads the page and the
// total in one transaction.
func (s *Store) ListPrincipals(
	ctx context.Context, pq principal.Query, kind principal.Kind,
) ([]principal.Principal, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return listPage(ctx, s, pq, pageReads[db.ListPrincipalsRow, principal.Principal]{
		list: func(ctx context.Context, q *db.Queries, pq principal.Query) ([]db.ListPrincipalsRow, error) {
			return listPrincipalRows(ctx, q, pq, kind, "")
		},
		count: func(ctx context.Context, q *db.Queries, pq principal.Query) (int64, error) {
			return countPrincipalRows(ctx, q, pq, kind, "")
		},
		conv: principalFromList,
	})
}

// listPrincipalRows reads one page of principals. An empty ns reads the
// principals of every namespace and of the installation.
func listPrincipalRows(
	ctx context.Context, q *db.Queries, pq principal.Query, kind principal.Kind, ns namespace.Name,
) ([]db.ListPrincipalsRow, error) {
	return q.ListPrincipals(
		ctx,
		db.ListPrincipalsParams{
			After:     pq.After,
			Low:       pq.Low,
			High:      pq.High,
			Kind:      string(kind),
			Namespace: ns.String(),
			RowLimit:  int64(pq.Limit),
		},
	)
}

func countPrincipalRows(
	ctx context.Context, q *db.Queries, pq principal.Query, kind principal.Kind, ns namespace.Name,
) (int64, error) {
	return q.CountPrincipals(ctx, db.CountPrincipalsParams{
		Low: pq.Low, High: pq.High, Kind: string(kind), Namespace: ns.String(),
	})
}

func principalFromList(r db.ListPrincipalsRow) principal.Principal {
	return principalOf(principalRow(r))
}

// pageReads are the reads of one paged list: its rows, its total, and the
// map from a row to a domain value.
type pageReads[R, T any] struct {
	list  func(context.Context, *db.Queries, principal.Query) ([]R, error)
	count func(context.Context, *db.Queries, principal.Query) (int64, error)
	conv  func(R) T
}

// listPage reads one page and its total with r in one transaction.
func listPage[R, T any](ctx context.Context, s *Store, pq principal.Query, r pageReads[R, T]) ([]T, int64, error) {
	var (
		out   []T
		total int64
	)

	err := s.inReadTx(ctx, func(q *db.Queries) error {
		rows, err := r.list(ctx, q, pq)
		if err != nil {
			return fmt.Errorf("select page: %w", err)
		}

		out = mapRows(rows, r.conv)

		total, err = r.count(ctx, q, pq)
		if err != nil {
			return fmt.Errorf("count rows: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	return out, total, nil
}

// UpdatePrincipal implements principal.Store.
func (s *Store) UpdatePrincipal(ctx context.Context, p principal.Principal, e audit.Entry) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		return updatePrincipalRow(ctx, q, p, "")
	})
}

// updatePrincipalRow writes the display name and the update time of p, a
// principal of the namespace ns ("" for the installation), with q. It
// returns ErrNotFound when no such principal exists in ns.
func updatePrincipalRow(ctx context.Context, q *db.Queries, p principal.Principal, ns namespace.Name) error {
	n, err := q.UpdatePrincipal(ctx, db.UpdatePrincipalParams{
		DisplayName: p.DisplayName,
		UpdatedAtMs: codec.Millis(p.UpdatedAt),
		ID:          p.ID,
		Namespace:   ns.String(),
	})
	if err != nil {
		return fmt.Errorf("update principal: %w", err)
	}

	if n == 0 {
		return principal.ErrNotFound
	}

	return nil
}

// DeletePrincipal implements principal.Store. It ends the principal, its
// memberships and its grants, and removes its temporary administrator
// record, in the transaction of the entry.
func (s *Store) DeletePrincipal(ctx context.Context, id string, e audit.Entry) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.endRecord(ctx, e, access.Subject{Kind: access.SubjectPrincipal, ID: id}, ending{
		record:  func(q *db.Queries, at int64) (int64, error) { return endInstallationPrincipal(ctx, q, id, at) },
		members: func(q *db.Queries, at int64) error { return endPrincipalMemberships(ctx, q, id, at) },
	})
}

// endInstallationPrincipal ends the principal id of the installation at the
// time at, and removes its temporary administrator record, so a new person
// with the same subject is not the old temporary administrator.
func endInstallationPrincipal(ctx context.Context, q *db.Queries, id string, at int64) (int64, error) {
	n, err := endPrincipal(ctx, q, id, "", at)
	if err != nil {
		return n, err
	}

	if n == 0 {
		// The write changed no row. When the principal is an enabled
		// break-glass account of the installation, the conditional update
		// kept it as the last one.
		enabledBreakGlass, countErr := q.CountEnabledBreakGlassPrincipal(ctx, id)
		if countErr != nil {
			return 0, fmt.Errorf("select break-glass principal: %w", countErr)
		}

		if enabledBreakGlass > 0 {
			return 0, principal.ErrNoWorkingBreakGlass
		}

		return 0, nil
	}

	if _, err := q.DeleteTempAdmin(ctx, id); err != nil {
		return 0, fmt.Errorf("delete temporary administrator record: %w", err)
	}

	return n, nil
}

// EnablePrincipal implements principal.Store.
func (s *Store) EnablePrincipal(ctx context.Context, id string, e audit.Entry) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		n, err := q.EnablePrincipal(ctx, db.EnablePrincipalParams{UpdatedAtMs: codec.Millis(e.At), ID: id})
		if err != nil {
			return fmt.Errorf("enable principal: %w", err)
		}

		if n == 1 {
			return nil
		}

		live, err := q.CountLivePrincipal(ctx, id)
		if err != nil {
			return fmt.Errorf("select principal: %w", err)
		}

		if live == 0 {
			return principal.ErrNotFound
		}

		return principal.ErrNotDisabled
	})
}

// DisablePrincipal implements principal.Store. It runs on the control path, so
// it works in maintenance mode, and the transaction journals it there.
func (s *Store) DisablePrincipal(ctx context.Context, id string, e audit.Entry) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rev := revocation{kind: maintenance.RevokedPrincipal, target: id, endReason: ""}

	return s.inRevokingTx(ctx, e, rev, guardSiteAdministrator(ctx, func(q *db.Queries) error {
		n, err := q.DisablePrincipal(ctx, db.DisablePrincipalParams{DisabledAtMs: codec.Millis(e.At), ID: id})
		if err != nil {
			return fmt.Errorf("disable principal: %w", err)
		}

		if n == 1 {
			return nil
		}

		live, err := q.CountLivePrincipal(ctx, id)
		if err != nil {
			return fmt.Errorf("select principal: %w", err)
		}

		if live == 0 {
			return principal.ErrNotFound
		}

		// The write changed no row of a principal that exists. It is either
		// disabled already, or the conditional update kept the last enabled
		// break-glass account.
		enabledBreakGlass, err := q.CountEnabledBreakGlassPrincipal(ctx, id)
		if err != nil {
			return fmt.Errorf("select break-glass principal: %w", err)
		}

		if enabledBreakGlass > 0 {
			return principal.ErrNoWorkingBreakGlass
		}

		return principal.ErrDisabled
	}))
}

// ending ends one record and then its memberships, at the time at.
type ending struct {
	record  func(q *db.Queries, at int64) (int64, error)
	members func(q *db.Queries, at int64) error
}

// endPrincipal ends the principal id of the namespace ns ("" for the
// installation) at the time at.
func endPrincipal(ctx context.Context, q *db.Queries, id string, ns namespace.Name, at int64) (int64, error) {
	n, err := q.DeletePrincipal(
		ctx,
		db.DeletePrincipalParams{DeletedAtMs: nullMillis(at), ID: id, Namespace: ns.String()},
	)
	if err != nil || n == 0 {
		return n, err
	}

	// The preferences of a person go away with the person, in the same
	// transaction.
	if err := q.DeletePersonPreferences(ctx, id); err != nil {
		return 0, fmt.Errorf("delete preferences of the principal: %w", err)
	}

	// The Git host account links and the open link requests of a principal go
	// away with it too, so that an account never runs a command as an ended
	// principal.
	if err := q.DeleteForgeAccountLinksOfPrincipal(ctx, id); err != nil {
		return 0, fmt.Errorf("delete Git host account links of the principal: %w", err)
	}

	if err := q.DeleteForgeLinkRequestsOfPrincipal(ctx, id); err != nil {
		return 0, fmt.Errorf("delete link requests of the principal: %w", err)
	}

	return n, nil
}

func endPrincipalMemberships(ctx context.Context, q *db.Queries, id string, at int64) error {
	return q.EndMembershipsOfPrincipal(ctx, db.EndMembershipsOfPrincipalParams{
		RemovedAtMs: nullMillis(at), PrincipalID: id,
	})
}

func endGroup(ctx context.Context, q *db.Queries, id string, at int64) (int64, error) {
	return q.DeleteGroup(ctx, db.DeleteGroupParams{DeletedAtMs: nullMillis(at), ID: id})
}

func endGroupMemberships(ctx context.Context, q *db.Queries, id string, at int64) error {
	return q.EndMembershipsInGroup(ctx, db.EndMembershipsInGroupParams{RemovedAtMs: nullMillis(at), GroupID: id})
}

// endRecord ends one record with end.record, then its memberships with
// end.members and the grants of subject, at the time of e and in the
// transaction of e. It returns ErrNotFound when end.record ends no row.
func (s *Store) endRecord(ctx context.Context, e audit.Entry, subject access.Subject, end ending) error {
	return s.inAuditedTx(ctx, e, endRecordTx(ctx, e, subject, end))
}

// endRecordTx returns the body of the transaction of endRecord.
func endRecordTx(ctx context.Context, e audit.Entry, subject access.Subject, end ending) func(q *db.Queries) error {
	at := codec.Millis(e.At)

	return guardSiteAdministrator(ctx, func(q *db.Queries) error {
		n, err := end.record(q, at)
		if err != nil {
			return fmt.Errorf("end record: %w", err)
		}

		if n == 0 {
			return principal.ErrNotFound
		}

		if err := end.members(q, at); err != nil {
			return fmt.Errorf("end memberships: %w", err)
		}

		return revokeGrantsOf(ctx, q, subject, e)
	})
}

// CreateGroup implements principal.Store.
func (s *Store) CreateGroup(ctx context.Context, g principal.Group, e audit.Entry) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		n, err := q.CreateGroup(ctx, db.CreateGroupParams{
			ID:                       g.ID,
			Name:                     g.Name,
			NameKey:                  groupNameKey(g.Name),
			Description:              g.Description,
			Source:                   string(g.Source),
			CreatedAtMs:              codec.Millis(g.CreatedAt),
			UpdatedAtMs:              codec.Millis(g.UpdatedAt),
			ClosedToIdentityProvider: g.ClosedToIdentityProvider,
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
	s.mu.RLock()
	defer s.mu.RUnlock()

	row, err := s.queries.GetGroup(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return principal.Group{}, principal.ErrNotFound
	}

	if err != nil {
		return principal.Group{}, fmt.Errorf("select group: %w", err)
	}

	return groupOf(groupRow(row)), nil
}

// ListGroups implements principal.Store. It reads the page and the total
// in one transaction.
func (s *Store) ListGroups(ctx context.Context, pq principal.Query) ([]principal.Group, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return listPage(ctx, s, pq, pageReads[db.ListGroupsRow, principal.Group]{
		list: listGroupRows, count: countGroupRows, conv: groupFromList,
	})
}

func listGroupRows(ctx context.Context, q *db.Queries, pq principal.Query) ([]db.ListGroupsRow, error) {
	return q.ListGroups(
		ctx,
		db.ListGroupsParams{After: pq.After, Low: pq.Low, High: pq.High, RowLimit: int64(pq.Limit)},
	)
}

func countGroupRows(ctx context.Context, q *db.Queries, pq principal.Query) (int64, error) {
	return q.CountGroups(ctx, db.CountGroupsParams{Low: pq.Low, High: pq.High})
}

func groupFromList(r db.ListGroupsRow) principal.Group { return groupOf(groupRow(r)) }

// UpdateGroup implements principal.Store. It refuses a name that another
// group holds without regard to case, in the same transaction as the write.
func (s *Store) UpdateGroup(ctx context.Context, g principal.Group, e audit.Entry) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := groupNameKey(g.Name)

	return s.inAuditedTx(ctx, e, func(q *db.Queries) error {
		holder, err := q.GroupIDByNameKey(ctx, key)

		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return fmt.Errorf("select group by name: %w", err)
		case holder != g.ID:
			return principal.ErrAlreadyExists
		default:
		}

		n, err := q.UpdateGroup(ctx, db.UpdateGroupParams{
			Name:                     g.Name,
			NameKey:                  key,
			Description:              g.Description,
			ClosedToIdentityProvider: g.ClosedToIdentityProvider,
			UpdatedAtMs:              codec.Millis(g.UpdatedAt),
			ID:                       g.ID,
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

// DeleteGroup implements principal.Store. It ends the group, its
// memberships and its grants in the transaction of the entry.
func (s *Store) DeleteGroup(ctx context.Context, id string, e audit.Entry) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.endRecord(ctx, e, access.Subject{Kind: access.SubjectGroup, ID: id}, ending{
		record:  func(q *db.Queries, at int64) (int64, error) { return endGroup(ctx, q, id, at) },
		members: func(q *db.Queries, at int64) error { return endGroupMemberships(ctx, q, id, at) },
	})
}

// AddMember implements principal.Store. It checks the group and the
// principal, and runs more, in the same transaction as the insert. An empty source or state
// defaults to local and active.
func (s *Store) AddMember(
	ctx context.Context, m principal.Membership, e audit.Entry, more principal.MemberAudit,
) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if err := validServerEntry(e); err != nil {
		return err
	}

	return s.inTx(ctx, func(q *db.Queries) error {
		if err := groupAndPrincipalExist(ctx, q, m.GroupID, m.PrincipalID); err != nil {
			return err
		}

		entry, merr := memberChanges(ctx, q, e, more)
		if merr != nil {
			return merr
		}

		m = m.WithDefaults()

		if verr := m.Validate(); verr != nil {
			return verr
		}

		n, ierr := q.AddGroupMember(ctx, db.AddGroupMemberParams{
			GroupID:       m.GroupID,
			PrincipalID:   m.PrincipalID,
			AddedAtMs:     codec.Millis(m.AddedAt),
			Source:        string(m.Source),
			SourceID:      m.SourceID,
			State:         string(m.State),
			SuspendedAtMs: codec.OptionalMillis(m.SuspendedAt),
			Pinned:        m.Pinned,
		})
		if ierr != nil {
			return fmt.Errorf("insert group member: %w", ierr)
		}

		if n == 0 {
			return principal.ErrAlreadyExists
		}

		return appendMemberAudit(ctx, q, entry)
	})
}

// memberChanges returns e with the changes of more appended to a copy, so
// the caller entry keeps its changes. It runs more only when more is not
// nil.
func memberChanges(
	ctx context.Context, q *db.Queries, e audit.Entry, more principal.MemberAudit,
) (audit.Entry, error) {
	changes := append([]audit.Change(nil), e.Changes...)
	if more != nil {
		extra, err := more(ctx, grantReads{q: q})
		if err != nil {
			return audit.Entry{}, err
		}

		changes = append(changes, extra...)
	}

	entry := e
	entry.Changes = changes

	return entry, nil
}

// appendMemberAudit validates the entry of the insert and appends it.
func appendMemberAudit(ctx context.Context, q *db.Queries, entry audit.Entry) error {
	if err := validServerEntry(entry); err != nil {
		return err
	}

	if _, err := appendAuditEntry(ctx, q, entry); err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}

	return nil
}

// groupAndPrincipalExist returns ErrNotFound when the group or the principal
// does not exist, and ErrManagedByDirectory when a directory keeps the
// group.
func groupAndPrincipalExist(ctx context.Context, q *db.Queries, groupID, principalID string) error {
	if err := localGroup(ctx, q, groupID); err != nil {
		return err
	}

	ns, err := q.GetPrincipalNamespace(ctx, principalID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("principal: %w", principal.ErrNotFound)
	}

	if err != nil {
		return fmt.Errorf("select principal namespace: %w", err)
	}

	if ns != "" {
		return fmt.Errorf("%w: a principal of a namespace cannot be a member of a group", principal.ErrInvalid)
	}

	return nil
}

// localGroup returns ErrNotFound when the group groupID does not exist, and
// ErrManagedByDirectory, with the name of the directory, when its source is
// not local.
func localGroup(ctx context.Context, q *db.Queries, groupID string) error {
	source, err := q.GroupSource(ctx, groupID)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("group: %w", principal.ErrNotFound)
	}

	if err != nil {
		return fmt.Errorf("select group source: %w", err)
	}

	if principal.Source(source) != principal.SourceLocal {
		return fmt.Errorf("%w: the %s directory keeps the members of group %s. Change them in %s",
			principal.ErrManagedByDirectory, source, groupID, source)
	}

	return nil
}

// RemoveMember implements principal.Store. It ends the membership in the
// transaction of the entry.
func (s *Store) RemoveMember(ctx context.Context, groupID, principalID string, e audit.Entry) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.inAuditedTx(ctx, e, guardSiteAdministrator(ctx, func(q *db.Queries) error {
		if err := localGroup(ctx, q, groupID); err != nil {
			return err
		}

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
	}))
}

// ListMembers implements principal.Store. It reads the group, the page and
// the total in one transaction.
func (s *Store) ListMembers(
	ctx context.Context,
	groupID string,
	pq principal.Query,
) ([]principal.Member, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var (
		out   []principal.Member
		total int64
	)

	err := s.inReadTx(ctx, func(q *db.Queries) error {
		_, err := q.GroupSource(ctx, groupID)
		if errors.Is(err, sql.ErrNoRows) {
			return principal.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("select group: %w", err)
		}

		rows, err := q.ListGroupMembers(ctx, db.ListGroupMembersParams{
			GroupID: groupID, After: pq.After, Low: pq.Low, High: pq.High, RowLimit: int64(pq.Limit),
		})
		if err != nil {
			return fmt.Errorf("select group members: %w", err)
		}

		out = mapRows(rows, func(r db.ListGroupMembersRow) principal.Member {
			return principal.Member{
				AddedAt:     codec.FromMillis(r.AddedAtMs),
				SuspendedAt: codec.FromOptionalMillis(r.MemberSuspendedAtMs),
				GroupID:     groupID,
				PrincipalID: r.ID,
				Source:      principal.MembershipSource(r.MemberSource),
				SourceID:    r.MemberSourceID,
				State:       principal.MembershipState(r.MemberState),
				Pinned:      r.MemberPinned,
				Principal: principalOf(principalFields{
					id: r.ID, subject: r.Subject, displayName: r.DisplayName, kind: r.Kind, source: r.Source,
					createdAtMs: r.CreatedAtMs, updatedAtMs: r.UpdatedAtMs, groupCount: r.GroupCount,
					disabledAtMs: r.DisabledAtMs.Int64, namespace: r.Namespace, breakGlass: r.BreakGlass,
				}),
			}
		})

		total, err = q.CountGroupMembers(ctx, db.CountGroupMembersParams{GroupID: groupID, Low: pq.Low, High: pq.High})
		if err != nil {
			return fmt.Errorf("count group members: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	return out, total, nil
}

// ListGroupsOf implements principal.Store. It reads the principal, the
// page and the total in one transaction.
func (s *Store) ListGroupsOf(
	ctx context.Context,
	principalID string,
	pq principal.Query,
) ([]principal.Group, int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var (
		out   []principal.Group
		total int64
	)

	err := s.inReadTx(ctx, func(q *db.Queries) error {
		principals, err := q.PrincipalExists(ctx, principalID)
		if err != nil {
			return fmt.Errorf("count principals: %w", err)
		}

		if principals == 0 {
			return principal.ErrNotFound
		}

		rows, err := q.ListGroupsOfPrincipal(ctx, db.ListGroupsOfPrincipalParams{
			PrincipalID: principalID, After: pq.After, Low: pq.Low, High: pq.High, RowLimit: int64(pq.Limit),
		})
		if err != nil {
			return fmt.Errorf("select groups of principal: %w", err)
		}

		out = mapRows(rows, func(r db.ListGroupsOfPrincipalRow) principal.Group { return groupOf(groupRow(r)) })

		total, err = q.CountGroupsOfPrincipal(ctx, db.CountGroupsOfPrincipalParams{
			PrincipalID: principalID, Low: pq.Low, High: pq.High,
		})
		if err != nil {
			return fmt.Errorf("count groups of principal: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, 0, err
	}

	return out, total, nil
}

// ListActiveGroupNamesOf implements principal.Store. It reads the principal
// and the names in one transaction.
func (s *Store) ListActiveGroupNamesOf(ctx context.Context, principalID string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var out []string

	err := s.inReadTx(ctx, func(q *db.Queries) error {
		principals, err := q.PrincipalExists(ctx, principalID)
		if err != nil {
			return fmt.Errorf("count principals: %w", err)
		}

		if principals == 0 {
			return principal.ErrNotFound
		}

		out, err = q.ListActiveGroupNamesOfPrincipal(ctx, principalID)
		if err != nil {
			return fmt.Errorf("select active group names of principal: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// principalFields are the columns of a principal that every read selects.
type principalFields struct {
	id, subject, displayName, kind, source string
	createdAtMs, updatedAtMs, groupCount   int64
	// disabledAtMs is 0 when the column is NULL.
	disabledAtMs int64
	namespace    string
	breakGlass   bool
}

// principalRow maps any generated principal row to its fields. The reads
// select the same columns, so sqlc generates one type for each of them.
func principalRow[T db.GetPrincipalRow | db.GetPrincipalBySubjectRow | db.ListPrincipalsRow](row T) principalFields {
	r := db.GetPrincipalRow(row)

	return principalFields{
		id: r.ID, subject: r.Subject, displayName: r.DisplayName, kind: r.Kind, source: r.Source,
		createdAtMs: r.CreatedAtMs, updatedAtMs: r.UpdatedAtMs, groupCount: r.GroupCount,
		disabledAtMs: r.DisabledAtMs.Int64, namespace: r.Namespace, breakGlass: r.BreakGlass,
	}
}

// principalOf maps the columns of a principal to a principal.
func principalOf(f principalFields) principal.Principal {
	return principal.Principal{
		CreatedAt:   codec.FromMillis(f.createdAtMs),
		UpdatedAt:   codec.FromMillis(f.updatedAtMs),
		DisabledAt:  codec.FromOptionalMillis(f.disabledAtMs),
		ID:          f.id,
		Subject:     f.subject,
		DisplayName: f.displayName,
		Kind:        principal.Kind(f.kind),
		Source:      principal.Source(f.source),
		Namespace:   namespace.Name(f.namespace),
		GroupCount:  f.groupCount,
		BreakGlass:  f.breakGlass,
	}
}

// groupRow maps any generated group row to the row of GetGroup.
func groupRow[T db.GetGroupRow | db.ListGroupsRow | db.ListGroupsOfPrincipalRow](row T) db.GetGroupRow {
	return db.GetGroupRow(row)
}

// groupOf maps the columns of a group to a group.
func groupOf(row db.GetGroupRow) principal.Group {
	return principal.Group{
		CreatedAt:                codec.FromMillis(row.CreatedAtMs),
		UpdatedAt:                codec.FromMillis(row.UpdatedAtMs),
		ID:                       row.ID,
		Name:                     row.Name,
		Description:              row.Description,
		Source:                   principal.Source(row.Source),
		MemberCount:              row.MemberCount,
		ClosedToIdentityProvider: row.ClosedToIdentityProvider,
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
func nullMillis(ms int64) sql.NullInt64 {
	return sql.NullInt64{Int64: ms, Valid: true}
}

// groupNameKey returns the key that makes group names unique without regard
// to case. Group names hold ASCII only (principal.ValidateGroupName).
func groupNameKey(name string) string {
	return strings.ToLower(name)
}

// isUniqueViolation reports whether err is a unique or primary key
// constraint failure of the driver.
func isUniqueViolation(err error) bool {
	sqliteErr, ok := errors.AsType[*sqlite.Error](err)

	return ok && (sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_UNIQUE ||
		sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY)
}

// guardSiteAdministrator returns the body of a transaction that runs change,
// which ends a principal, a membership or a grant, and returns
// access.ErrLastSiteAdministrator when the change takes the last site
// administrator. The count runs in the transaction of the change, so a change
// that commits first is in the count. An installation with no site
// administrator before the change refuses nothing.
//
// The guard is correct only because its callers run in a write transaction of
// the store, which SQLite serializes (an immediate transaction holds the write
// lock before the first statement). Two removals of two administrators then
// run one after the other, and the second count reads the commit of the first.
// A new caller must use a write transaction of the store too.
func guardSiteAdministrator(ctx context.Context, change func(q *db.Queries) error) func(q *db.Queries) error {
	return func(q *db.Queries) error {
		before, err := q.CountSiteAdministrators(ctx, access.RoleSiteAdministrator)
		if err != nil {
			return fmt.Errorf("count site administrators: %w", err)
		}

		if changeErr := change(q); changeErr != nil {
			return changeErr
		}

		if before == 0 {
			return nil
		}

		after, err := q.CountSiteAdministrators(ctx, access.RoleSiteAdministrator)
		if err != nil {
			return fmt.Errorf("count site administrators: %w", err)
		}

		if after == 0 {
			return access.ErrLastSiteAdministrator
		}

		return nil
	}
}
