package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/profile"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/sqlite/internal/db"
)

var _ profile.Store = (*Profiles)(nil)

// Profiles is the store of profiles on the database of a Store. It
// implements profile.Store. A separate type keeps its List method apart from
// the List methods of the other ports of the Store.
type Profiles struct {
	store *Store
}

// Profiles returns the store of profiles on s.
func (s *Store) Profiles() *Profiles {
	return &Profiles{store: s}
}

// profileNamespaceID returns the stable ID of the namespace ns, or
// profile.ErrNotFound.
func profileNamespaceID(ctx context.Context, q *db.Queries, ns namespace.Name) (string, error) {
	id, err := q.GetNamespaceID(ctx, ns.String())
	if errors.Is(err, sql.ErrNoRows) {
		return "", profile.ErrNotFound
	}

	if err != nil {
		return "", fmt.Errorf("select namespace ID: %w", err)
	}

	return id, nil
}

// liveProfile reads the live profile id of the namespace with the stable ID
// nsID.
func liveProfile(ctx context.Context, q *db.Queries, nsID string, id profile.ID) (db.Profile, error) {
	row, err := q.GetLiveProfileByID(ctx, db.GetLiveProfileByIDParams{NamespaceID: nsID, ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return db.Profile{}, profile.ErrNotFound
	}

	if err != nil {
		return db.Profile{}, fmt.Errorf("select profile: %w", err)
	}

	return row, nil
}

func insertProfileRevision(ctx context.Context, q *db.Queries, nsID string, r profile.Revision) error {
	err := q.InsertProfileRevision(ctx, db.InsertProfileRevisionParams{
		NamespaceID: nsID, ProfileID: string(r.Profile), Number: int64(r.Number), Document: string(r.Document),
		DocumentSha256: r.SHA256, Author: r.Author, CreatedAtMs: codec.Millis(r.CreatedAt), Reason: r.Reason,
	})
	if err != nil {
		return fmt.Errorf("insert profile revision: %w", err)
	}

	return nil
}

// Create implements profile.Store.
func (p *Profiles) Create(
	ctx context.Context, pr profile.Profile, r profile.Revision, fence namespace.Fence, e []audit.Entry,
) (profile.Profile, error) {
	if r.Number != 1 || r.Profile != pr.ID || pr.Latest != 1 || pr.Namespace != fence.Namespace {
		return profile.Profile{}, fmt.Errorf("%w: a new profile has revision 1 of its own", profile.ErrInvalid)
	}

	p.store.mu.RLock()
	defer p.store.mu.RUnlock()

	written := profile.Written{Profile: pr, Revision: &r}

	err := p.inAuditedTx(ctx, fence, e, &written, func(q *db.Queries) error {
		nsID, err := profileNamespaceID(ctx, q, pr.Namespace)
		if err != nil {
			return err
		}

		if insertErr := insertProfile(ctx, q, nsID, pr); insertErr != nil {
			return insertErr
		}

		return insertProfileRevision(ctx, q, nsID, r)
	})
	if err != nil {
		return profile.Profile{}, err
	}

	return pr, nil
}

// insertProfile adds the live profile pr to the namespace with the stable ID
// nsID. It checks the name and the limit.
func insertProfile(ctx context.Context, q *db.Queries, nsID string, pr profile.Profile) error {
	_, err := q.GetLiveProfileByName(ctx, db.GetLiveProfileByNameParams{NamespaceID: nsID, Name: string(pr.Name)})
	if err == nil {
		return fmt.Errorf("%w: the name %q is in use", profile.ErrConflict, pr.Name)
	}

	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("select profile by name: %w", err)
	}

	count, err := q.CountLiveProfiles(ctx, nsID)
	if err != nil {
		return fmt.Errorf("count profiles: %w", err)
	}

	if count >= profile.MaxProfiles {
		return fmt.Errorf("%w: a namespace holds at most %d profiles", profile.ErrLimit, profile.MaxProfiles)
	}

	err = q.InsertProfile(ctx, db.InsertProfileParams{
		NamespaceID: nsID, ID: string(pr.ID), Name: string(pr.Name), Latest: int64(pr.Latest),
		CreatedBy: pr.CreatedBy, CreatedAtMs: codec.Millis(pr.CreatedAt), UpdatedAtMs: codec.Millis(pr.UpdatedAt),
	})
	if isUniqueViolation(err) {
		return fmt.Errorf("%w: the name %q is in use", profile.ErrConflict, pr.Name)
	}

	if err != nil {
		return fmt.Errorf("insert profile: %w", err)
	}

	return nil
}

// Get implements profile.Store.
func (p *Profiles) Get(ctx context.Context, ns namespace.Name, ref profile.Ref) (profile.Profile, error) {
	p.store.mu.RLock()
	defer p.store.mu.RUnlock()

	nsID, err := profileNamespaceID(ctx, p.store.queries, ns)
	if err != nil {
		return profile.Profile{}, err
	}

	var row db.Profile

	switch {
	case ref.ID != "":
		row, err = liveProfile(ctx, p.store.queries, nsID, ref.ID)
	case ref.Name != "":
		row, err = p.store.queries.GetLiveProfileByName(
			ctx,
			db.GetLiveProfileByNameParams{NamespaceID: nsID, Name: string(ref.Name)},
		)
		if errors.Is(err, sql.ErrNoRows) {
			err = profile.ErrNotFound
		}
	default:
		err = fmt.Errorf("%w: a reference names an ID or a name", profile.ErrInvalid)
	}

	if err != nil {
		return profile.Profile{}, err
	}

	return codec.ProfileFromRow(ns, codec.ProfileRow(row)), nil
}

// List implements profile.Store.
func (p *Profiles) List(ctx context.Context, f profile.Filter) (profile.Page, error) {
	if f.Limit < 1 {
		return profile.Page{}, fmt.Errorf("%w: limit must be 1 or more", profile.ErrInvalid)
	}

	p.store.mu.RLock()
	defer p.store.mu.RUnlock()

	nsID, err := profileNamespaceID(ctx, p.store.queries, f.Namespace)
	if errors.Is(err, profile.ErrNotFound) {
		return profile.Page{Profiles: []profile.Profile{}, Next: ""}, nil
	}

	if err != nil {
		return profile.Page{}, err
	}

	rows, err := p.store.queries.ListLiveProfiles(ctx, db.ListLiveProfilesParams{
		NamespaceID: nsID, AfterName: string(f.After), Folder: f.Folder, PageLimit: int64(f.Limit) + 1,
	})
	if err != nil {
		return profile.Page{}, fmt.Errorf("select profiles: %w", err)
	}

	page := profile.Page{Profiles: make([]profile.Profile, 0, min(len(rows), f.Limit)), Next: ""}

	for n := range rows {
		if n == f.Limit {
			page.Next = page.Profiles[n-1].Name

			break
		}

		page.Profiles = append(page.Profiles, codec.ProfileFromRow(f.Namespace, codec.ProfileRow(rows[n])))
	}

	return page, nil
}

// Revision implements profile.Store.
func (p *Profiles) Revision(
	ctx context.Context,
	ns namespace.Name,
	id profile.ID,
	at profile.At,
) (profile.Revision, error) {
	number := at.Number
	if number < 0 {
		return profile.Revision{}, fmt.Errorf("%w: revision number %d", profile.ErrInvalid, number)
	}

	p.store.mu.RLock()
	defer p.store.mu.RUnlock()

	var rev profile.Revision

	err := p.store.inReadTx(ctx, func(q *db.Queries) error {
		nsID, err := profileNamespaceID(ctx, q, ns)
		if err != nil {
			return err
		}

		row, err := liveProfile(ctx, q, nsID, id)
		if err != nil {
			return err
		}

		if number == 0 {
			number = int(row.Latest)
		}

		got, err := q.GetProfileRevision(ctx, db.GetProfileRevisionParams{
			NamespaceID: nsID, ProfileID: string(id), Number: int64(number),
		})
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: revision %d", profile.ErrNotFound, number)
		}

		if err != nil {
			return fmt.Errorf("select profile revision: %w", err)
		}

		rev = codec.ProfileRevisionFromRow(codec.ProfileRevisionRow(got))

		return nil
	})
	if err != nil {
		return profile.Revision{}, err
	}

	return rev, nil
}

// Revisions implements profile.Store.
func (p *Profiles) Revisions(
	ctx context.Context, ns namespace.Name, id profile.ID, before, limit int,
) ([]profile.Revision, error) {
	if limit < 1 || before < 0 {
		return nil, fmt.Errorf("%w: limit must be 1 or more and before 0 or more", profile.ErrInvalid)
	}

	if before == 0 {
		before = math.MaxInt32
	}

	p.store.mu.RLock()
	defer p.store.mu.RUnlock()

	var out []profile.Revision

	err := p.store.inReadTx(ctx, func(q *db.Queries) error {
		nsID, err := profileNamespaceID(ctx, q, ns)
		if err != nil {
			return err
		}

		if _, err = liveProfile(ctx, q, nsID, id); err != nil {
			return err
		}

		rows, err := q.ListProfileRevisions(ctx, db.ListProfileRevisionsParams{
			NamespaceID: nsID, ProfileID: string(id), BeforeNumber: int64(before), PageLimit: int64(limit),
		})
		if err != nil {
			return fmt.Errorf("select profile revisions: %w", err)
		}

		out = make([]profile.Revision, 0, len(rows))
		for n := range rows {
			out = append(out, codec.ProfileRevisionFromRow(codec.ProfileRevisionRow(rows[n])))
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// Save implements profile.Store.
func (p *Profiles) Save(
	ctx context.Context, at profile.Anchor, r profile.Revision, fence namespace.Fence, e []audit.Entry,
) (profile.Profile, error) {
	p.store.mu.RLock()
	defer p.store.mu.RUnlock()

	var updated profile.Profile

	var written profile.Written

	err := p.inAuditedTx(ctx, fence, e, &written, func(q *db.Queries) error {
		nsID, err := profileNamespaceID(ctx, q, at.Namespace)
		if err != nil {
			return err
		}

		row, err := liveProfile(ctx, q, nsID, at.ID)
		if err != nil {
			return err
		}

		if checkErr := checkNextRevision(row, at, r); checkErr != nil {
			return checkErr
		}

		if insertErr := insertProfileRevision(ctx, q, nsID, r); insertErr != nil {
			return insertErr
		}

		n, err := q.MoveProfileLatest(ctx, db.MoveProfileLatestParams{
			Next: int64(r.Number), UpdatedAtMs: codec.Millis(r.CreatedAt), NamespaceID: nsID, ID: string(at.ID),
			Base: int64(at.Latest),
		})
		if err != nil {
			return fmt.Errorf("update profile: %w", err)
		}

		if n == 0 {
			return fmt.Errorf("%w: the profile changed", profile.ErrConflict)
		}

		row.Latest, row.UpdatedAtMs = int64(r.Number), codec.Millis(r.CreatedAt)
		updated = codec.ProfileFromRow(at.Namespace, codec.ProfileRow(row))
		written = profile.Written{Profile: updated, Revision: &r}

		return nil
	})
	if err != nil {
		return profile.Profile{}, err
	}

	return updated, nil
}

// checkNextRevision checks that r is the revision after at.Latest, and that
// at.Latest is the newest revision of the profile in row.
func checkNextRevision(row db.Profile, at profile.Anchor, r profile.Revision) error {
	if int(row.Latest) != at.Latest {
		return fmt.Errorf("%w: the newest revision is %d, not %d", profile.ErrConflict, row.Latest, at.Latest)
	}

	if r.Number != at.Latest+1 || r.Profile != at.ID {
		return fmt.Errorf("%w: the next revision is number %d of the same profile", profile.ErrInvalid, at.Latest+1)
	}

	return nil
}

// Delete implements profile.Store.
func (p *Profiles) Delete(ctx context.Context, at profile.Anchor, fence namespace.Fence, e []audit.Entry) error {
	p.store.mu.RLock()
	defer p.store.mu.RUnlock()

	var written profile.Written

	return p.inAuditedTx(ctx, fence, e, &written, func(q *db.Queries) error {
		nsID, err := profileNamespaceID(ctx, q, at.Namespace)
		if err != nil {
			return err
		}

		row, err := liveProfile(ctx, q, nsID, at.ID)
		if err != nil {
			return err
		}

		if at.Latest > 0 && int(row.Latest) != at.Latest {
			return fmt.Errorf("%w: the newest revision is %d, not %d", profile.ErrConflict, row.Latest, at.Latest)
		}

		n, err := q.MarkProfileDeleted(ctx, db.MarkProfileDeletedParams{
			DeletedAtMs: e[0].At.UnixMilli(), NamespaceID: nsID, ID: string(at.ID), IfLatest: int64(at.Latest),
		})
		if err != nil {
			return fmt.Errorf("delete profile: %w", err)
		}

		if n == 0 {
			return fmt.Errorf("%w: the profile changed", profile.ErrConflict)
		}

		return nil
	})
}

// inAuditedTx runs fn and appends each entry of es in the same fenced
// transaction. It checks every entry before the transaction starts. When ctx
// carries an idempotency.Completion, the transaction also stores its answer
// for written, which fn fills before it returns.
func (p *Profiles) inAuditedTx(
	ctx context.Context, fence namespace.Fence, es []audit.Entry, written *profile.Written,
	fn func(q *db.Queries) error,
) error {
	if len(es) == 0 {
		return fmt.Errorf("%w: a write needs at least one audit entry", profile.ErrInvalid)
	}

	for n := range es {
		if err := es[n].Validate(); err != nil {
			return fmt.Errorf("audit entry: %w", err)
		}

		if es[n].Namespace != fence.Namespace {
			return fmt.Errorf("%w: entry names %q, fence names %q",
				audit.ErrInvalidEntry, es[n].Namespace, fence.Namespace)
		}
	}

	// The store appends the last entry after fn, so fn appends the others.
	last := es[len(es)-1]

	return p.store.inFencedAuditedTx(ctx, fence, &last, func(q *db.Queries) error {
		if err := fn(q); err != nil {
			return err
		}

		for n := range len(es) - 1 {
			if _, err := appendAuditEntry(ctx, q, es[n]); err != nil {
				return fmt.Errorf("append audit entry: %w", err)
			}
		}

		return finishCompletion(ctx, q, *written)
	})
}
