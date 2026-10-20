package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/durable"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/store/internal/codec"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

var (
	_ runner.PoolStore    = (*Runners)(nil)
	_ runner.MemberStore  = (*Runners)(nil)
	_ runner.SessionStore = (*Runners)(nil)
)

// Runners is the store of runner pools, join tokens, runners, handoff tokens
// and job sessions on the database of a Store. It implements runner.PoolStore,
// runner.MemberStore and runner.SessionStore. A separate type keeps the method
// names of the three ports apart from the other ports of the Store.
//
// The live claim of a durable operation is the row of durable_operations: the
// operation is running, holds the owner and the token of the claim, and its
// lease did not pass on the clock of the database. A lease that passed ends
// the claim: the next claim of the operation takes it over with a new token.
type Runners struct {
	store *Store
}

// Runners returns the store of runner pools, runners and sessions on s.
func (s *Store) Runners() *Runners {
	return &Runners{store: s}
}

// appendRunnerAudit appends the entries e in order.
func appendRunnerAudit(ctx context.Context, q *db.Queries, e []audit.Entry) error {
	for i := range e {
		if _, err := appendAuditEntry(ctx, q, e[i]); err != nil {
			return fmt.Errorf("append audit entry: %w", err)
		}
	}

	return nil
}

// loadPool returns the pool id, or runner.ErrNotFound.
func loadPool(ctx context.Context, q *db.Queries, id string) (runner.Pool, string, error) {
	row, err := q.GetRunnerPool(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return runner.Pool{}, "", runner.ErrNotFound
	}

	if err != nil {
		return runner.Pool{}, "", fmt.Errorf("select runner pool: %w", err)
	}

	p, err := codec.PoolFromRow(codec.RunnerPoolRow(row))

	return p, row.NamespaceID, err
}

// loadRunnerRow returns the runner id and the ID of its namespace, or
// runner.ErrNotFound.
func loadRunnerRow(ctx context.Context, q *db.Queries, id string) (runner.Runner, string, error) {
	row, err := q.GetRunner(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return runner.Runner{}, "", runner.ErrNotFound
	}

	if err != nil {
		return runner.Runner{}, "", fmt.Errorf("select runner: %w", err)
	}

	rn, err := codec.RunnerFromRow(codec.RunnerRow(row))

	return rn, row.NamespaceID, err
}

// loadRunner returns the runner id, or runner.ErrNotFound.
func loadRunner(ctx context.Context, q *db.Queries, id string) (runner.Runner, error) {
	rn, _, err := loadRunnerRow(ctx, q, id)

	return rn, err
}

// loadRunnerOf returns the runner id of the namespace nsID. A runner of
// another namespace is runner.ErrNotFound, as an unknown runner is.
func loadRunnerOf(ctx context.Context, q *db.Queries, id, nsID string) (runner.Runner, error) {
	rn, rnNS, err := loadRunnerRow(ctx, q, id)
	if err != nil {
		return runner.Runner{}, err
	}

	if rnNS != nsID {
		return runner.Runner{}, runner.ErrNotFound
	}

	return rn, nil
}

// CreatePool implements runner.PoolStore.
func (r *Runners) CreatePool(
	ctx context.Context, p runner.Pool, fence namespace.Fence, e []audit.Entry,
) (runner.Pool, error) {
	if err := p.Validate(); err != nil {
		return runner.Pool{}, fmt.Errorf("check pool: %w", err)
	}

	var stored runner.Pool

	err := r.inRunnerTx(ctx, fence, e, func(q *db.Queries, nsID string) error {
		named, err := q.CountRunnerPoolNamed(ctx, db.CountRunnerPoolNamedParams{NamespaceID: nsID, Name: p.Name})
		if err != nil {
			return fmt.Errorf("count pools named: %w", err)
		}

		if named > 0 {
			return runner.ErrAlreadyExists
		}

		total, err := q.CountRunnerPoolsOfNamespace(ctx, nsID)
		if err != nil {
			return fmt.Errorf("count pools: %w", err)
		}

		if total >= runner.MaxPoolsPerNamespace {
			return fmt.Errorf(
				"%w: a namespace has at most %d runner pools",
				runner.ErrLimit,
				runner.MaxPoolsPerNamespace,
			)
		}

		p.ID, p.Namespace = ident.NewID(runner.PoolIDPrefix), fence.Namespace

		row, err := codec.PoolToRow(&p)
		if err != nil {
			return fmt.Errorf("encode pool: %w", err)
		}

		err = q.InsertRunnerPool(ctx, db.InsertRunnerPoolParams{
			ID: row.ID, NamespaceID: nsID, Name: row.Name, Purpose: row.Purpose, LeaseMs: row.LeaseMs,
			Concurrency: row.Concurrency, Isolation: row.Isolation, GitMode: row.GitMode, GitHelper: row.GitHelper,
			CreatedAtMs: row.CreatedAtMs, UpdatedAtMs: row.UpdatedAtMs,
		})
		if err != nil {
			return fmt.Errorf("insert runner pool: %w", err)
		}

		stored = p

		return nil
	})
	if err != nil {
		return runner.Pool{}, err
	}

	return stored, nil
}

// UpdatePool implements runner.PoolStore.
func (r *Runners) UpdatePool(
	ctx context.Context, p runner.Pool, fence namespace.Fence, e []audit.Entry,
) (runner.Pool, error) {
	var stored runner.Pool

	err := r.inRunnerTx(ctx, fence, e, func(q *db.Queries, nsID string) error {
		old, oldNS, err := loadPool(ctx, q, p.ID)
		if err != nil {
			return err
		}

		if oldNS != nsID {
			return runner.ErrNotFound
		}

		if err = runner.CheckUpdate(&old, &p); err != nil {
			return fmt.Errorf("check pool update: %w", err)
		}

		if stepErr := checkRename(ctx, q, nsID, &old, &p); stepErr != nil {
			return stepErr
		}

		row, err := codec.PoolToRow(&p)
		if err != nil {
			return fmt.Errorf("encode pool: %w", err)
		}

		err = q.UpdateRunnerPool(ctx, db.UpdateRunnerPoolParams{
			ID: p.ID, Name: row.Name, LeaseMs: row.LeaseMs, Concurrency: row.Concurrency, Isolation: row.Isolation,
			GitMode: row.GitMode, GitHelper: row.GitHelper, UpdatedAtMs: row.UpdatedAtMs,
		})
		if err != nil {
			return fmt.Errorf("update runner pool: %w", err)
		}

		stored = p
		stored.CreatedAt = old.CreatedAt

		return nil
	})
	if err != nil {
		return runner.Pool{}, err
	}

	return stored, nil
}

// checkRename returns runner.ErrAlreadyExists when next renames the pool old
// to a name that the namespace nsID has.
func checkRename(ctx context.Context, q *db.Queries, nsID string, old, next *runner.Pool) error {
	if next.Name == old.Name {
		return nil
	}

	named, err := q.CountRunnerPoolNamed(ctx, db.CountRunnerPoolNamedParams{NamespaceID: nsID, Name: next.Name})
	if err != nil {
		return fmt.Errorf("count pools named: %w", err)
	}

	if named > 0 {
		return runner.ErrAlreadyExists
	}

	return nil
}

// DeletePool implements runner.PoolStore.
func (r *Runners) DeletePool(ctx context.Context, id string, fence namespace.Fence, e []audit.Entry) error {
	return r.inRunnerTx(ctx, fence, e, func(q *db.Queries, nsID string) error {
		_, poolNS, err := loadPool(ctx, q, id)
		if err != nil {
			return err
		}

		if poolNS != nsID {
			return runner.ErrNotFound
		}

		kinds := codec.PoolOperationKinds(id)

		open, err := q.CountOpenOperationsOfKinds(ctx, db.CountOpenOperationsOfKindsParams{
			KindA: string(kinds[0]), KindB: string(kinds[1]),
		})
		if err != nil {
			return fmt.Errorf("count open operations of pool: %w", err)
		}

		if open > 0 {
			return runner.ErrPoolInUse
		}

		if err = q.DeleteRunnerPoolJoinTokens(ctx, id); err != nil {
			return fmt.Errorf("delete join tokens: %w", err)
		}

		if err = q.DeleteRunnerPoolRunners(ctx, id); err != nil {
			return fmt.Errorf("delete runners: %w", err)
		}

		if err = q.DeleteRunnerPool(ctx, id); err != nil {
			return fmt.Errorf("delete runner pool: %w", err)
		}

		return nil
	})
}

// Pool implements runner.PoolStore.
func (r *Runners) Pool(ctx context.Context, id string) (runner.Pool, error) {
	p, _, err := loadPool(ctx, r.store.queries, id)

	return p, err
}

// Pools implements runner.PoolStore.
func (r *Runners) Pools(ctx context.Context, ns namespace.Name) ([]runner.Pool, error) {
	rows, err := r.store.queries.ListRunnerPools(ctx, ns.String())
	if err != nil {
		return nil, fmt.Errorf("select runner pools: %w", err)
	}

	out := make([]runner.Pool, 0, len(rows))

	for i := range rows {
		p, decodeErr := codec.PoolFromRow(codec.RunnerPoolRow(rows[i]))
		if decodeErr != nil {
			return nil, decodeErr
		}

		out = append(out, p)
	}

	return out, nil
}

// CreateJoinToken implements runner.PoolStore.
func (r *Runners) CreateJoinToken(
	ctx context.Context, t runner.JoinToken, fence namespace.Fence, e []audit.Entry,
) (runner.JoinToken, error) {
	var stored runner.JoinToken

	err := r.inRunnerTx(ctx, fence, e, func(q *db.Queries, nsID string) error {
		_, poolNS, err := loadPool(ctx, q, t.Pool)
		if err != nil {
			return err
		}

		if poolNS != nsID {
			return runner.ErrNotFound
		}

		if _, err = runner.JoinTTL(t.ExpiresAt.Sub(t.CreatedAt)); err != nil {
			return fmt.Errorf("check join token expiry: %w", err)
		}

		open, err := q.CountOpenRunnerJoinTokens(ctx, db.CountOpenRunnerJoinTokensParams{
			PoolID: t.Pool, NowMs: t.CreatedAt.UnixMilli(),
		})
		if err != nil {
			return fmt.Errorf("count open join tokens: %w", err)
		}

		if open >= runner.MaxOpenJoinTokens {
			return fmt.Errorf("%w: a pool has at most %d open join tokens", runner.ErrLimit, runner.MaxOpenJoinTokens)
		}

		t.ID = ident.NewID(runner.JoinTokenIDPrefix)

		err = q.InsertRunnerJoinToken(ctx, db.InsertRunnerJoinTokenParams{
			ID: t.ID, NamespaceID: nsID, PoolID: t.Pool, Sha256: t.SHA256, CreatedBy: t.CreatedBy,
			CreatedAtMs: t.CreatedAt.UnixMilli(), ExpiresAtMs: t.ExpiresAt.UnixMilli(),
		})
		if err != nil {
			return fmt.Errorf("insert join token: %w", err)
		}

		stored = t

		return nil
	})
	if err != nil {
		return runner.JoinToken{}, err
	}

	return stored, nil
}

// RevokeJoinToken implements runner.PoolStore.
func (r *Runners) RevokeJoinToken(ctx context.Context, id string, fence namespace.Fence, e []audit.Entry) error {
	// A revoke of a revoked token changes nothing, and appends nothing: the
	// transaction returns errNoChange to roll back its audit entries.
	err := r.inRunnerTx(ctx, fence, e, func(q *db.Queries, nsID string) error {
		row, err := q.GetRunnerJoinToken(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.NamespaceID != nsID) {
			return runner.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("select join token: %w", err)
		}

		t := codec.JoinTokenFromRow(codec.RunnerJoinTokenRow(row))

		switch {
		case !t.UsedAt.IsZero():
			return runner.ErrTokenUsed
		case !t.RevokedAt.IsZero():
			return errNoChange
		}

		now, err := storeNow(ctx, q)
		if err != nil {
			return err
		}

		if err = q.RevokeRunnerJoinToken(ctx, db.RevokeRunnerJoinTokenParams{RevokedAtMs: now, ID: id}); err != nil {
			return fmt.Errorf("revoke join token: %w", err)
		}

		return nil
	})
	if errors.Is(err, errNoChange) {
		return nil
	}

	return err
}

// errNoChange ends a transaction that found nothing to write. The caller
// turns it into success.
var errNoChange = errors.New("nothing to change")

// JoinTokens implements runner.PoolStore.
func (r *Runners) JoinTokens(ctx context.Context, pool string) ([]runner.JoinToken, error) {
	rows, err := r.store.queries.ListRunnerJoinTokens(ctx, pool)
	if err != nil {
		return nil, fmt.Errorf("select join tokens: %w", err)
	}

	out := make([]runner.JoinToken, 0, len(rows))

	for i := range rows {
		out = append(out, codec.JoinTokenFromRow(codec.RunnerJoinTokenRow(rows[i])))
	}

	return out, nil
}

// LookupJoin implements runner.MemberStore.
func (r *Runners) LookupJoin(ctx context.Context, sum string) (runner.JoinToken, runner.Pool, error) {
	row, err := r.store.queries.GetRunnerJoinTokenBySHA256(ctx, sum)
	if errors.Is(err, pgx.ErrNoRows) {
		return runner.JoinToken{}, runner.Pool{}, runner.ErrNotFound
	}

	if err != nil {
		return runner.JoinToken{}, runner.Pool{}, fmt.Errorf("select join token: %w", err)
	}

	p, _, err := loadPool(ctx, r.store.queries, row.PoolID)
	if err != nil {
		return runner.JoinToken{}, runner.Pool{}, err
	}

	return codec.JoinTokenFromRow(codec.RunnerJoinTokenRow(row)), p, nil
}

// Join implements runner.MemberStore.
func (r *Runners) Join(
	ctx context.Context, j runner.Joining, fence namespace.Fence, audited runner.JoinAudit,
) (runner.Joined, error) {
	var out runner.Joined

	// The entries come from the result, so the transaction appends them
	// after fn, and the fence check and the audit validation run in it.
	err := r.store.inFencedUpdateTx(ctx, fence, func(q *db.Queries) error {
		joined, err := join(ctx, q, j, fence)
		if err != nil {
			return err
		}

		e := audited(&joined)
		for i := range e {
			if err = e[i].Validate(); err != nil {
				return fmt.Errorf("audit entry: %w", err)
			}

			if e[i].Namespace != fence.Namespace {
				return fmt.Errorf("%w: entry names %q, fence names %q",
					audit.ErrInvalidEntry, e[i].Namespace, fence.Namespace)
			}
		}

		if stepErr := appendRunnerAudit(ctx, q, e); stepErr != nil {
			return stepErr
		}

		out = joined

		return nil
	})
	if err != nil {
		return runner.Joined{}, err
	}

	return out, nil
}

// join registers the runner of j in the transaction of q.
func join(ctx context.Context, q *db.Queries, j runner.Joining, fence namespace.Fence) (runner.Joined, error) {
	nsID, err := q.GetNamespaceID(ctx, fence.Namespace.String())
	if err != nil {
		return runner.Joined{}, fmt.Errorf("select namespace ID: %w", err)
	}

	tok, err := usableJoinToken(ctx, q, nsID, j.TokenSHA256, j.At)
	if err != nil {
		return runner.Joined{}, err
	}

	pool, _, err := loadPool(ctx, q, tok.Pool)
	if err != nil {
		return runner.Joined{}, err
	}

	req := j.Request
	if !pool.Allows(req.Isolation) {
		return runner.Joined{}, fmt.Errorf(
			"%w: the pool does not allow the isolation %s",
			runner.ErrInvalid,
			req.Isolation,
		)
	}

	labels, err := codec.LabelsToJSON(req.Labels)
	if err != nil {
		return runner.Joined{}, fmt.Errorf("encode labels: %w", err)
	}

	placed, err := placeRunner(ctx, q, &placement{nsID: nsID, pool: &pool, joining: &j, labels: labels})
	if err != nil {
		return runner.Joined{}, err
	}

	err = q.UseRunnerJoinToken(ctx, db.UseRunnerJoinTokenParams{
		UsedAtMs: j.At.UnixMilli(), UsedBy: placed.id, ID: tok.ID,
	})
	if err != nil {
		return runner.Joined{}, fmt.Errorf("use join token: %w", err)
	}

	stored, err := loadRunner(ctx, q, placed.id)
	if err != nil {
		return runner.Joined{}, err
	}

	return runner.Joined{
		Runner: stored, Rejoined: placed.rejoined, PreviousKeyID: placed.previousKeyID, EndedClaims: placed.ended,
	}, nil
}

// usableJoinToken returns the join token with the hash sum of the namespace
// nsID, when a join at the time at may use it.
func usableJoinToken(
	ctx context.Context, q *db.Queries, nsID, sum string, at time.Time,
) (runner.JoinToken, error) {
	row, err := q.GetRunnerJoinTokenBySHA256(ctx, sum)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && row.NamespaceID != nsID) {
		return runner.JoinToken{}, runner.ErrNotFound
	}

	if err != nil {
		return runner.JoinToken{}, fmt.Errorf("select join token: %w", err)
	}

	tok := codec.JoinTokenFromRow(codec.RunnerJoinTokenRow(row))
	if err = tok.CheckUse(at); err != nil {
		return runner.JoinToken{}, fmt.Errorf("check join token: %w", err)
	}

	return tok, nil
}

// placement is what placeRunner needs to place the runner of a join.
type placement struct {
	nsID    string
	pool    *runner.Pool
	joining *runner.Joining
	labels  string
}

// placed is the result of placeRunner: the ID of the runner, and for a join
// again the ID of its key before the join and the claims that the join ended.
type placed struct {
	id            string
	rejoined      bool
	previousKeyID string
	ended         []string
}

// placeRunner inserts the runner of j, or takes the runner of the same name
// over, and returns its ID.
func placeRunner(ctx context.Context, q *db.Queries, pl *placement) (placed, error) {
	none := placed{id: "", rejoined: false, previousKeyID: "", ended: nil}

	nsID, pool, j, labels := pl.nsID, pl.pool, pl.joining, pl.labels
	req := j.Request

	existing, err := q.GetRunnerNamed(ctx, db.GetRunnerNamedParams{PoolID: pool.ID, Name: req.Name})
	if errors.Is(err, pgx.ErrNoRows) {
		count, countErr := q.CountRunnersOfPool(ctx, pool.ID)
		if countErr != nil {
			return none, fmt.Errorf("count runners: %w", countErr)
		}

		if count >= runner.MaxRunnersPerPool {
			return none, fmt.Errorf("%w: a pool has at most %d runners", runner.ErrLimit, runner.MaxRunnersPerPool)
		}

		id := ident.NewID(runner.RunnerIDPrefix)

		err = q.InsertRunner(ctx, db.InsertRunnerParams{
			ID: id, NamespaceID: nsID, PoolID: pool.ID, Name: req.Name, Kind: string(req.Kind),
			Isolation: string(req.Isolation), Labels: labels, Release: releaseString(req.Release),
			KeyPublic: req.Key.Public, KeyCreatedAtMs: j.At.UnixMilli(), JoinedWith: j.TokenSHA256,
			LastSeenAtMs: j.At.UnixMilli(), JoinedAtMs: j.At.UnixMilli(),
		})
		if err != nil {
			return none, fmt.Errorf("insert runner: %w", err)
		}

		return placed{id: id, rejoined: false, previousKeyID: "", ended: nil}, nil
	}

	if err != nil {
		return none, fmt.Errorf("select runner: %w", err)
	}

	known, err := codec.RunnerFromRow(codec.RunnerRow(existing))
	if err != nil {
		return none, err
	}

	if !known.CanRejoin(j.At) {
		return none, runner.ErrRunnerOnline
	}

	ended, err := q.ListRunnerClaimsWithoutSession(ctx, known.ID)
	if err != nil {
		return none, fmt.Errorf("select waiting claims: %w", err)
	}

	if err = q.ExpireRunnerClaimsWithoutSession(ctx, known.ID); err != nil {
		return none, fmt.Errorf("expire waiting claims: %w", err)
	}

	err = q.RejoinRunner(ctx, db.RejoinRunnerParams{
		ID: known.ID, Kind: string(req.Kind), Isolation: string(req.Isolation), Labels: labels,
		Release: releaseString(req.Release), KeyPublic: req.Key.Public, KeyCreatedAtMs: j.At.UnixMilli(),
		JoinedWith: j.TokenSHA256, LastSeenAtMs: j.At.UnixMilli(),
	})
	if err != nil {
		return none, fmt.Errorf("rejoin runner: %w", err)
	}

	return placed{id: known.ID, rejoined: true, previousKeyID: known.Key.ID(), ended: ended}, nil
}

// releaseString encodes a release, with the empty string for the zero
// release.
func releaseString(rel runner.Release) string {
	if rel == (runner.Release{Major: 0, Minor: 0, Patch: 0, Pre: ""}) {
		return ""
	}

	return rel.String()
}

// Runner implements runner.MemberStore.
func (r *Runners) Runner(ctx context.Context, id string) (runner.Runner, error) {
	return loadRunner(ctx, r.store.queries, id)
}

// Runners implements runner.MemberStore.
func (r *Runners) Runners(ctx context.Context, pool string) ([]runner.Runner, error) {
	rows, err := r.store.queries.ListRunners(ctx, pool)
	if err != nil {
		return nil, fmt.Errorf("select runners: %w", err)
	}

	out := make([]runner.Runner, 0, len(rows))

	for i := range rows {
		rn, decodeErr := codec.RunnerFromRow(codec.RunnerRow(rows[i]))
		if decodeErr != nil {
			return nil, decodeErr
		}

		out = append(out, rn)
	}

	return out, nil
}

// liveRunner returns the runner id when its key authenticates at now.
func liveRunner(ctx context.Context, q *db.Queries, id string, now time.Time) (runner.Runner, error) {
	rn, err := loadRunner(ctx, q, id)
	if err != nil {
		return runner.Runner{}, err
	}

	switch {
	case !rn.RetiredAt.IsZero():
		return runner.Runner{}, runner.ErrRetired
	case rn.Key.ExpiredAt(now):
		return runner.Runner{}, runner.ErrKeyExpired
	default:
		return rn, nil
	}
}

// Heartbeat implements runner.MemberStore.
func (r *Runners) Heartbeat(
	ctx context.Context, id string, rel runner.Release, now time.Time,
) (runner.Runner, error) {
	var out runner.Runner

	err := r.store.inTx(ctx, func(q *db.Queries) error {
		_, err := liveRunner(ctx, q, id, now)
		if err != nil {
			return err
		}

		err = q.TouchRunner(ctx, db.TouchRunnerParams{
			ID: id, Release: releaseString(rel), LastSeenAtMs: now.UnixMilli(),
		})
		if err != nil {
			return fmt.Errorf("touch runner: %w", err)
		}

		out, err = loadRunner(ctx, q, id)

		return err
	})
	if err != nil {
		return runner.Runner{}, err
	}

	return out, nil
}

// RotateKey implements runner.MemberStore.
func (r *Runners) RotateKey(
	ctx context.Context, rot runner.Rotation, fence namespace.Fence, e []audit.Entry,
) (runner.Runner, error) {
	if err := rot.Next.Validate(); err != nil {
		return runner.Runner{}, fmt.Errorf("check next key: %w", err)
	}

	var out runner.Runner

	// A request that changes nothing appends no audit entry: errNoChange
	// rolls the transaction back.
	err := r.inRunnerTx(ctx, fence, e, func(q *db.Queries, nsID string) error {
		if _, err := loadRunnerOf(ctx, q, rot.Runner, nsID); err != nil {
			return err
		}

		current, err := liveRunner(ctx, q, rot.Runner, rot.Next.CreatedAt)
		if err != nil {
			return err
		}

		rotate, err := current.CheckRotation(rot.Signer, rot.Next, rot.Next.CreatedAt)
		if err != nil {
			return fmt.Errorf("check rotation: %w", err)
		}

		if !rotate {
			out = current

			return errNoChange
		}

		err = q.RotateRunnerKey(ctx, db.RotateRunnerKeyParams{
			ID: rot.Runner, RotatedAtMs: rot.Next.CreatedAt.UnixMilli(), KeyPublic: rot.Next.Public,
			KeyCreatedAtMs: rot.Next.CreatedAt.UnixMilli(),
		})
		if err != nil {
			return fmt.Errorf("rotate runner key: %w", err)
		}

		out, err = loadRunner(ctx, q, rot.Runner)

		return err
	})
	if errors.Is(err, errNoChange) {
		return out, nil
	}

	if err != nil {
		return runner.Runner{}, err
	}

	return out, nil
}

// SetDraining implements runner.MemberStore.
func (r *Runners) SetDraining(
	ctx context.Context, id string, draining bool, fence namespace.Fence, e []audit.Entry,
) error {
	return r.inRunnerTx(ctx, fence, e, func(q *db.Queries, nsID string) error {
		rn, err := loadRunnerOf(ctx, q, id, nsID)
		if err != nil {
			return err
		}

		if !rn.RetiredAt.IsZero() {
			return runner.ErrRetired
		}

		if err = q.SetRunnerDraining(ctx, db.SetRunnerDrainingParams{ID: id, Draining: draining}); err != nil {
			return fmt.Errorf("set draining: %w", err)
		}

		return nil
	})
}

// Retire implements runner.MemberStore.
func (r *Runners) Retire(
	ctx context.Context, id string, now time.Time, fence namespace.Fence, e []audit.Entry,
) error {
	err := r.inRunnerTx(ctx, fence, e, func(q *db.Queries, nsID string) error {
		rn, err := loadRunnerOf(ctx, q, id, nsID)
		if err != nil {
			return err
		}

		if !rn.RetiredAt.IsZero() {
			return errNoChange
		}

		if err = q.RetireRunner(ctx, db.RetireRunnerParams{ID: id, RetiredAtMs: now.UnixMilli()}); err != nil {
			return fmt.Errorf("retire runner: %w", err)
		}

		if err = q.ExpireRunnerClaims(ctx, id); err != nil {
			return fmt.Errorf("expire claims: %w", err)
		}

		return nil
	})
	if errors.Is(err, errNoChange) {
		return nil
	}

	return err
}

// operationClaim reports whether c is the live claim of the operation in
// row at nowMs, the time of the database: the lease of the claim did not
// pass. It returns the durable kind of the operation.
func operationClaim(row *db.DurableOperation, c durable.Claim, nowMs int64) (durable.Kind, bool) {
	live := row.State == string(durable.StateRunning) && row.ClaimToken == c.Token &&
		row.ClaimOwner.Valid && row.ClaimOwner.String == c.Owner &&
		row.ClaimExpiresAtMs.Valid && row.ClaimExpiresAtMs.Int64 > nowMs

	return durable.Kind(row.Kind), live
}

// liveClaim reports whether c is the live claim of its operation, and returns
// the durable kind of the operation.
func liveClaim(ctx context.Context, q *db.Queries, c durable.Claim) (durable.Kind, bool, error) {
	row, err := q.GetDurableOperation(ctx, string(c.Operation))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("select durable operation: %w", err)
	}

	nowMs, err := storeNow(ctx, q)
	if err != nil {
		return "", false, err
	}

	kind, live := operationClaim(&row, c, nowMs)

	return kind, live, nil
}

// lockClaim is liveClaim with a row lock held to the end of the transaction,
// so two instances never decide on one claim at once.
func lockClaim(ctx context.Context, q *db.Queries, c durable.Claim) (durable.Kind, bool, error) {
	row, err := q.LockDurableOperation(ctx, string(c.Operation))
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("lock durable operation: %w", err)
	}

	nowMs, err := storeNow(ctx, q)
	if err != nil {
		return "", false, err
	}

	kind, live := operationClaim(&row, c, nowMs)

	return kind, live, nil
}

// RecordClaim implements runner.SessionStore.
func (r *Runners) RecordClaim(ctx context.Context, c durable.Claim, jobID string, phase int) error {
	return r.store.inTx(ctx, func(q *db.Queries) error {
		_, live, err := lockClaim(ctx, q, c)
		if err != nil {
			return err
		}

		if !live {
			return runner.ErrClaimLost
		}

		nowMs, err := storeNow(ctx, q)
		if err != nil {
			return err
		}

		return recordClaimTime(ctx, q, c, claimStamp{atMs: nowMs, jobID: jobID, phase: phase})
	})
}

// LookupClaim implements runner.SessionStore.
func (r *Runners) LookupClaim(ctx context.Context, c durable.Claim) (runner.ClaimRecord, error) {
	var out runner.ClaimRecord

	err := r.store.inControlTx(ctx, func(q *db.Queries) error {
		_, live, err := lockClaim(ctx, q, c)
		if err != nil {
			return err
		}

		if !live {
			return runner.ErrClaimLost
		}

		row, err := q.GetRunnerClaim(ctx, db.GetRunnerClaimParams{OperationID: string(c.Operation), Token: c.Token})
		if errors.Is(err, pgx.ErrNoRows) {
			return runner.ErrNotFound
		}

		if err != nil {
			return fmt.Errorf("select claim: %w", err)
		}

		sessions, err := q.CountRunnerSessionsOfClaim(ctx, db.CountRunnerSessionsOfClaimParams{
			OperationID: string(c.Operation), Token: c.Token,
		})
		if err != nil {
			return fmt.Errorf("count sessions: %w", err)
		}

		out = runner.ClaimRecord{
			Job:        row.JobID,
			Phase:      int(row.Phase),
			ClaimedAt:  codec.RunnerTime(row.ClaimedAtMs),
			HasSession: sessions > 0,
		}

		return nil
	})
	if err != nil {
		return runner.ClaimRecord{}, err
	}

	return out, nil
}

// IssueHandoffToken implements runner.SessionStore.
func (r *Runners) IssueHandoffToken(ctx context.Context, t runner.HandoffToken, c durable.Claim) error {
	return r.store.inTx(ctx, func(q *db.Queries) error {
		kind, live, err := lockClaim(ctx, q, c)
		if err != nil {
			return err
		}

		if !live || c.Owner != t.Runner {
			return runner.ErrClaimLost
		}

		if stepErr := checkHandoffKind(ctx, q, t.Runner, kind); stepErr != nil {
			return stepErr
		}

		if stepErr := refuseSessionOfClaim(ctx, q, c); stepErr != nil {
			return stepErr
		}

		if stepErr := storeHandoffToken(ctx, q, t); stepErr != nil {
			return stepErr
		}

		return recordClaimTime(ctx, q, c, claimStamp{atMs: t.IssuedAt.UnixMilli(), jobID: "", phase: 0})
	})
}

// checkHandoffKind returns runner.ErrInvalid when the operation kind is not a
// kind that the pool of the runner id claims.
func checkHandoffKind(ctx context.Context, q *db.Queries, id string, kind durable.Kind) error {
	rn, err := loadRunner(ctx, q, id)
	if err != nil {
		return err
	}

	pool, _, err := loadPool(ctx, q, rn.Pool)
	if err != nil {
		return err
	}

	kinds, err := runner.ClaimKinds(&pool)
	if err != nil {
		return fmt.Errorf("claim kinds: %w", err)
	}

	if !slices.Contains(kinds, kind) {
		return fmt.Errorf("%w: the operation is not of a kind of the pool", runner.ErrInvalid)
	}

	return nil
}

// refuseSessionOfClaim returns runner.ErrConflict when the claim c has a job
// session.
func refuseSessionOfClaim(ctx context.Context, q *db.Queries, c durable.Claim) error {
	sessions, err := q.CountRunnerSessionsOfClaim(ctx, db.CountRunnerSessionsOfClaimParams{
		OperationID: string(c.Operation), Token: c.Token,
	})
	if err != nil {
		return fmt.Errorf("count sessions: %w", err)
	}

	if sessions > 0 {
		return runner.ErrConflict
	}

	return nil
}

// storeHandoffToken replaces the handoff tokens of the claim of t with t.
func storeHandoffToken(ctx context.Context, q *db.Queries, t runner.HandoffToken) error {
	err := q.DeleteRunnerHandoffTokensOfClaim(ctx, db.DeleteRunnerHandoffTokensOfClaimParams{
		OperationID: t.Operation, Token: t.Token,
	})
	if err != nil {
		return fmt.Errorf("delete handoff tokens: %w", err)
	}

	err = q.InsertRunnerHandoffToken(ctx, db.InsertRunnerHandoffTokenParams{
		Sha256: t.SHA256, OperationID: t.Operation, Token: t.Token, RunnerID: t.Runner,
		IssuedAtMs: t.IssuedAt.UnixMilli(), ExpiresAtMs: t.ExpiresAt.UnixMilli(),
	})
	if err != nil {
		return fmt.Errorf("insert handoff token: %w", err)
	}

	return nil
}

// claimStamp is the claim time in Unix milliseconds of a claim, with its job
// and phase.
type claimStamp struct {
	atMs  int64
	jobID string
	phase int
}

// recordClaimTime records the claim time of the claim c, with its job and
// phase, when it has no record.
func recordClaimTime(ctx context.Context, q *db.Queries, c durable.Claim, rec claimStamp) error {
	err := q.InsertRunnerClaimTime(ctx, db.InsertRunnerClaimTimeParams{
		OperationID: string(
			c.Operation,
		),
		Token:       c.Token,
		ClaimedAtMs: rec.atMs,
		JobID:       rec.jobID,
		Phase:       int64(rec.phase),
	})
	if err != nil {
		return fmt.Errorf("record claim time: %w", err)
	}

	return nil
}

// OpenSession implements runner.SessionStore.
func (r *Runners) OpenSession(ctx context.Context, sum string, k runner.Key, now time.Time) (runner.Session, error) {
	var out runner.Session

	err := r.store.inTx(ctx, func(q *db.Queries) error {
		t, err := openableHandoff(ctx, q, sum, now)
		if err != nil {
			return err
		}

		err = q.UseRunnerHandoffToken(ctx, db.UseRunnerHandoffTokenParams{
			Sha256: sum, UsedAtMs: now.UnixMilli(),
		})
		if err != nil {
			return fmt.Errorf("use handoff token: %w", err)
		}

		out = runner.Session{
			ID: ident.NewID(runner.SessionIDPrefix), Operation: t.Operation, Token: t.Token, Runner: t.Runner,
			Key: runner.Key{Public: k.Public, CreatedAt: now.UTC()}, CreatedAt: now.UTC(),
		}

		err = q.InsertRunnerSession(ctx, db.InsertRunnerSessionParams{
			ID: out.ID, OperationID: out.Operation, Token: out.Token, RunnerID: out.Runner, KeyPublic: k.Public,
			KeyCreatedAtMs: now.UnixMilli(), CreatedAtMs: now.UnixMilli(),
		})
		if err != nil {
			return fmt.Errorf("insert job session: %w", err)
		}

		return nil
	})
	if err != nil {
		return runner.Session{}, err
	}

	return out, nil
}

// openableHandoff returns the handoff token with the hash sum when a session
// may open with it at now: the token is not used or expired, its claim is
// live, and the session wait limit did not pass.
func openableHandoff(ctx context.Context, q *db.Queries, sum string, now time.Time) (runner.HandoffToken, error) {
	row, err := q.LockRunnerHandoffToken(ctx, sum)
	if errors.Is(err, pgx.ErrNoRows) {
		return runner.HandoffToken{}, runner.ErrNotFound
	}

	if err != nil {
		return runner.HandoffToken{}, fmt.Errorf("select handoff token: %w", err)
	}

	t := codec.HandoffTokenFromRow(codec.RunnerHandoffTokenRow(row))
	if err = t.CheckUse(now); err != nil {
		return runner.HandoffToken{}, fmt.Errorf("check handoff token: %w", err)
	}

	c := durable.Claim{Operation: durable.ID(t.Operation), Owner: t.Runner, Token: t.Token, ExpiresAt: time.Time{}}

	_, live, err := lockClaim(ctx, q, c)
	if err != nil {
		return runner.HandoffToken{}, err
	}

	if !live {
		return runner.HandoffToken{}, runner.ErrClaimLost
	}

	claimed, err := q.GetRunnerClaim(ctx, db.GetRunnerClaimParams{OperationID: t.Operation, Token: t.Token})
	if errors.Is(err, pgx.ErrNoRows) {
		return runner.HandoffToken{}, fmt.Errorf("%w: the claim has no record", runner.ErrClaimLost)
	}

	if err != nil {
		return runner.HandoffToken{}, fmt.Errorf("select claim time: %w", err)
	}

	if now.Sub(codec.RunnerTime(claimed.ClaimedAtMs)) > runner.MaxSessionWait {
		return runner.HandoffToken{}, fmt.Errorf("%w: no job session %v after the claim", runner.ErrClaimLost,
			runner.MaxSessionWait)
	}

	return t, nil
}

// RunnerRenew implements runner.SessionStore.
func (r *Runners) RunnerRenew(
	ctx context.Context, c durable.Claim, fence namespace.Fence, d time.Duration, now time.Time,
) (durable.Claim, error) {
	if d <= 0 {
		return durable.Claim{}, fmt.Errorf("%w: a renewal needs a positive lease", runner.ErrInvalid)
	}

	var out durable.Claim

	err := r.store.inFencedTx(ctx, fence, func(q *db.Queries) error {
		lost, err := renewState(ctx, q, c, now)
		if err != nil {
			return err
		}

		if lost {
			return runner.ErrSessionWaitPassed
		}

		dbNow, err := storeNow(ctx, q)
		if err != nil {
			return err
		}

		expires := dbNow + d.Milliseconds()

		err = q.SetRunnerClaimExpiry(ctx, db.SetRunnerClaimExpiryParams{
			ID: string(c.Operation), Token: c.Token, ExpiresAtMs: pgtype.Int8{Int64: expires, Valid: true},
		})
		if err != nil {
			return fmt.Errorf("move claim expiry: %w", err)
		}

		out = c
		out.ExpiresAt = codec.RunnerTime(expires)

		return nil
	})
	if err != nil {
		return durable.Claim{}, err
	}

	return out, nil
}

// renewState checks the renewal of the claim c at now. It returns
// true when the claim must be released, and any other refusal as an error.
func renewState(ctx context.Context, q *db.Queries, c durable.Claim, now time.Time) (bool, error) {
	_, live, err := lockClaim(ctx, q, c)
	if err != nil {
		return false, err
	}

	if !live {
		return false, runner.ErrClaimLost
	}

	claimed, err := q.GetRunnerClaim(ctx, db.GetRunnerClaimParams{
		OperationID: string(c.Operation), Token: c.Token,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, fmt.Errorf("%w: the claim has no record", runner.ErrClaimLost)
	}

	if err != nil {
		return false, fmt.Errorf("select claim time: %w", err)
	}

	sessions, err := q.CountRunnerSessionsOfClaim(ctx, db.CountRunnerSessionsOfClaimParams{
		OperationID: string(c.Operation), Token: c.Token,
	})
	if err != nil {
		return false, fmt.Errorf("count sessions: %w", err)
	}

	checkErr := runner.CheckRunnerRenew(codec.RunnerTime(claimed.ClaimedAtMs), sessions > 0, now)
	if checkErr != nil && !errors.Is(checkErr, runner.ErrClaimLost) {
		return false, checkErr
	}

	return checkErr != nil, nil
}

// Session implements runner.SessionStore.
func (r *Runners) Session(ctx context.Context, id string) (runner.Session, error) {
	row, err := r.store.queries.GetRunnerSession(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return runner.Session{}, runner.ErrNotFound
	}

	if err != nil {
		return runner.Session{}, fmt.Errorf("select job session: %w", err)
	}

	if row.RunnerRetiredAtMs != 0 {
		return runner.Session{}, runner.ErrRetired
	}

	c := durable.Claim{
		Operation: durable.ID(row.OperationID),
		Owner:     row.RunnerID,
		Token:     row.Token,
		ExpiresAt: time.Time{},
	}

	_, live, err := liveClaim(ctx, r.store.queries, c)
	if err != nil {
		return runner.Session{}, err
	}

	if !live {
		return runner.Session{}, runner.ErrClaimLost
	}

	return runner.Session{
		ID: row.ID, Operation: row.OperationID, Token: row.Token, Runner: row.RunnerID,
		Key:       runner.Key{Public: row.KeyPublic, CreatedAt: codec.RunnerTime(row.KeyCreatedAtMs)},
		CreatedAt: codec.RunnerTime(row.CreatedAtMs),
	}, nil
}

// UseNonce implements runner.SessionStore.
func (r *Runners) UseNonce(ctx context.Context, subject, nonce string, expires, now time.Time) error {
	return r.store.inTx(ctx, func(q *db.Queries) error {
		if err := q.DeleteExpiredRunnerNonces(ctx, now.UnixMilli()); err != nil {
			return fmt.Errorf("delete expired nonces: %w", err)
		}

		n, err := q.UseRunnerNonce(ctx, db.UseRunnerNonceParams{
			Subject: subject, Nonce: nonce, ExpiresAtMs: expires.UnixMilli(),
		})
		if err != nil {
			return fmt.Errorf("keep nonce: %w", err)
		}

		if n == 0 {
			return runner.ErrReplay
		}

		return nil
	})
}

// storeNow returns the time of the database clock in Unix milliseconds.
func storeNow(ctx context.Context, q *db.Queries) (int64, error) {
	now, err := q.DatabaseNowMs(ctx)
	if err != nil {
		return 0, fmt.Errorf("read the database clock: %w", err)
	}

	return now, nil
}

// inRunnerTx runs fn in a transaction that checks fence and then appends the
// audit entries e, so an error changes nothing.
func (r *Runners) inRunnerTx(
	ctx context.Context, fence namespace.Fence, e []audit.Entry, fn func(q *db.Queries, nsID string) error,
) error {
	for i := range e {
		if err := e[i].Validate(); err != nil {
			return fmt.Errorf("audit entry: %w", err)
		}

		if e[i].Namespace != fence.Namespace {
			return fmt.Errorf("%w: entry names %q, fence names %q",
				audit.ErrInvalidEntry, e[i].Namespace, fence.Namespace)
		}
	}

	return r.store.inFencedUpdateTx(ctx, fence, func(q *db.Queries) error {
		nsID, err := q.GetNamespaceID(ctx, fence.Namespace.String())
		if err != nil {
			return fmt.Errorf("select namespace ID: %w", err)
		}

		if fnErr := fn(q, nsID); fnErr != nil {
			return fnErr
		}

		return appendRunnerAudit(ctx, q, e)
	})
}
