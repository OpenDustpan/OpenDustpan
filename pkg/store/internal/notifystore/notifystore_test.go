package notifystore_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/store/internal/notifystore"
)

func goodNotification() notifystore.NotificationRow {
	return notifystore.NotificationRow{
		Scope: "shop", Sequence: 1, ID: "ntf-1", Type: string(notify.JobFailed), Object: "opentofu:shop/network",
		ObjectID: "u-1", Subject: "alice", Agent: "", Severity: int64(notify.SeverityWarning), Summary: "Failed.",
		Links:      `[{"title":"Run","url":"https://console.example.com/run","kind":"console"}]`,
		Attributes: `{"job":"job-1"}`, Recipients: `[{"admins":true}]`, DedupeKey: "job-1/failed",
		AtMs: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC).UnixMilli(),
	}
}

func TestNotificationRoundTrip(t *testing.T) {
	t.Parallel()

	want := goodNotification()

	n, err := notifystore.Notification(want)
	if err != nil {
		t.Fatalf("Notification: %v", err)
	}

	got, err := notifystore.EncodeNotification(n, want.Sequence)
	if err != nil {
		t.Fatalf("EncodeNotification: %v", err)
	}

	if got != want {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}

// TestStoredRowsAreUntrusted gives each reader a row that a restore or another
// version could have written, as if every other check had failed.
func TestStoredRowsAreUntrusted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		change func(r *notifystore.NotificationRow)
	}{
		{"a list that does not decode", func(r *notifystore.NotificationRow) { r.Links = `{` }},
		{"a link with a scheme that is not http", func(r *notifystore.NotificationRow) {
			r.Links = `[{"title":"x","url":"javascript:alert(1)","kind":"console"}]`
		}},
		{"an unknown type", func(r *notifystore.NotificationRow) { r.Type = "nope" }},
		{"no dedupe key", func(r *notifystore.NotificationRow) { r.DedupeKey = "" }},
		{"an unknown severity", func(r *notifystore.NotificationRow) { r.Severity = 9 }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := goodNotification()
			tc.change(&r)

			if _, err := notifystore.Notification(r); !errors.Is(err, notifystore.ErrCorrupt) {
				t.Fatalf("Notification = %v, want ErrCorrupt", err)
			}
		})
	}

	subs := []notifystore.SubscriptionRow{
		{ID: "sub-1", Scope: "shop", SinkID: "snk-1", Types: `["nope.*"]`, Objects: `[]`, MinSeverity: 0,
			State: "active", Reason: "", CreatedBy: "alice", Version: 1},
		{ID: "sub-1", Scope: "shop", SinkID: "snk-1", Types: `[]`, Objects: `[]`, MinSeverity: 0,
			State: "active", Reason: "", CreatedBy: "alice", Version: 0},
		{ID: "sub-1", Scope: "shop", SinkID: "snk-1", Types: `[]`, Objects: `[]`, MinSeverity: 0,
			State: "weird", Reason: "", CreatedBy: "alice", Version: 1},
	}

	for _, r := range subs {
		if _, err := notifystore.Subscription(r); !errors.Is(err, notifystore.ErrCorrupt) {
			t.Fatalf("Subscription(%+v) = %v, want ErrCorrupt", r, err)
		}
	}

	d := notifystore.DeliveryRow{
		ID: "dlv-1", Scope: "shop", NotificationID: "ntf-1", SubscriptionID: "sub-1", SinkID: "snk-1", Contact: "",
		Principal: "alice", State: "pending", Attempts: 0, NextAtMs: 1, LastError: "dial tcp 10.0.0.1: refused",
		DeliveredAtMs: 0, Owner: "", LeaseUntilMs: 0, Version: 1,
	}

	if _, err := notifystore.Delivery(d); !errors.Is(err, notifystore.ErrCorrupt) {
		t.Fatalf("Delivery with the text of a network error = %v, want ErrCorrupt", err)
	}
}
