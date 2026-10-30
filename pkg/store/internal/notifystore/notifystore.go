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

// EncodeFilter returns the text form of the type list and the object list of
// f.
func EncodeFilter(f notify.Filter) (types, objects string, err error) {
	t, err := json.Marshal(nonNil(f.Types))
	if err != nil {
		return "", "", fmt.Errorf("encode filter types: %w", err)
	}

	o, err := json.Marshal(nonNil(f.Objects))
	if err != nil {
		return "", "", fmt.Errorf("encode filter objects: %w", err)
	}

	return string(t), string(o), nil
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
