package postgres

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/store/internal/notifystore"
	"opendustpan.org/opendustpan/pkg/store/postgres/internal/db"
)

// Notifications returns the store of the outbox, the sinks, the subscriptions
// and the deliveries. Each write checks the installation state and the fence
// of its scope in its transaction. A write that appends an audit entry appends
// it in the same transaction. The store keeps the rows of namespaces and the
// rows of audit.ServerScope.
func (s *Store) Notifications() NotificationStore {
	return NotificationStore{s: s}
}

// NotificationStore implements notify.Outbox, notify.Subscriptions,
// notify.Deliveries and notify.SinkStore over one database.
type NotificationStore struct {
	s *Store
}

var (
	_ notify.Outbox        = NotificationStore{s: nil}
	_ notify.Subscriptions = NotificationStore{s: nil}
	_ notify.Deliveries    = NotificationStore{s: nil}
	_ notify.SinkStore     = NotificationStore{s: nil}
)

// scopedWrite names the row of a write: how to find its scope, and the error
// for a fence that belongs to another scope. e is the audit entry that the
// write appends, or nil.
type scopedWrite struct {
	scope    func(ctx context.Context, q *db.Queries) (namespace.Name, error)
	mismatch error
	// label names a stored row for the text of notify.ErrNotFound. A fence of
	// another scope answers with the same text as an unknown row, so a caller
	// learns nothing of the rows of another scope.
	label string
	entry *audit.Entry
}

// known returns the scope function of a row whose scope the caller gives.
func known(scope namespace.Name) func(context.Context, *db.Queries) (namespace.Name, error) {
	return func(context.Context, *db.Queries) (namespace.Name, error) { return scope, nil }
}

// inScopedTx runs fn in a write transaction for a row of the scope that w
// names. The checks run in this order: the audit entry, the scope of the row,
// the fence against that scope (w.mismatch), the failover version of the
// fence (namespace.ErrFenced), the scope of the audit entry (ErrInvalid), and
// then fn, then the append of the audit entry. A refused write changes
// nothing. A fence for the scope audit.ServerScope is the zero fence.
//
// Defense in depth: the caller checks its own input too, and the audit append
// compares the entry with the fence again. This function stays the one place
// that compares the fence with the scope of the row, so no write can skip it.
func (s *Store) inScopedTx(
	ctx context.Context, fence namespace.Fence, w scopedWrite, fn func(q *db.Queries, scope namespace.Name) error,
) error {
	if w.entry != nil {
		if err := w.entry.Validate(); err != nil {
			return fmt.Errorf("audit entry: %w", err)
		}
	}

	return s.inTx(ctx, func(q *db.Queries) error {
		scope, err := w.scope(ctx, q)
		if err != nil {
			return err
		}

		if checkErr := checkNotifyFence(ctx, q, fence, scope, w); checkErr != nil {
			return checkErr
		}

		if w.entry != nil && w.entry.Namespace != scope {
			return fmt.Errorf("%w: the audit entry is not in the scope %q", notify.ErrInvalid, scope)
		}

		if fnErr := fn(q, scope); fnErr != nil {
			return fnErr
		}

		return appendScopedEntry(ctx, q, w.entry)
	})
}

// appendScopedEntry appends e in the transaction of q. A nil entry appends
// nothing.
func appendScopedEntry(ctx context.Context, q *db.Queries, e *audit.Entry) error {
	if e == nil {
		return nil
	}

	if _, err := appendAuditEntry(ctx, q, *e); err != nil {
		return fmt.Errorf("append audit entry: %w", err)
	}

	return nil
}

// checkNotifyFence returns mismatch when fence does not belong to scope,
// namespace.ErrNotFound for an unknown namespace, and namespace.ErrFenced for
// a stale fence.
func checkNotifyFence(
	ctx context.Context, q *db.Queries, fence namespace.Fence, scope namespace.Name, w scopedWrite,
) error {
	if scope == audit.ServerScope {
		if fence.Namespace != "" || fence.FailoverVersion != 0 {
			return fenceMismatch(w)
		}

		return nil
	}

	if fence.Namespace == "" || fence.Namespace != scope {
		return fenceMismatch(w)
	}

	version, err := q.GetFailoverVersionForShare(ctx, fence.Namespace.String())
	if errors.Is(err, pgx.ErrNoRows) {
		return namespace.ErrNotFound
	}

	if err != nil {
		return fmt.Errorf("read failover version: %w", err)
	}

	if version != fence.FailoverVersion {
		return namespace.ErrFenced
	}

	return nil
}

// fenceMismatch returns the error of a fence that does not belong to the scope
// of the row. For a stored row that the caller names by its ID it is the error
// of an unknown row, with the same text, and it never names the scope.
func fenceMismatch(w scopedWrite) error {
	if errors.Is(w.mismatch, notify.ErrNotFound) {
		return fmt.Errorf("%s: %w", w.label, notify.ErrNotFound)
	}

	return fmt.Errorf("%w: the fence does not belong to the scope of the row", w.mismatch)
}

// commonScope returns the scope that all of scopes have, and notify.ErrInvalid
// when there are two.
func commonScope(scopes []namespace.Name) (namespace.Name, error) {
	for _, sc := range scopes {
		if sc != scopes[0] {
			return "", fmt.Errorf("%w: rows of two scopes", notify.ErrInvalid)
		}
	}

	return scopes[0], nil
}

// Append implements notify.Outbox.
func (k NotificationStore) Append(ctx context.Context, fence namespace.Fence, ns []notify.Notification) error {
	if len(ns) == 0 {
		return nil
	}

	scopes := make([]namespace.Name, 0, len(ns))

	for i := range ns {
		if err := ns[i].Validate(); err != nil {
			return fmt.Errorf("append: %w", err)
		}

		scopes = append(scopes, ns[i].Scope)
	}

	scope, err := commonScope(scopes)
	if err != nil {
		return err
	}

	return k.s.inScopedTx(
		ctx,
		fence,
		scopedWrite{scope: known(scope), mismatch: notify.ErrInvalid, label: "", entry: nil},
		func(q *db.Queries, scope namespace.Name) error { return appendNotifications(ctx, q, scope, ns) },
	)
}

// appendNotifications appends ns to the outbox of scope, each with the next
// sequence of the scope, in the transaction of q. A domain store calls it in
// the transaction of its change. A notification with the scope and dedupe key
// of a stored one, or of an earlier one of the call, is skipped and takes no
// sequence. An ID that exists is notify.ErrInvalid.
//
// The row of the sequence counter orders the appends of a scope, and the write
// transaction holds it until the commit, so a later sequence never becomes
// visible before an earlier one.
func appendNotifications(ctx context.Context, q *db.Queries, scope namespace.Name, ns []notify.Notification) error {
	if len(ns) == 0 {
		return nil
	}

	if err := q.EnsureNotificationSequence(ctx, scope.String()); err != nil {
		return fmt.Errorf("make notification sequence: %w", err)
	}

	last, err := q.GetNotificationSequence(ctx, scope.String())
	if err != nil {
		return fmt.Errorf("read notification sequence: %w", err)
	}

	seenKeys := map[string]bool{}
	seenIDs := map[string]bool{}

	for i := range ns {
		n := ns[i]

		if n.Scope != scope {
			return fmt.Errorf("%w: notification %s is not in the scope %q", notify.ErrInvalid, n.ID, scope)
		}

		skip, checkErr := skipNotification(ctx, q, n, seenKeys, seenIDs)
		if checkErr != nil {
			return checkErr
		}

		if skip {
			continue
		}

		last++

		row, encErr := notifystore.EncodeNotification(n, last)
		if encErr != nil {
			return encErr
		}

		insErr := q.InsertNotification(ctx, db.InsertNotificationParams{
			Scope: row.Scope, Sequence: row.Sequence, ID: row.ID, Type: row.Type, Object: row.Object,
			ObjectID: row.ObjectID, Subject: row.Subject, Agent: row.Agent, Severity: row.Severity,
			Summary: row.Summary, Links: row.Links, Attributes: row.Attributes, Recipients: row.Recipients,
			DedupeKey: row.DedupeKey, AtMs: row.AtMs,
		})
		if insErr != nil {
			return fmt.Errorf("insert notification: %w", insErr)
		}
	}

	setErr := q.SetNotificationSequence(ctx, db.SetNotificationSequenceParams{
		LastSequence: last, Scope: scope.String(),
	})
	if setErr != nil {
		return fmt.Errorf("write notification sequence: %w", setErr)
	}

	return nil
}

// skipNotification reports whether the append skips n, because a stored
// notification or an earlier one of the call has its scope and dedupe key. It
// returns notify.ErrInvalid for an ID that a stored notification or an earlier
// one of the call has. It records n in seenKeys and seenIDs when it is added.
func skipNotification(
	ctx context.Context, q *db.Queries, n notify.Notification, seenKeys, seenIDs map[string]bool,
) (bool, error) {
	if seenKeys[n.DedupeKey] {
		return true, nil
	}

	_, err := q.GetNotificationIDByDedupe(ctx, db.GetNotificationIDByDedupeParams{
		Scope: n.Scope.String(), DedupeKey: n.DedupeKey,
	})

	switch {
	case err == nil:
		return true, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return false, fmt.Errorf("select notification by dedupe key: %w", err)
	default:
		// No stored notification tells this fact.
	}

	count, err := q.CountNotificationsByID(ctx, n.ID)
	if err != nil {
		return false, fmt.Errorf("count notifications by ID: %w", err)
	}

	if count > 0 || seenIDs[n.ID] {
		return false, fmt.Errorf("append %s: %w: the ID exists", n.ID, notify.ErrInvalid)
	}

	seenKeys[n.DedupeKey], seenIDs[n.ID] = true, true

	return false, nil
}

// Scopes implements notify.Outbox.
func (k NotificationStore) Scopes(ctx context.Context, name string) ([]namespace.Name, error) {
	rows, err := k.s.queries.ListNotificationScopesAbove(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("select notification scopes: %w", err)
	}

	out := make([]namespace.Name, 0, len(rows))
	for _, r := range rows {
		out = append(out, namespace.Name(r))
	}

	return out, nil
}

// After implements notify.Outbox. A write transaction orders the appends of a
// scope, and the commit makes a sequence visible only with every sequence
// before it, so After never skips one.
func (k NotificationStore) After(
	ctx context.Context, scope namespace.Name, seq int64, limit int,
) ([]notify.Notification, error) {
	if limit <= 0 {
		return nil, nil
	}

	rows, err := k.s.queries.ListNotificationsAfter(ctx, db.ListNotificationsAfterParams{
		Scope: scope.String(), After: seq, MaxRows: rowLimit(limit),
	})
	if err != nil {
		return nil, fmt.Errorf("select notifications: %w", err)
	}

	out := make([]notify.Notification, 0, len(rows))

	for i := range rows {
		r := &rows[i]

		n, convErr := notifystore.Notification(notifystore.NotificationRow{
			Scope: r.Scope, Sequence: r.Sequence, ID: r.ID, Type: r.Type, Object: r.Object, ObjectID: r.ObjectID,
			Subject: r.Subject, Agent: r.Agent, Severity: r.Severity, Summary: r.Summary, Links: r.Links,
			Attributes: r.Attributes, Recipients: r.Recipients, DedupeKey: r.DedupeKey, AtMs: r.AtMs,
		})
		if convErr != nil {
			return nil, fmt.Errorf("notification %s: %w", r.ID, convErr)
		}

		out = append(out, n)
	}

	return out, nil
}

// Cursor implements notify.Outbox.
func (k NotificationStore) Cursor(ctx context.Context, name string, scope namespace.Name) (int64, error) {
	seq, err := k.s.queries.GetNotificationCursor(
		ctx,
		db.GetNotificationCursorParams{Name: name, Scope: scope.String()},
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}

	if err != nil {
		return 0, fmt.Errorf("select notification cursor: %w", err)
	}

	return seq, nil
}

// MoveCursor implements notify.Outbox.
func (k NotificationStore) MoveCursor(ctx context.Context, fence namespace.Fence, m notify.CursorMove) error {
	if m.Name == "" || m.Scope == "" {
		return fmt.Errorf("%w: a cursor needs a name and a scope", notify.ErrInvalid)
	}

	return k.s.inScopedTx(
		ctx,
		fence,
		scopedWrite{scope: known(m.Scope), mismatch: notify.ErrInvalid, label: "", entry: nil},
		func(q *db.Queries, _ namespace.Name) error {
			err := q.EnsureNotificationCursor(ctx, db.EnsureNotificationCursorParams{
				Name: m.Name, Scope: m.Scope.String(),
			})
			if err != nil {
				return fmt.Errorf("make notification cursor: %w", err)
			}

			n, err := q.MoveNotificationCursor(ctx, db.MoveNotificationCursorParams{
				ToSequence: m.To, Name: m.Name, Scope: m.Scope.String(), FromSequence: m.From,
			})
			if err != nil {
				return fmt.Errorf("move notification cursor: %w", err)
			}

			if n == 0 {
				return fmt.Errorf("move cursor %s: %w", m.Name, notify.ErrConflict)
			}

			return nil
		},
	)
}

// sinkScope returns the scope function of the stored sink id. An unknown sink
// is notify.ErrNotFound.
func sinkScope(id, what string) func(context.Context, *db.Queries) (namespace.Name, error) {
	return func(ctx context.Context, q *db.Queries) (namespace.Name, error) {
		row, err := q.GetNotificationSink(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("%s %s: %w", what, id, notify.ErrNotFound)
		}

		if err != nil {
			return "", fmt.Errorf("select sink: %w", err)
		}

		return namespace.Name(row.Namespace), nil
	}
}

// PutSink implements notify.Subscriptions.
func (k NotificationStore) PutSink(
	ctx context.Context, s notify.Sink, secrets []notify.SealedSecret, fence namespace.Fence, e audit.Entry,
) error {
	if err := s.ValidateWrite(); err != nil {
		return fmt.Errorf("put sink: %w", err)
	}

	if err := s.ValidateSealed(secrets); err != nil {
		return fmt.Errorf("put sink: %w", err)
	}

	w := scopedWrite{scope: known(s.Scope), mismatch: notify.ErrInvalid, label: "", entry: &e}

	return k.s.inScopedTx(ctx, fence, w, func(q *db.Queries, _ namespace.Name) error {
		changed, err := sinkDestinationChanged(ctx, q, s)
		if err != nil {
			return err
		}

		stored, err := putSinkRow(ctx, q, s)
		if err != nil {
			return err
		}

		if secretsErr := putSinkSecrets(ctx, q, s, secrets); secretsErr != nil {
			return secretsErr
		}

		if changed {
			suspendErr := suspendSubscriptionsOf(
				ctx,
				q,
				sinkChange{scope: s.Scope, sink: s.ID, version: stored.Version},
				e,
			)
			if suspendErr != nil {
				return suspendErr
			}
		}

		return finishCompletion(ctx, q, stored)
	})
}

// sinkDestinationChanged reports whether the write of s sends to another place
// than the stored sink: a new target or other secret refs. The kind cannot
// change. A new sink changes nothing.
func sinkDestinationChanged(ctx context.Context, q *db.Queries, s notify.Sink) (bool, error) {
	old, err := q.GetNotificationSink(ctx, s.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("select sink: %w", err)
	}

	// A sink of another scope is notify.ErrNotFound at the write.
	if old.Namespace != s.Scope.String() {
		return false, nil
	}

	rows, err := q.ListNotificationSinkSecrets(ctx, db.ListNotificationSinkSecretsParams{
		Namespace: old.Namespace, SinkID: s.ID,
	})
	if err != nil {
		return false, fmt.Errorf("select sink secrets: %w", err)
	}

	refs := make([]string, 0, len(rows))
	for _, r := range rows {
		refs = append(refs, r.Ref)
	}

	return old.Target != s.Target || !slices.Equal(refs, s.SecretRefs), nil
}

// sinkChange names the sink whose destination a write changed, and the version
// of the sink that the write stores.
type sinkChange struct {
	scope   namespace.Name
	sink    string
	version int64
}

// suspendSubscriptionsOf suspends each active subscription of the sink id with
// the reason notify.ReasonSinkChanged and increments its version, so a put
// that read the subscription before the change gets notify.ErrConflict. It
// appends one notice for each subscription that it suspends, and skips each
// pending delivery of each subscription of the sink, so a delivery that was
// routed to the old place is never sent to the new one.
func suspendSubscriptionsOf(ctx context.Context, q *db.Queries, c sinkChange, e audit.Entry) error {
	scope, id, version := c.scope, c.sink, c.version

	rows, err := q.ListNotificationSubscriptionsOfSink(ctx, db.ListNotificationSubscriptionsOfSinkParams{
		Scope: scope.String(), SinkID: id,
	})
	if err != nil {
		return fmt.Errorf("select subscriptions of sink: %w", err)
	}

	subs, err := subscriptionsOf(rows)
	if err != nil {
		return err
	}

	var notices []notify.Notification

	for i := range subs {
		if subs[i].State == notify.SubscriptionStateActive {
			notices = append(notices, notify.SinkChangedNotice(subs[i], version, e))
		}
	}

	if appendErr := appendNotifications(ctx, q, scope, notices); appendErr != nil {
		return appendErr
	}

	for i := range subs {
		if subs[i].State == notify.SubscriptionStateActive {
			susErr := q.SuspendNotificationSubscription(ctx, db.SuspendNotificationSubscriptionParams{
				Reason: notify.ReasonSinkChanged, ID: subs[i].ID, Scope: scope.String(),
			})
			if susErr != nil {
				return fmt.Errorf("suspend subscription: %w", susErr)
			}
		}

		skipErr := q.SkipPendingNotificationDeliveriesOfSubscription(
			ctx,
			db.SkipPendingNotificationDeliveriesOfSubscriptionParams{Scope: scope.String(), SubscriptionID: subs[i].ID},
		)
		if skipErr != nil {
			return fmt.Errorf("skip pending deliveries: %w", skipErr)
		}
	}

	return nil
}

// subscriptionsOf converts stored rows and checks each one again.
func subscriptionsOf(rows []db.NotificationSubscription) ([]notify.Subscription, error) {
	out := make([]notify.Subscription, 0, len(rows))

	for i := range rows {
		r := &rows[i]

		s, err := notifystore.Subscription(notifystore.SubscriptionRow{
			ID: r.ID, Scope: r.Scope, SinkID: r.SinkID, Types: r.Types, Objects: r.Objects,
			MinSeverity: r.MinSeverity, State: r.State, Reason: r.Reason, CreatedBy: r.CreatedBy, Version: r.Version,
		})
		if err != nil {
			return nil, fmt.Errorf("subscription %s: %w", r.ID, err)
		}

		out = append(out, s)
	}

	return out, nil
}

// DeleteSink implements notify.Subscriptions. While a subscription names the
// sink it returns notify.ErrInUse.
func (k NotificationStore) DeleteSink(ctx context.Context, id string, fence namespace.Fence, e audit.Entry) error {
	w := scopedWrite{scope: sinkScope(id, "sink"), mismatch: notify.ErrNotFound, label: "sink " + id, entry: &e}

	return k.s.inScopedTx(ctx, fence, w, func(q *db.Queries, scope namespace.Name) error {
		users, err := q.ListNotificationSubscriptionsOfSink(ctx, db.ListNotificationSubscriptionsOfSinkParams{
			Scope: scope.String(), SinkID: id,
		})
		if err != nil {
			return fmt.Errorf("select subscriptions of sink: %w", err)
		}

		if len(users) > 0 {
			return fmt.Errorf("delete sink %s: %w", id, notify.ErrInUse)
		}

		delErr := q.DeleteNotificationSinkSecrets(ctx, db.DeleteNotificationSinkSecretsParams{
			SinkID: id, Namespace: scope.String(),
		})
		if delErr != nil {
			return fmt.Errorf("delete sink secrets: %w", delErr)
		}

		_, delErr = q.DeleteNotificationSink(ctx, db.DeleteNotificationSinkParams{
			ID: id, Namespace: scope.String(),
		})
		if delErr != nil {
			return fmt.Errorf("delete sink: %w", delErr)
		}

		return finishCompletion(ctx, q, id)
	})
}

// RecordSinkTest implements notify.SinkStore.
func (k NotificationStore) RecordSinkTest(ctx context.Context, id string, fence namespace.Fence, e audit.Entry) error {
	w := scopedWrite{
		scope: sinkScope(id, "record test of sink"), mismatch: notify.ErrNotFound, label: "record test of sink " + id,
		entry: &e,
	}

	return k.s.inScopedTx(ctx, fence, w, func(q *db.Queries, _ namespace.Name) error {
		return finishCompletion(ctx, q, id)
	})
}

// PutSubscription implements notify.Subscriptions.
func (k NotificationStore) PutSubscription(
	ctx context.Context, s notify.Subscription, fence namespace.Fence, e audit.Entry,
) error {
	// The subject of the write is the creator, so each delivery uses the
	// access of the person who last approved the subscription.
	s.CreatedBy = e.Subject

	if err := s.Validate(); err != nil {
		return fmt.Errorf("put subscription: %w", err)
	}

	filter, err := notifystore.EncodeFilter(s.Filter)
	if err != nil {
		return err
	}

	w := scopedWrite{scope: known(s.Scope), mismatch: notify.ErrInvalid, label: "", entry: &e}

	return k.s.inScopedTx(ctx, fence, w, func(q *db.Queries, _ namespace.Name) error {
		exists, checkErr := checkSubscriptionWrite(ctx, q, s)
		if checkErr != nil {
			return checkErr
		}

		var writeErr error
		if exists {
			writeErr = updateSubscription(ctx, q, s, filter)
		} else {
			writeErr = insertSubscription(ctx, q, s, filter)
		}

		if writeErr != nil {
			return writeErr
		}

		stored := s
		stored.Version++

		return finishCompletion(ctx, q, stored)
	})
}

// checkSubscriptionWrite checks the write of s against the stored sink and the
// stored subscription. It reports whether the subscription exists. It returns
// notify.ErrNotFound for a sink that is not in the scope of s and for a stored
// subscription of another scope, and notify.ErrConflict when s.Version is not
// the stored version (0 for a new subscription).
func checkSubscriptionWrite(ctx context.Context, q *db.Queries, s notify.Subscription) (bool, error) {
	sink, err := q.GetNotificationSink(ctx, s.Sink)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && sink.Namespace != s.Scope.String() {
		return false, fmt.Errorf("put subscription %s: sink %s: %w", s.ID, s.Sink, notify.ErrNotFound)
	}

	if err != nil {
		return false, fmt.Errorf("select sink: %w", err)
	}

	old, err := q.GetNotificationSubscription(ctx, s.ID)

	var version int64

	exists := false

	switch {
	case err == nil && old.Scope != s.Scope.String():
		return false, fmt.Errorf("put subscription %s: %w", s.ID, notify.ErrNotFound)
	case err == nil:
		version, exists = old.Version, true
	case !errors.Is(err, pgx.ErrNoRows):
		return false, fmt.Errorf("select subscription: %w", err)
	default:
		// The subscription is new.
	}

	if version != s.Version {
		return false, fmt.Errorf("put subscription %s: %w", s.ID, notify.ErrConflict)
	}

	return exists, nil
}

// insertSubscription writes a new subscription. It returns notify.ErrConflict
// when another write made the subscription since.
func insertSubscription(ctx context.Context, q *db.Queries, s notify.Subscription, f notifystore.Filter) error {
	n, err := q.InsertNotificationSubscription(ctx, db.InsertNotificationSubscriptionParams{
		ID: s.ID, Scope: s.Scope.String(), SinkID: s.Sink, Types: f.Types, Objects: f.Objects,
		MinSeverity: int64(s.Filter.MinSeverity), State: string(s.State), Reason: s.Reason,
		CreatedBy: s.CreatedBy,
	})
	if err != nil {
		return fmt.Errorf("insert subscription: %w", err)
	}

	if n == 0 {
		return fmt.Errorf("put subscription %s: %w", s.ID, notify.ErrConflict)
	}

	return nil
}

// updateSubscription replaces the stored subscription at the version of s. It
// returns notify.ErrConflict when another write changed it since.
func updateSubscription(ctx context.Context, q *db.Queries, s notify.Subscription, f notifystore.Filter) error {
	n, err := q.UpdateNotificationSubscription(ctx, db.UpdateNotificationSubscriptionParams{
		SinkID: s.Sink, Types: f.Types, Objects: f.Objects, MinSeverity: int64(s.Filter.MinSeverity),
		State: string(s.State), Reason: s.Reason, CreatedBy: s.CreatedBy, ID: s.ID, Scope: s.Scope.String(),
		Version: s.Version,
	})
	if err != nil {
		return fmt.Errorf("update subscription: %w", err)
	}

	if n == 0 {
		return fmt.Errorf("put subscription %s: %w", s.ID, notify.ErrConflict)
	}

	return nil
}

// DeleteSubscription implements notify.Subscriptions.
func (k NotificationStore) DeleteSubscription(
	ctx context.Context, id string, fence namespace.Fence, e audit.Entry,
) error {
	scope := func(ctx context.Context, q *db.Queries) (namespace.Name, error) {
		row, err := q.GetNotificationSubscription(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("subscription %s: %w", id, notify.ErrNotFound)
		}

		if err != nil {
			return "", fmt.Errorf("select subscription: %w", err)
		}

		return namespace.Name(row.Scope), nil
	}

	w := scopedWrite{scope: scope, mismatch: notify.ErrNotFound, label: "subscription " + id, entry: &e}

	return k.s.inScopedTx(ctx, fence, w, func(q *db.Queries, scope namespace.Name) error {
		err := q.DeletePendingNotificationDeliveriesOfSubscription(
			ctx, db.DeletePendingNotificationDeliveriesOfSubscriptionParams{Scope: scope.String(), SubscriptionID: id},
		)
		if err != nil {
			return fmt.Errorf("delete pending deliveries: %w", err)
		}

		_, err = q.DeleteNotificationSubscription(ctx, db.DeleteNotificationSubscriptionParams{
			ID: id, Scope: scope.String(),
		})
		if err != nil {
			return fmt.Errorf("delete subscription: %w", err)
		}

		return finishCompletion(ctx, q, id)
	})
}

// Subscriptions implements notify.Subscriptions.
func (k NotificationStore) Subscriptions(ctx context.Context, scope namespace.Name) ([]notify.Subscription, error) {
	rows, err := k.s.queries.ListNotificationSubscriptions(ctx, scope.String())
	if err != nil {
		return nil, fmt.Errorf("select subscriptions: %w", err)
	}

	return subscriptionsOf(rows)
}

// Enqueue implements notify.Deliveries.
func (k NotificationStore) Enqueue(ctx context.Context, fence namespace.Fence, ds []notify.Delivery) error {
	if len(ds) == 0 {
		return nil
	}

	scopes := make([]namespace.Name, 0, len(ds))

	for i := range ds {
		if err := ds[i].Validate(); err != nil {
			return fmt.Errorf("enqueue: %w", err)
		}

		if ds[i].State != notify.DeliveryPending || ds[i].Attempts != 0 {
			return fmt.Errorf("enqueue %s: %w: a new delivery is pending with no attempts", ds[i].ID, notify.ErrInvalid)
		}

		scopes = append(scopes, ds[i].Scope)
	}

	scope, err := commonScope(scopes)
	if err != nil {
		return err
	}

	return k.s.inScopedTx(
		ctx,
		fence,
		scopedWrite{scope: known(scope), mismatch: notify.ErrInvalid, label: "", entry: nil},
		func(q *db.Queries, _ namespace.Name) error { return enqueueDeliveries(ctx, q, ds) },
	)
}

// enqueueDeliveries stores ds as pending. A delivery for the route of a stored
// one, or of an earlier one of the call, is skipped. An ID that exists in the
// scope is notify.ErrInvalid, and an ID that exists in another scope is
// notify.ErrNotFound, so it never replaces a delivery.
func enqueueDeliveries(ctx context.Context, q *db.Queries, ds []notify.Delivery) error {
	seen := enqueued{routes: map[string]bool{}, ids: map[string]bool{}}

	for i := range ds {
		if err := enqueueDelivery(ctx, q, &ds[i], seen); err != nil {
			return err
		}
	}

	return nil
}

// enqueued holds the routes and the IDs that a call of Enqueue stored so far.
type enqueued struct {
	routes map[string]bool
	ids    map[string]bool
}

// enqueueDelivery stores d as pending, or skips it when its route exists.
func enqueueDelivery(ctx context.Context, q *db.Queries, d *notify.Delivery, seen enqueued) error {
	if err := checkDeliveryReferences(ctx, q, d); err != nil {
		return err
	}

	count, err := q.CountNotificationDeliveryRoute(ctx, db.CountNotificationDeliveryRouteParams{
		Scope: d.Scope.String(), NotificationID: d.Notification, SubscriptionID: d.Subscription, Contact: d.Contact,
	})
	if err != nil {
		return fmt.Errorf("count delivery route: %w", err)
	}

	key := strings.Join([]string{d.Notification, d.Subscription, d.Contact}, "\x00")
	if count > 0 || seen.routes[key] {
		return nil
	}

	if idErr := checkDeliveryID(ctx, q, d, seen.ids); idErr != nil {
		return idErr
	}

	insErr := q.InsertNotificationDelivery(ctx, db.InsertNotificationDeliveryParams{
		ID: d.ID, Scope: d.Scope.String(), NotificationID: d.Notification, SubscriptionID: d.Subscription,
		SinkID: d.Sink, Contact: d.Contact, Principal: d.Principal, NextAtMs: notifystore.Millis(d.NextAt),
	})
	if insErr != nil {
		return fmt.Errorf("insert delivery: %w", insErr)
	}

	seen.routes[key], seen.ids[d.ID] = true, true

	return nil
}

// checkDeliveryID returns notify.ErrInvalid for the ID of a stored delivery of
// the scope of d, or of an earlier delivery of the call, and notify.ErrNotFound
// for the ID of a stored delivery of another scope.
func checkDeliveryID(ctx context.Context, q *db.Queries, d *notify.Delivery, ids map[string]bool) error {
	old, err := q.GetNotificationDelivery(ctx, d.ID)

	switch {
	case err == nil && old.Scope != d.Scope.String():
		return fmt.Errorf("enqueue %s: %w", d.ID, notify.ErrNotFound)
	case err == nil || ids[d.ID]:
		return fmt.Errorf("enqueue %s: %w: the ID exists", d.ID, notify.ErrInvalid)
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("select delivery: %w", err)
	default:
		return nil
	}
}

// checkDeliveryReferences returns notify.ErrNotFound when d names a sink, a
// subscription or a notification that a stored row of another scope has. A
// reference to a row that the store does not hold is allowed: a retention job
// deletes old notifications, and a person deletes subscriptions, while their
// deliveries stay as history. The message never names the other scope.
//
// Defense in depth: the dispatcher builds a delivery from the rows of one scope.
// This check stays so that a delivery of one scope never points at a sink, a
// subscription or a notification of another scope, whatever the caller does.
func checkDeliveryReferences(ctx context.Context, q *db.Queries, d *notify.Delivery) error {
	if err := checkSinkReference(ctx, q, d); err != nil {
		return err
	}

	if err := checkSubscriptionReference(ctx, q, d); err != nil {
		return err
	}

	return checkNotificationReference(ctx, q, d)
}

func checkSinkReference(ctx context.Context, q *db.Queries, d *notify.Delivery) error {
	if d.Sink == "" {
		return nil
	}

	row, err := q.GetNotificationSink(ctx, d.Sink)

	switch {
	case err == nil && row.Namespace != d.Scope.String():
		return fmt.Errorf("enqueue %s: sink %s: %w", d.ID, d.Sink, notify.ErrNotFound)
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("select sink: %w", err)
	default:
		// The sink is in the scope, or the store does not hold it.
		return nil
	}
}

func checkSubscriptionReference(ctx context.Context, q *db.Queries, d *notify.Delivery) error {
	if d.Subscription == "" {
		return nil
	}

	row, err := q.GetNotificationSubscription(ctx, d.Subscription)

	switch {
	case err == nil && row.Scope != d.Scope.String():
		return fmt.Errorf("enqueue %s: subscription %s: %w", d.ID, d.Subscription, notify.ErrNotFound)
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("select subscription: %w", err)
	default:
		// The subscription is in the scope, or the store does not hold it.
		return nil
	}
}

func checkNotificationReference(ctx context.Context, q *db.Queries, d *notify.Delivery) error {
	owner, err := q.GetNotificationScopeByID(ctx, d.Notification)

	switch {
	case err == nil && owner != d.Scope.String():
		return fmt.Errorf("enqueue %s: notification %s: %w", d.ID, d.Notification, notify.ErrNotFound)
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("select notification: %w", err)
	default:
		// The notification is in the scope, or the store does not hold it.
		return nil
	}
}

// DueScopes implements notify.Deliveries.
func (k NotificationStore) DueScopes(ctx context.Context, now time.Time) ([]namespace.Name, error) {
	rows, err := k.s.queries.ListDueNotificationDeliveryScopes(ctx, now.UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("select due scopes: %w", err)
	}

	out := make([]namespace.Name, 0, len(rows))
	for _, r := range rows {
		out = append(out, namespace.Name(r))
	}

	return out, nil
}

// Claim implements notify.Deliveries.
func (k NotificationStore) Claim(
	ctx context.Context, fence namespace.Fence, req notify.ClaimRequest,
) ([]notify.Delivery, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("claim: %w", err)
	}

	var out []notify.Delivery

	err := k.s.inScopedTx(
		ctx,
		fence,
		scopedWrite{scope: known(req.Scope), mismatch: notify.ErrInvalid, label: "", entry: nil},
		func(q *db.Queries, _ namespace.Name) error {
			rows, err := q.ClaimNotificationDeliveries(ctx, db.ClaimNotificationDeliveriesParams{
				Owner: req.Owner, LeaseUntilMs: req.Now.Add(req.Lease).UnixMilli(), Scope: req.Scope.String(),
				NowMs: req.Now.UnixMilli(), MaxRows: rowLimit(req.Limit),
			})
			if err != nil {
				return fmt.Errorf("claim deliveries: %w", err)
			}

			out, err = deliveriesOf(rows)

			return err
		},
	)
	if err != nil {
		return nil, err
	}

	slices.SortFunc(out, func(a, b notify.Delivery) int {
		if c := a.NextAt.Compare(b.NextAt); c != 0 {
			return c
		}

		return strings.Compare(a.ID, b.ID)
	})

	return out, nil
}

// deliveriesOf converts stored rows and checks each one again.
func deliveriesOf(rows []db.NotificationDelivery) ([]notify.Delivery, error) {
	out := make([]notify.Delivery, 0, len(rows))

	for i := range rows {
		r := &rows[i]

		d, err := notifystore.Delivery(deliveryRow(r))
		if err != nil {
			return nil, fmt.Errorf("delivery %s: %w", r.ID, err)
		}

		out = append(out, d)
	}

	return out, nil
}

func deliveryRow(r *db.NotificationDelivery) notifystore.DeliveryRow {
	return notifystore.DeliveryRow{
		ID: r.ID, Scope: r.Scope, NotificationID: r.NotificationID, SubscriptionID: r.SubscriptionID,
		SinkID: r.SinkID, Contact: r.Contact, Principal: r.Principal, State: r.State, Attempts: r.Attempts,
		NextAtMs: r.NextAtMs, LastError: r.LastError, DeliveredAtMs: r.DeliveredAtMs, Owner: r.Owner,
		LeaseUntilMs: r.LeaseUntilMs, Version: r.Version,
	}
}

// deliveryScope returns the scope function of the stored delivery id. An
// unknown delivery is notify.ErrNotFound.
func deliveryScope(id string) func(context.Context, *db.Queries) (namespace.Name, error) {
	return func(ctx context.Context, q *db.Queries) (namespace.Name, error) {
		row, err := q.GetNotificationDelivery(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("delivery %s: %w", id, notify.ErrNotFound)
		}

		if err != nil {
			return "", fmt.Errorf("select delivery: %w", err)
		}

		return namespace.Name(row.Scope), nil
	}
}

// Record implements notify.Deliveries.
func (k NotificationStore) Record(ctx context.Context, fence namespace.Fence, a notify.Attempt) error {
	if err := a.Validate(); err != nil {
		return fmt.Errorf("record: %w", err)
	}

	w := scopedWrite{scope: deliveryScope(a.ID), mismatch: notify.ErrNotFound, label: "delivery " + a.ID, entry: nil}

	return k.s.inScopedTx(ctx, fence, w, func(q *db.Queries, scope namespace.Name) error {
		n, err := q.RecordNotificationAttempt(ctx, db.RecordNotificationAttemptParams{
			State: string(a.State), Attempts: int64(a.Attempts), NextAtMs: notifystore.Millis(a.NextAt),
			LastError: a.LastError, DeliveredAtMs: notifystore.Millis(a.DeliveredAt), ID: a.ID,
			Scope: scope.String(), Owner: a.Owner, Version: a.Version, NowMs: a.Now.UnixMilli(),
		})
		if err != nil {
			return fmt.Errorf("record attempt: %w", err)
		}

		if n == 0 {
			return fmt.Errorf("record %s: %w: no live claim", a.ID, notify.ErrConflict)
		}

		return nil
	})
}

// History implements notify.Deliveries.
func (k NotificationStore) History(
	ctx context.Context, scope namespace.Name, id string, after *notify.DeliveryCursor, limit int,
) ([]notify.Delivery, error) {
	var out []notify.Delivery

	// One snapshot, so the subscription and its deliveries come from one state.
	err := k.s.inSnapshotTx(ctx, func(q *db.Queries) error {
		sub, err := q.GetNotificationSubscription(ctx, id)

		switch {
		case err == nil && sub.Scope != scope.String():
			return fmt.Errorf("history of %s: %w", id, notify.ErrNotFound)
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("select subscription: %w", err)
		default:
			// The subscription is in the scope, or it was deleted and its history stays.
		}

		if limit <= 0 {
			return nil
		}

		var rows []db.NotificationDelivery

		if after == nil {
			rows, err = q.ListNotificationDeliveriesOfSubscription(ctx,
				db.ListNotificationDeliveriesOfSubscriptionParams{
					Scope: scope.String(), SubscriptionID: id, MaxRows: rowLimit(limit),
				})
		} else {
			rows, err = q.ListNotificationDeliveriesOfSubscriptionAfter(ctx,
				db.ListNotificationDeliveriesOfSubscriptionAfterParams{
					Scope: scope.String(), SubscriptionID: id,
					AfterMs: after.NextAt.UnixMilli(), AfterID: after.ID, MaxRows: rowLimit(limit),
				})
		}

		if err != nil {
			return fmt.Errorf("select deliveries: %w", err)
		}

		out, err = deliveriesOf(rows)

		return err
	})
	if err != nil {
		return nil, err
	}

	return out, nil
}

// CountBefore implements notify.Deliveries.
func (k NotificationStore) CountBefore(
	ctx context.Context, scope namespace.Name, id string, after *notify.DeliveryCursor, maxCount int,
) (int, error) {
	var count int64

	// One snapshot, so the subscription and its deliveries come from one state.
	err := k.s.inSnapshotTx(ctx, func(q *db.Queries) error {
		sub, err := q.GetNotificationSubscription(ctx, id)

		switch {
		case err == nil && sub.Scope != scope.String():
			return fmt.Errorf("count deliveries of %s: %w", id, notify.ErrNotFound)
		case err != nil && !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("select subscription: %w", err)
		default:
			// The subscription is in the scope, or it was deleted and its history stays.
		}

		if after == nil || maxCount <= 0 {
			return nil
		}

		count, err = q.CountNotificationDeliveriesOfSubscriptionBefore(ctx,
			db.CountNotificationDeliveriesOfSubscriptionBeforeParams{
				Scope: scope.String(), SubscriptionID: id,
				AfterMs: after.NextAt.UnixMilli(), AfterID: after.ID, MaxRows: rowLimit(maxCount),
			})
		if err != nil {
			return fmt.Errorf("count deliveries: %w", err)
		}

		return nil
	})
	if err != nil {
		return 0, err
	}

	return int(count), nil
}

// Redeliver implements notify.Deliveries.
func (k NotificationStore) Redeliver(ctx context.Context, id string, fence namespace.Fence, e audit.Entry) error {
	w := scopedWrite{scope: deliveryScope(id), mismatch: notify.ErrNotFound, label: "delivery " + id, entry: &e}

	return k.s.inScopedTx(ctx, fence, w, func(q *db.Queries, scope namespace.Name) error {
		row, err := q.GetNotificationDelivery(ctx, id)
		if err != nil {
			return fmt.Errorf("select delivery: %w", err)
		}

		d, err := notifystore.Delivery(deliveryRow(&row))
		if err != nil {
			return fmt.Errorf("delivery %s: %w", id, err)
		}

		if d.State == notify.DeliveryPending {
			return fmt.Errorf("redeliver %s: %w: the delivery is pending", id, notify.ErrConflict)
		}

		if sendErr := checkSubscriptionSends(ctx, q, d); sendErr != nil {
			return sendErr
		}

		n, err := q.RedeliverNotificationDelivery(ctx, db.RedeliverNotificationDeliveryParams{
			ID: id, Scope: scope.String(),
		})
		if err != nil {
			return fmt.Errorf("redeliver: %w", err)
		}

		if n == 0 {
			return fmt.Errorf("redeliver %s: %w: the delivery is pending", id, notify.ErrConflict)
		}

		return finishCompletion(ctx, q, id)
	})
}

// checkSubscriptionSends returns notify.ErrConflict when d belongs to a
// subscription that no longer sends it, so a delivery from before a change of
// the sink or of the subscription never goes to the new place.
func checkSubscriptionSends(ctx context.Context, q *db.Queries, d notify.Delivery) error {
	if d.Subscription == "" {
		return nil
	}

	var sub notify.Subscription

	row, err := q.GetNotificationSubscription(ctx, d.Subscription)

	switch {
	case err == nil:
		subs, convErr := subscriptionsOf([]db.NotificationSubscription{row})
		if convErr != nil {
			return convErr
		}

		sub = subs[0]
	case !errors.Is(err, pgx.ErrNoRows):
		return fmt.Errorf("select subscription: %w", err)
	default:
		// The subscription is gone, and the zero subscription sends nothing.
	}

	if !notify.SendsFor(sub, d) {
		return fmt.Errorf("redeliver %s: %w: the subscription no longer sends it", d.ID, notify.ErrConflict)
	}

	return nil
}
