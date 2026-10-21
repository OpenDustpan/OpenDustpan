package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/store/internal/sinkstore"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// NotificationSinks returns the store of notification sinks. Each write
// checks the installation state and the fence of its namespace, and appends
// its audit entry in its own transaction. The store keeps the sinks of
// namespaces. It refuses a sink of audit.ServerScope with notify.ErrInvalid.
//
//nolint:ireturn // Store-agnostic wiring takes the port.
func (s *Store) NotificationSinks() notify.SinkStore {
	return sinks{s: s}
}

// sinks is notify.SinkStore over the database of a Store.
type sinks struct {
	s *Store
}

var _ notify.SinkStore = sinks{s: nil}

// checkSinkScope returns notify.ErrInvalid when the fence or the audit entry
// of a write does not belong to the scope of the sink.
//
// Defense in depth: the fence check of the transaction compares the fence with
// the namespace row, and the audit append compares the entry with the fence.
// This check stays so that a sink of one namespace cannot be written under the
// fence or the audit entry of another namespace, whatever the caller does.
func checkSinkScope(scope namespace.Name, fence namespace.Fence, e audit.Entry) error {
	if scope == "" || scope == audit.ServerScope || fence.Namespace != scope || e.Namespace != scope {
		return fmt.Errorf("%w: the fence and the audit entry must name the namespace %q of the sink",
			notify.ErrInvalid, scope)
	}

	return nil
}

// PutSink implements notify.SinkStore. When ctx carries an idempotency
// completion, the transaction also stores its answer for the stored sink.
func (k sinks) PutSink(
	ctx context.Context, s notify.Sink, secrets []notify.SealedSecret, fence namespace.Fence, e audit.Entry,
) error {
	if err := s.ValidateWrite(); err != nil {
		return fmt.Errorf("put sink: %w", err)
	}

	if err := s.ValidateSealed(secrets); err != nil {
		return fmt.Errorf("put sink: %w", err)
	}

	if err := checkSinkScope(s.Scope, fence, e); err != nil {
		return err
	}

	return k.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		stored, err := putSinkRow(ctx, q, s)
		if err != nil {
			return err
		}

		if secretsErr := putSinkSecrets(ctx, q, s, secrets); secretsErr != nil {
			return secretsErr
		}

		return finishCompletion(ctx, q, stored)
	})
}

// putSinkRow checks the version and the scope of the stored sink, and writes
// the row of s. It returns the sink as stored.
func putSinkRow(ctx context.Context, q *db.Queries, s notify.Sink) (notify.Sink, error) {
	var prev *sinkstore.Row

	old, err := q.GetNotificationSink(ctx, s.ID)

	switch {
	case err == nil:
		prev = &sinkstore.Row{
			ID: old.ID, Namespace: old.Namespace, Kind: old.Kind, Name: old.Name, Platform: old.Platform,
			Target: old.Target, CreatedBy: old.CreatedBy, Version: old.Version,
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return notify.Sink{}, fmt.Errorf("select sink: %w", err)
	default:
		// The sink is new.
	}

	stored, err := sinkstore.CheckWrite(prev, s)
	if err != nil {
		return notify.Sink{}, fmt.Errorf("put sink: %w", err)
	}

	if prev != nil {
		return stored, updateSinkRow(ctx, q, s)
	}

	return stored, insertSinkRow(ctx, q, s)
}

// updateSinkRow changes the name and the target of the stored sink at the
// version of s. It returns notify.ErrConflict when another write changed the
// sink since.
func updateSinkRow(ctx context.Context, q *db.Queries, s notify.Sink) error {
	n, err := q.UpdateNotificationSink(ctx, db.UpdateNotificationSinkParams{
		Name: s.Name, Target: s.Target, ID: s.ID, Version: s.Version, Namespace: s.Scope.String(),
	})
	if err != nil {
		return fmt.Errorf("update sink: %w", err)
	}

	if n == 0 {
		return fmt.Errorf("put sink %s: %w", s.ID, notify.ErrConflict)
	}

	return nil
}

// insertSinkRow writes a new sink. It returns notify.ErrConflict when another
// write made the sink since.
func insertSinkRow(ctx context.Context, q *db.Queries, s notify.Sink) error {
	n, err := q.InsertNotificationSink(ctx, db.InsertNotificationSinkParams{
		ID: s.ID, Namespace: s.Scope.String(), Kind: string(s.Kind), Name: s.Name, Platform: s.Platform,
		Target: s.Target, CreatedBy: s.CreatedBy,
	})
	if err != nil {
		return fmt.Errorf("insert sink: %w", err)
	}

	if n == 0 {
		return fmt.Errorf("put sink %s: %w", s.ID, notify.ErrConflict)
	}

	return nil
}

// putSinkSecrets writes the secret items of s: the new items, the order of the
// refs, and the drop of the items that s no longer names.
func putSinkSecrets(ctx context.Context, q *db.Queries, s notify.Sink, secrets []notify.SealedSecret) error {
	rows, err := q.ListNotificationSinkSecrets(
		ctx,
		db.ListNotificationSinkSecretsParams{Namespace: s.Scope.String(), SinkID: s.ID},
	)
	if err != nil {
		return fmt.Errorf("select sink secrets: %w", err)
	}

	stored := make([]string, 0, len(rows))
	for _, r := range rows {
		stored = append(stored, r.Ref)
	}

	fresh := make([]string, 0, len(secrets))
	for _, sec := range secrets {
		fresh = append(fresh, sec.Ref())
	}

	change, err := sinkstore.Plan(s, stored, fresh)
	if err != nil {
		return fmt.Errorf("put sink: %w", err)
	}

	if applyErr := applySecretChange(ctx, q, s, change); applyErr != nil {
		return applyErr
	}

	return writeSinkSecrets(ctx, q, s, secrets)
}

// applySecretChange deletes the items that the sink no longer names, and
// records each new ref as held. A ref names one value for good, so a ref that
// the namespace held before is notify.ErrInvalid.
func applySecretChange(ctx context.Context, q *db.Queries, s notify.Sink, change sinkstore.Change) error {
	scope := s.Scope.String()

	for _, ref := range change.Drop {
		err := q.DeleteNotificationSinkSecret(ctx, db.DeleteNotificationSinkSecretParams{
			Ref: ref, SinkID: s.ID, Namespace: scope,
		})
		if err != nil {
			return fmt.Errorf("delete sink secret: %w", err)
		}
	}

	for _, ref := range change.Claim {
		n, err := q.ClaimNotificationSinkRef(ctx, db.ClaimNotificationSinkRefParams{Namespace: scope, Ref: ref})
		if err != nil {
			return fmt.Errorf("claim sink secret ref: %w", err)
		}

		// The namespace held this ref before.
		if n == 0 {
			return fmt.Errorf("%w: the secret ref %s of sink %s was used before", notify.ErrInvalid, ref, s.ID)
		}
	}

	return nil
}

// writeSinkSecrets stores the new items of the write and sets the position of
// each ref of s.
func writeSinkSecrets(ctx context.Context, q *db.Queries, s notify.Sink, secrets []notify.SealedSecret) error {
	byRef := make(map[string]notify.SealedSecret, len(secrets))
	for _, sec := range secrets {
		byRef[sec.Ref()] = sec
	}

	for i, ref := range s.SecretRefs {
		place := secretPlace{sink: s, position: i, ref: ref, fresh: nil}

		if sec, isNew := byRef[ref]; isNew {
			place.fresh = &sec
		}

		if writeErr := writeSinkSecret(ctx, q, place); writeErr != nil {
			return writeErr
		}
	}

	return nil
}

// secretPlace is where one secret item of a sink goes: its position among the
// refs of the sink, and its new sealed value, which is nil for a stored item.
type secretPlace struct {
	sink     notify.Sink
	position int
	ref      string
	fresh    *notify.SealedSecret
}

// writeSinkSecret stores the new item of the place, or moves the stored item
// to the position of the place.
func writeSinkSecret(ctx context.Context, q *db.Queries, p secretPlace) error {
	scope := p.sink.Scope.String()

	if p.fresh == nil {
		err := q.MoveNotificationSinkSecret(ctx, db.MoveNotificationSinkSecretParams{
			Position: int64(p.position), Ref: p.ref, SinkID: p.sink.ID, Namespace: scope,
		})
		if err != nil {
			return fmt.Errorf("move sink secret: %w", err)
		}

		return nil
	}

	err := q.InsertNotificationSinkSecret(ctx, db.InsertNotificationSinkSecretParams{
		Namespace: scope, Ref: p.ref, SinkID: p.sink.ID, Position: int64(p.position), Ciphertext: p.fresh.Ciphertext(),
	})
	if err != nil {
		return fmt.Errorf("insert sink secret: %w", err)
	}

	return nil
}

// SinkSecrets implements notify.SinkStore.
func (k sinks) SinkSecrets(ctx context.Context, scope namespace.Name, id string) ([]notify.SealedSecret, error) {
	row, err := k.s.queries.GetNotificationSink(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && row.Namespace != scope.String() {
		return nil, fmt.Errorf("secrets of sink %s: %w", id, notify.ErrNotFound)
	}

	if err != nil {
		return nil, fmt.Errorf("select sink: %w", err)
	}

	rows, err := k.s.queries.ListNotificationSinkSecrets(ctx, db.ListNotificationSinkSecretsParams{
		Namespace: scope.String(), SinkID: id,
	})
	if err != nil {
		return nil, fmt.Errorf("select sink secrets: %w", err)
	}

	out := make([]notify.SealedSecret, 0, len(rows))
	for _, r := range rows {
		out = append(out, notify.StoredSecret(scope, id, r.Ref, r.Ciphertext))
	}

	return out, nil
}

// DeleteSink implements notify.SinkStore. When ctx carries an idempotency
// completion, the transaction also stores its answer.
func (k sinks) DeleteSink(ctx context.Context, id string, fence namespace.Fence, e audit.Entry) error {
	scope := fence.Namespace

	// A zero fence names no namespace, so it names no stored sink.
	if scope == "" || scope == audit.ServerScope {
		return fmt.Errorf("delete sink %s: %w", id, notify.ErrNotFound)
	}

	if e.Namespace != scope {
		return fmt.Errorf("%w: the audit entry must name the namespace %q of the fence", notify.ErrInvalid, scope)
	}

	return k.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		row, err := q.GetNotificationSink(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && row.Namespace != scope.String() {
			return fmt.Errorf("delete sink %s: %w", id, notify.ErrNotFound)
		}

		if err != nil {
			return fmt.Errorf("select sink: %w", err)
		}

		err = q.DeleteNotificationSinkSecrets(ctx, db.DeleteNotificationSinkSecretsParams{
			SinkID: id, Namespace: scope.String(),
		})
		if err != nil {
			return fmt.Errorf("delete sink secrets: %w", err)
		}

		_, err = q.DeleteNotificationSink(
			ctx,
			db.DeleteNotificationSinkParams{ID: id, Namespace: scope.String()},
		)
		if err != nil {
			return fmt.Errorf("delete sink: %w", err)
		}

		return finishCompletion(ctx, q, id)
	})
}

// Sinks implements notify.SinkStore. A row that fails its checks is left out.
func (k sinks) Sinks(ctx context.Context, scope namespace.Name) ([]notify.Sink, error) {
	rows, err := k.s.queries.ListNotificationSinks(ctx, scope.String())
	if err != nil {
		return nil, fmt.Errorf("select sinks: %w", err)
	}

	refRows, err := k.s.queries.ListNotificationSinkRefs(ctx, scope.String())
	if err != nil {
		return nil, fmt.Errorf("select sink refs: %w", err)
	}

	refs := make(map[string][]string, len(rows))
	for _, r := range refRows {
		refs[r.SinkID] = append(refs[r.SinkID], r.Ref)
	}

	out := make([]notify.Sink, 0, len(rows))

	for _, row := range rows {
		s, sinkErr := sinkstore.Sink(sinkstore.Row{
			ID: row.ID, Namespace: row.Namespace, Kind: row.Kind, Name: row.Name, Platform: row.Platform,
			Target: row.Target, CreatedBy: row.CreatedBy, Version: row.Version,
		}, refs[row.ID])
		// A row that fails its checks is not served. The warning names the row by
		// its ID and its namespace: the text of the check can hold a target, so
		// it stays out of the log.
		if sinkErr != nil || s.Scope != scope {
			k.s.logger.WarnContext(ctx, "notification destination left out: it fails its checks",
				slog.String("id", row.ID), slog.String("namespace", scope.String()))

			continue
		}

		out = append(out, s)
	}

	return out, nil
}

// RecordSinkTest implements notify.SinkStore. When ctx carries an idempotency
// completion, the transaction also stores its answer.
func (k sinks) RecordSinkTest(ctx context.Context, id string, fence namespace.Fence, e audit.Entry) error {
	scope := fence.Namespace

	// A zero fence names no namespace, so it names no stored sink.
	if scope == "" || scope == audit.ServerScope {
		return fmt.Errorf("record test of sink %s: %w", id, notify.ErrNotFound)
	}

	if e.Namespace != scope {
		return fmt.Errorf("%w: the audit entry must name the namespace %q of the fence", notify.ErrInvalid, scope)
	}

	return k.s.inFencedAuditedTx(ctx, fence, &e, func(q *db.Queries) error {
		row, err := q.GetNotificationSink(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && row.Namespace != scope.String() {
			return fmt.Errorf("record test of sink %s: %w", id, notify.ErrNotFound)
		}

		if err != nil {
			return fmt.Errorf("select sink: %w", err)
		}

		return finishCompletion(ctx, q, id)
	})
}
