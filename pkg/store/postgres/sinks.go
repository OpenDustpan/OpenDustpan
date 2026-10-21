package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"

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
	if err := s.Validate(); err != nil {
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

		if err = putSinkSecrets(ctx, q, s, secrets); err != nil {
			return err
		}

		return finishCompletion(ctx, q, stored)
	})
}

// putSinkRow checks the version and the scope of the stored sink, and writes
// the row of s. It returns the sink as stored.
func putSinkRow(ctx context.Context, q *db.Queries, s notify.Sink) (notify.Sink, error) {
	old, err := q.GetNotificationSink(ctx, s.ID)
	found := err == nil

	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return notify.Sink{}, fmt.Errorf("select sink: %w", err)
	}

	var version int64

	if found {
		if old.Namespace != s.Scope.String() {
			return notify.Sink{}, fmt.Errorf("put sink %s: %w", s.ID, notify.ErrNotFound)
		}

		version = old.Version
	}

	if s.Version != version {
		return notify.Sink{}, fmt.Errorf("put sink %s: %w", s.ID, notify.ErrConflict)
	}

	if found && (old.Kind != string(s.Kind) || old.Platform != s.Platform) {
		return notify.Sink{}, fmt.Errorf("put sink %s: %w: a new kind or platform is a new sink", s.ID, notify.ErrInvalid)
	}

	stored := s
	stored.Version = version + 1

	if found {
		stored.CreatedBy = old.CreatedBy

		n, updErr := q.UpdateNotificationSink(ctx, db.UpdateNotificationSinkParams{
			Name: s.Name, Target: s.Target, ID: s.ID, Version: s.Version, Namespace: s.Scope.String(),
		})
		if updErr != nil {
			return notify.Sink{}, fmt.Errorf("update sink: %w", updErr)
		}

		if n == 0 {
			return notify.Sink{}, fmt.Errorf("put sink %s: %w", s.ID, notify.ErrConflict)
		}

		return stored, nil
	}

	n, insErr := q.InsertNotificationSink(ctx, db.InsertNotificationSinkParams{
		ID: s.ID, Namespace: s.Scope.String(), Kind: string(s.Kind), Name: s.Name, Platform: s.Platform,
		Target: s.Target, CreatedBy: s.CreatedBy,
	})
	if insErr != nil {
		return notify.Sink{}, fmt.Errorf("insert sink: %w", insErr)
	}

	if n == 0 {
		return notify.Sink{}, fmt.Errorf("put sink %s: %w", s.ID, notify.ErrConflict)
	}

	return stored, nil
}

// putSinkSecrets writes the secret items of s: the new items, the order of the
// refs, and the drop of the items that s no longer names.
func putSinkSecrets(ctx context.Context, q *db.Queries, s notify.Sink, secrets []notify.SealedSecret) error {
	scope := s.Scope.String()

	rows, err := q.ListNotificationSinkSecrets(ctx, db.ListNotificationSinkSecretsParams{Namespace: scope, SinkID: s.ID})
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

	for _, ref := range change.Drop {
		if err = q.DeleteNotificationSinkSecret(ctx, db.DeleteNotificationSinkSecretParams{
			Ref: ref, SinkID: s.ID, Namespace: scope,
		}); err != nil {
			return fmt.Errorf("delete sink secret: %w", err)
		}
	}

	for _, ref := range change.Claim {
		n, claimErr := q.ClaimNotificationSinkRef(ctx, db.ClaimNotificationSinkRefParams{Namespace: scope, Ref: ref})
		if claimErr != nil {
			return fmt.Errorf("claim sink secret ref: %w", claimErr)
		}

		// A ref names one value for good: the namespace held this ref before.
		if n == 0 {
			return fmt.Errorf("%w: the secret ref %s of sink %s was used before", notify.ErrInvalid, ref, s.ID)
		}
	}

	for i, ref := range s.SecretRefs {
		if err = writeSinkSecret(ctx, q, s, i, ref, secrets); err != nil {
			return err
		}
	}

	return nil
}

// writeSinkSecret stores the new item of ref, or moves the stored item of ref
// to position i.
func writeSinkSecret(
	ctx context.Context, q *db.Queries, s notify.Sink, i int, ref string, secrets []notify.SealedSecret,
) error {
	scope := s.Scope.String()

	at := slices.IndexFunc(secrets, func(sec notify.SealedSecret) bool { return sec.Ref() == ref })
	if at < 0 {
		if err := q.MoveNotificationSinkSecret(ctx, db.MoveNotificationSinkSecretParams{
			Position: int64(i), Ref: ref, SinkID: s.ID, Namespace: scope,
		}); err != nil {
			return fmt.Errorf("move sink secret: %w", err)
		}

		return nil
	}

	if err := q.InsertNotificationSinkSecret(ctx, db.InsertNotificationSinkSecretParams{
		Namespace: scope, Ref: ref, SinkID: s.ID, Position: int64(i), Ciphertext: secrets[at].Ciphertext(),
	}); err != nil {
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

		if err = q.DeleteNotificationSinkSecrets(ctx, db.DeleteNotificationSinkSecretsParams{
			SinkID: id, Namespace: scope.String(),
		}); err != nil {
			return fmt.Errorf("delete sink secrets: %w", err)
		}

		if _, err = q.DeleteNotificationSink(ctx, db.DeleteNotificationSinkParams{ID: id, Namespace: scope.String()}); err != nil {
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
		// A row that fails its checks is not served.
		if sinkErr != nil || s.Scope != scope {
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
