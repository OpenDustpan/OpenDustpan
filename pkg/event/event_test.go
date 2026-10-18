package event_test

import (
	"errors"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/event"
	"opendustpan.org/opendustpan/pkg/namespace"
)

func sample() event.Event {
	return event.Event{
		ID:       "evt-1",
		Type:     event.JobFailed,
		Scope:    "shop",
		Object:   "opentofu:shop/network",
		ObjectID: "u-1",
		Subject:  "alice",
		Agent:    "",
		Severity: event.SeverityWarning,
		Summary:  "The apply of network failed.",
		Links: []event.Link{
			{Title: "Job", URL: "https://dustpan.example.com/jobs/job-1", Kind: event.LinkConsole},
		},
		Attributes: map[string]string{"job.id": "job-1"},
		Recipients: nil,
		DedupeKey:  "job-1/failed",
		At:         time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC),
		Sequence:   0,
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	if err := sample().Validate(); err != nil {
		t.Fatalf("sample: %v", err)
	}

	bad := map[string]func(e *event.Event){
		"an unknown type":          func(e *event.Event) { e.Type = "job.exploded" },
		"no summary":               func(e *event.Event) { e.Summary = "" },
		"an http link":             func(e *event.Event) { e.Links[0].URL = "http://dustpan.example.com" },
		"an attribute key":         func(e *event.Event) { e.Attributes = map[string]string{"Job ID": "x"} },
		"no severity":              func(e *event.Event) { e.Severity = 0 },
		"a recipient of two kinds": func(e *event.Event) { e.Recipients = []event.Recipient{{Principal: "a", Group: "b"}} },
	}

	for name, change := range bad {
		e := sample()
		change(&e)

		if err := e.Validate(); !errors.Is(err, event.ErrInvalid) {
			t.Fatalf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
}

func TestFilterMatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		f    event.Filter
		want bool
	}{
		{"an empty filter matches", event.Filter{Types: nil, Objects: nil, MinSeverity: 0}, true},
		{"a type", event.Filter{Types: []string{"job.failed"}, Objects: nil, MinSeverity: 0}, true},
		{"a type prefix", event.Filter{Types: []string{"job.*"}, Objects: nil, MinSeverity: 0}, true},
		{"another prefix", event.Filter{Types: []string{"freeze.*"}, Objects: nil, MinSeverity: 0}, false},
		{"a prefix is a whole word", event.Filter{Types: []string{"jo.*"}, Objects: nil, MinSeverity: 0}, false},
		{"an object prefix", event.Filter{Types: nil, Objects: []string{"opentofu:shop/*"}, MinSeverity: 0}, true},
		{"another object", event.Filter{Types: nil, Objects: []string{"opentofu:shop/db"}, MinSeverity: 0}, false},
		{"a severity above", event.Filter{Types: nil, Objects: nil, MinSeverity: event.SeverityCritical}, false},
	}

	for _, tc := range cases {
		if got := tc.f.Match(sample()); got != tc.want {
			t.Fatalf("%s: Match = %t, want %t", tc.name, got, tc.want)
		}
	}

	if err := (event.Filter{Types: []string{"nothing.*"}, Objects: nil, MinSeverity: 0}).Validate(); !errors.Is(
		err,
		event.ErrInvalid,
	) {
		t.Fatalf("a filter that names no type: err = %v, want ErrInvalid", err)
	}
}

func TestRoute(t *testing.T) {
	t.Parallel()

	sub := func(id string, scope namespace.Name, state event.SubscriptionState, types ...string) event.Subscription {
		return event.Subscription{
			ID: id, Scope: scope, Sink: "snk-1", State: state, Reason: "", CreatedBy: "alice", Version: 1,
			Filter: event.Filter{Types: types, Objects: nil, MinSeverity: 0},
		}
	}

	subs := []event.Subscription{
		sub("sub-1", "shop", event.SubscriptionActive, "job.*"),
		sub("sub-2", "shop", event.SubscriptionSuspended, "job.*"),
		sub("sub-3", "shop", event.SubscriptionActive, "freeze.*"),
		sub("sub-4", "other", event.SubscriptionActive),
	}

	got := event.Route(sample(), subs)
	if len(got) != 1 || got[0].ID != "sub-1" {
		t.Fatalf("Route = %v, want sub-1", got)
	}
}

func TestRetryAt(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		n    int
		wait time.Duration
	}{{1, 30 * time.Second}, {2, time.Minute}, {3, 2 * time.Minute}, {9, time.Hour}, {14, time.Hour}}

	for _, tc := range cases {
		got, ok := event.RetryAt(tc.n, at)
		if !ok || got.Sub(at) != tc.wait {
			t.Fatalf("RetryAt(%d) = %s, %t; want a wait of %s", tc.n, got.Sub(at), ok, tc.wait)
		}
	}

	if _, ok := event.RetryAt(event.MaxAttempts, at); ok {
		t.Fatal("RetryAt after the last attempt = true, want false")
	}
}

func TestSignVerify(t *testing.T) {
	t.Parallel()

	secret := []byte("test-secret")
	body := []byte(`{"id":"evt-1"}`)
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	sig := event.Sign(secret, "evt-1", now.Unix(), body)
	ts := "1792490400"

	cases := []struct {
		name string
		id   string
		ts   string
		sig  string
		body []byte
		at   time.Time
		want error
	}{
		{"a good signature", "evt-1", ts, sig, body, now, nil},
		{"another body", "evt-1", ts, sig, []byte(`{"id":"evt-2"}`), now, event.ErrSignature},
		{"another event ID", "evt-2", ts, sig, body, now, event.ErrSignature},
		{
			"a replay after the tolerance",
			"evt-1",
			ts,
			sig,
			body,
			now.Add(event.SignatureTolerance + time.Second),
			event.ErrSignatureExpired,
		},
		{"a timestamp that is not a number", "evt-1", "soon", sig, body, now, event.ErrSignatureExpired},
		{"no version prefix", "evt-1", ts, sig[3:], body, now, event.ErrSignature},
	}

	for _, tc := range cases {
		h := event.Headers{EventID: tc.id, Timestamp: tc.ts, Signature: tc.sig}

		err := event.Verify(secret, h, tc.body, tc.at)
		if (tc.want == nil && err != nil) || (tc.want != nil && !errors.Is(err, tc.want)) {
			t.Fatalf("%s: Verify = %v, want %v", tc.name, err, tc.want)
		}
	}
}

func TestQuiet(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 24, 3, 0, 0, 0, time.UTC)
	monday := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	next := func(time.Time) (time.Time, error) { return monday, nil }

	if got, err := event.Quiet(event.SeverityCritical, now, next); err != nil || !got.Equal(now) {
		t.Fatalf("critical: %s (%v), want now", got, err)
	}

	if got, err := event.Quiet(event.SeverityWarning, now, next); err != nil || !got.Equal(monday) {
		t.Fatalf("warning: %s (%v), want Monday", got, err)
	}
}
