// Package notifystore holds the rules of the notification tables that the
// SQLite and PostgreSQL adapters share: the text form of the lists of a
// notification and of a filter, and the check of a stored row. A row is
// untrusted when it is read, because a backup, a restore or another version of
// the server can have written it.
package notifystore

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
)

// ErrCorrupt reports a stored row that does not pass the checks of its model.
// The adapters do not serve such a row.
var ErrCorrupt = errors.New("stored notification data is not valid")

// link is the stored form of a notify.Link.
type link struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Kind  string `json:"kind"`
}

// recipient is the stored form of a notify.Recipient.
type recipient struct {
	Principal string `json:"principal,omitempty"`
	Group     string `json:"group,omitempty"`
	Admins    bool   `json:"admins,omitempty"`
}

// NotificationRow is a notification as the database holds it. Links,
// Attributes and Recipients are JSON text.
type NotificationRow struct {
	Scope      string
	Sequence   int64
	ID         string
	Type       string
	Object     string
	ObjectID   string
	Subject    string
	Agent      string
	Severity   int64
	Summary    string
	Links      string
	Attributes string
	Recipients string
	DedupeKey  string
	AtMs       int64
}

// EncodeNotification returns the row of n for the sequence seq.
func EncodeNotification(n notify.Notification, seq int64) (NotificationRow, error) {
	links := make([]link, 0, len(n.Links))
	for _, l := range n.Links {
		links = append(links, link{Title: l.Title, URL: l.URL, Kind: string(l.Kind)})
	}

	recipients := make([]recipient, 0, len(n.Recipients))
	for _, r := range n.Recipients {
		recipients = append(recipients, recipient{Principal: r.Principal, Group: r.Group, Admins: r.Admins})
	}

	attrs := n.Attributes
	if attrs == nil {
		attrs = map[string]string{}
	}

	l, err := json.Marshal(links)
	if err != nil {
		return NotificationRow{}, fmt.Errorf("encode links: %w", err)
	}

	a, err := json.Marshal(attrs)
	if err != nil {
		return NotificationRow{}, fmt.Errorf("encode attributes: %w", err)
	}

	r, err := json.Marshal(recipients)
	if err != nil {
		return NotificationRow{}, fmt.Errorf("encode recipients: %w", err)
	}

	return NotificationRow{
		Scope: n.Scope.String(), Sequence: seq, ID: n.ID, Type: string(n.Type), Object: n.Object,
		ObjectID: n.ObjectID, Subject: n.Subject, Agent: n.Agent, Severity: int64(n.Severity), Summary: n.Summary,
		Links: string(l), Attributes: string(a), Recipients: string(r), DedupeKey: n.DedupeKey,
		AtMs: n.At.UnixMilli(),
	}, nil
}

// Notification builds the notification of a stored row and checks it again
// with notify.Notification.Validate. It returns ErrCorrupt for a row that
// fails.
func Notification(r NotificationRow) (notify.Notification, error) {
	var (
		links      []link
		attrs      map[string]string
		recipients []recipient
	)

	for _, part := range []struct {
		text string
		into any
	}{{r.Links, &links}, {r.Attributes, &attrs}, {r.Recipients, &recipients}} {
		if err := json.Unmarshal([]byte(part.text), part.into); err != nil {
			return notify.Notification{}, fmt.Errorf("%w: notification %s: a list does not decode", ErrCorrupt, r.ID)
		}
	}

	n := notify.Notification{
		ID: r.ID, Type: notify.Type(r.Type), Scope: namespace.Name(r.Scope), Object: r.Object, ObjectID: r.ObjectID,
		Subject: r.Subject, Agent: r.Agent, Severity: notify.Severity(r.Severity), Summary: r.Summary,
		Links: nil, Attributes: attrs, Recipients: nil, DedupeKey: r.DedupeKey,
		At: time.UnixMilli(r.AtMs).UTC(), Sequence: r.Sequence,
	}

	for _, l := range links {
		n.Links = append(n.Links, notify.Link{Title: l.Title, URL: l.URL, Kind: notify.LinkKind(l.Kind)})
	}

	for _, x := range recipients {
		n.Recipients = append(n.Recipients, notify.Recipient{Principal: x.Principal, Group: x.Group, Admins: x.Admins})
	}

	if len(attrs) == 0 {
		n.Attributes = nil
	}

	if err := n.Validate(); err != nil {
		return notify.Notification{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	return n, nil
}

// SubscriptionRow is a subscription as the database holds it. Types and
// Objects are JSON lists of text.
type SubscriptionRow struct {
	ID          string
	Scope       string
	SinkID      string
	Types       string
	Objects     string
	MinSeverity int64
	State       string
	Reason      string
	CreatedBy   string
	Version     int64
}

// Filter is the text form of the type list and the object list of a
// notify.Filter.
type Filter struct {
	Types   string
	Objects string
}

// EncodeFilter returns the text form of the type list and the object list of
// f.
func EncodeFilter(f notify.Filter) (Filter, error) {
	t, err := json.Marshal(nonNil(f.Types))
	if err != nil {
		return Filter{}, fmt.Errorf("encode filter types: %w", err)
	}

	o, err := json.Marshal(nonNil(f.Objects))
	if err != nil {
		return Filter{}, fmt.Errorf("encode filter objects: %w", err)
	}

	return Filter{Types: string(t), Objects: string(o)}, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}

	return s
}

// Subscription builds the subscription of a stored row and checks it again
// with notify.Subscription.Validate. It returns ErrCorrupt for a row that
// fails.
func Subscription(r SubscriptionRow) (notify.Subscription, error) {
	var types, objects []string

	if err := json.Unmarshal([]byte(r.Types), &types); err != nil {
		return notify.Subscription{}, fmt.Errorf("%w: subscription %s: the types do not decode", ErrCorrupt, r.ID)
	}

	if err := json.Unmarshal([]byte(r.Objects), &objects); err != nil {
		return notify.Subscription{}, fmt.Errorf("%w: subscription %s: the objects do not decode", ErrCorrupt, r.ID)
	}

	if len(types) == 0 {
		types = nil
	}

	if len(objects) == 0 {
		objects = nil
	}

	s := notify.Subscription{
		ID: r.ID, Scope: namespace.Name(r.Scope), Sink: r.SinkID,
		Filter: notify.Filter{Types: types, Objects: objects, MinSeverity: notify.Severity(r.MinSeverity)},
		State:  notify.SubscriptionState(r.State), Reason: r.Reason, CreatedBy: r.CreatedBy, Version: r.Version,
	}

	if s.Version < 1 {
		return notify.Subscription{}, fmt.Errorf("%w: subscription %s: version %d", ErrCorrupt, s.ID, s.Version)
	}

	if err := s.Validate(); err != nil {
		return notify.Subscription{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	return s, nil
}

// DeliveryRow is a delivery as the database holds it. A time of 0 is the zero
// time.
type DeliveryRow struct {
	ID             string
	Scope          string
	NotificationID string
	SubscriptionID string
	SinkID         string
	Contact        string
	Principal      string
	State          string
	Attempts       int64
	NextAtMs       int64
	LastError      string
	DeliveredAtMs  int64
	Owner          string
	LeaseUntilMs   int64
	Version        int64
}

// Time returns the time of ms, or the zero time for 0.
func Time(ms int64) time.Time {
	if ms == 0 {
		return time.Time{}
	}

	return time.UnixMilli(ms).UTC()
}

// Millis returns the Unix milliseconds of t, or 0 for the zero time.
func Millis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}

	return t.UnixMilli()
}

// Delivery builds the delivery of a stored row and checks it again with
// notify.Delivery.Validate. It returns ErrCorrupt for a row that fails.
func Delivery(r DeliveryRow) (notify.Delivery, error) {
	d := notify.Delivery{
		ID: r.ID, Notification: r.NotificationID, Scope: namespace.Name(r.Scope), Subscription: r.SubscriptionID,
		Sink: r.SinkID, Contact: r.Contact, Principal: r.Principal, State: notify.DeliveryState(r.State),
		Attempts: int(r.Attempts), NextAt: Time(r.NextAtMs), LastError: r.LastError,
		DeliveredAt: Time(r.DeliveredAtMs), Owner: r.Owner, LeaseUntil: Time(r.LeaseUntilMs), Version: r.Version,
	}

	if err := d.Validate(); err != nil {
		return notify.Delivery{}, fmt.Errorf("%w: %w", ErrCorrupt, err)
	}

	return d, nil
}

// StagedRow is a row of a notification table in a staged backup. An adapter
// reads it with Dest and checks it with Check, so the adapters of the two
// dialects share one check for each kind of row.
type StagedRow interface {
	// Dest returns the destinations of the columns of the row, in the order of
	// the staged query.
	Dest() []any
	// Check applies the rules of a stored row. It returns the ID of the row, and
	// ErrCorrupt when the row fails.
	Check() (string, error)
}

var (
	_ StagedRow = (*NotificationRow)(nil)
	_ StagedRow = (*SubscriptionRow)(nil)
	_ StagedRow = (*DeliveryRow)(nil)
)

// Dest implements StagedRow for the columns scope, sequence, id, type, object,
// object_id, subject, agent, severity, summary, links, attributes, recipients,
// dedupe_key and at_ms.
func (r *NotificationRow) Dest() []any {
	return []any{
		&r.Scope, &r.Sequence, &r.ID, &r.Type, &r.Object, &r.ObjectID, &r.Subject, &r.Agent, &r.Severity,
		&r.Summary, &r.Links, &r.Attributes, &r.Recipients, &r.DedupeKey, &r.AtMs,
	}
}

// Check implements StagedRow.
func (r *NotificationRow) Check() (string, error) {
	_, err := Notification(*r)

	return r.ID, err
}

// Dest implements StagedRow for the columns id, scope, sink_id, types, objects,
// min_severity, state, reason, created_by and version.
func (r *SubscriptionRow) Dest() []any {
	return []any{
		&r.ID, &r.Scope, &r.SinkID, &r.Types, &r.Objects, &r.MinSeverity, &r.State, &r.Reason, &r.CreatedBy,
		&r.Version,
	}
}

// Check implements StagedRow.
func (r *SubscriptionRow) Check() (string, error) {
	_, err := Subscription(*r)

	return r.ID, err
}

// Dest implements StagedRow for the columns id, scope, notification_id,
// subscription_id, sink_id, contact, principal, state, attempts, next_at_ms,
// last_error, delivered_at_ms, owner, lease_until_ms and version.
func (r *DeliveryRow) Dest() []any {
	return []any{
		&r.ID, &r.Scope, &r.NotificationID, &r.SubscriptionID, &r.SinkID, &r.Contact, &r.Principal, &r.State,
		&r.Attempts, &r.NextAtMs, &r.LastError, &r.DeliveredAtMs, &r.Owner, &r.LeaseUntilMs, &r.Version,
	}
}

// Check implements StagedRow.
func (r *DeliveryRow) Check() (string, error) {
	_, err := Delivery(*r)

	return r.ID, err
}
