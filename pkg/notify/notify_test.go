package notify_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
)

func sample() notify.Notification {
	return notify.Notification{
		ID:       "ntf-1",
		Type:     notify.JobFailed,
		Scope:    "shop",
		Object:   "opentofu:shop/network",
		ObjectID: "u-1",
		Subject:  "alice",
		Agent:    "",
		Severity: notify.SeverityWarning,
		Summary:  "The apply of network failed.",
		Links: []notify.Link{
			{Title: "Job", URL: "https://dustpan.example.com/jobs/job-1", Kind: notify.LinkConsole},
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

	cases := []struct {
		name   string
		change func(n *notify.Notification)
		ok     bool
	}{
		{"the sample", func(*notify.Notification) {}, true},
		{"an http link, as the context of an object allows", func(n *notify.Notification) {
			n.Links[0].URL = "http://wiki.example.com/net"
			n.Links[0].Kind = notify.LinkDocs
		}, true},
		{"an unknown type", func(n *notify.Notification) { n.Type = "job.exploded" }, false},
		{"another ID prefix", func(n *notify.Notification) { n.ID = "evt-1" }, false},
		{"no summary", func(n *notify.Notification) { n.Summary = "" }, false},
		{"no dedupe key", func(n *notify.Notification) { n.DedupeKey = "" }, false},
		{"a link of another scheme", func(n *notify.Notification) { n.Links[0].URL = "javascript:alert(1)" }, false},
		{"a link with no host", func(n *notify.Notification) { n.Links[0].URL = "https://" }, false},
		{"a link of an unknown kind", func(n *notify.Notification) { n.Links[0].Kind = "video" }, false},
		{"an attribute key", func(n *notify.Notification) { n.Attributes = map[string]string{"Job ID": "x"} }, false},
		{"no severity", func(n *notify.Notification) { n.Severity = 0 }, false},
		{"a recipient of two kinds", func(n *notify.Notification) {
			n.Recipients = []notify.Recipient{{Principal: "a", Group: "b"}}
		}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			n := sample()
			tc.change(&n)

			err := n.Validate()
			if tc.ok && err != nil {
				t.Fatalf("Validate = %v, want nil", err)
			}

			if !tc.ok && !errors.Is(err, notify.ErrInvalid) {
				t.Fatalf("Validate = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestFit(t *testing.T) {
	t.Parallel()

	many := func(k int, f func(i int) notify.Link) []notify.Link {
		out := make([]notify.Link, k)
		for i := range out {
			out[i] = f(i)
		}

		return out
	}

	cases := []struct {
		name   string
		change func(n *notify.Notification)
		check  func(n notify.Notification) bool
	}{
		{"a long summary is cut at a rune boundary", func(n *notify.Notification) {
			n.Summary = strings.Repeat("é", notify.MaxSummaryLen)
		}, func(n notify.Notification) bool {
			return len(n.Summary) == notify.MaxSummaryLen && strings.HasSuffix(n.Summary, "é")
		}},
		{
			"an empty summary becomes the type", func(n *notify.Notification) { n.Summary = "" },
			func(n notify.Notification) bool { return n.Summary == string(notify.JobFailed) },
		},
		{
			"an empty dedupe key becomes the ID", func(n *notify.Notification) { n.DedupeKey = "" },
			func(n notify.Notification) bool { return n.DedupeKey == "ntf-1" },
		},
		{"an http link stays", func(n *notify.Notification) {
			n.Links = []notify.Link{{Title: "Wiki", URL: "http://wiki.example.com", Kind: notify.LinkDocs}}
		}, func(n notify.Notification) bool { return len(n.Links) == 1 }},
		{"a link that is not valid is dropped", func(n *notify.Notification) {
			n.Links = []notify.Link{{Title: "Bad", URL: "ftp://x", Kind: notify.LinkDocs}}
		}, func(n notify.Notification) bool { return len(n.Links) == 0 }},
		{"links above the bound are dropped", func(n *notify.Notification) {
			n.Links = many(notify.MaxLinks+5, func(i int) notify.Link {
				return notify.Link{Title: "L", URL: fmt.Sprintf("https://x.example.com/%d", i), Kind: notify.LinkOther}
			})
		}, func(n notify.Notification) bool { return len(n.Links) == notify.MaxLinks }},
		{"a long link title is cut", func(n *notify.Notification) {
			n.Links[0].Title = strings.Repeat("t", notify.MaxLinkTitleLen+1)
		}, func(n notify.Notification) bool { return len(n.Links[0].Title) == notify.MaxLinkTitleLen }},
		{"a long attribute value is cut and a bad key is dropped", func(n *notify.Notification) {
			n.Attributes = map[string]string{"job.id": strings.Repeat("v", 300), "Bad Key": "x"}
		}, func(n notify.Notification) bool {
			return len(n.Attributes) == 1 && len(n.Attributes["job.id"]) == notify.MaxAttributeLen
		}},
		{"a bad recipient is dropped", func(n *notify.Notification) {
			n.Recipients = []notify.Recipient{{Principal: "", Group: ""}, {Principal: "alice", Group: ""}}
		}, func(n notify.Notification) bool { return len(n.Recipients) == 1 }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			n := sample()
			tc.change(&n)

			got := n.Fit()
			if err := got.Validate(); err != nil {
				t.Fatalf("Validate after Fit = %v", err)
			}

			if !tc.check(got) {
				t.Fatalf("Fit = %+v", got)
			}
		})
	}
}

func TestSinkValidate(t *testing.T) {
	t.Parallel()

	webhook := func(target string, refs ...string) notify.Sink {
		return notify.Sink{
			ID: "snk-1", Scope: "shop", Kind: notify.SinkWebhook, Name: "Ops", Target: target,
			SecretRefs: refs, CreatedBy: "alice", Version: 0,
		}
	}

	email := webhook("ops@example.com")
	email.Kind = notify.SinkEmail

	chat := webhook("https://chat.example.com/hook/1")
	chat.Kind = notify.SinkChat

	cases := []struct {
		name    string
		sink    notify.Sink
		ok      bool
		refused bool
	}{
		{"a webhook", webhook("https://hooks.example.com/ops", "itm-1"), true, false},
		{"a webhook with two secrets", webhook("https://hooks.example.com/ops", "itm-1", "itm-2"), true, false},
		{"a webhook with three secrets", webhook("https://hooks.example.com/ops", "a", "b", "c"), false, false},
		{"a webhook with no secret", webhook("https://hooks.example.com/ops"), false, false},
		{"an http webhook", webhook("http://hooks.example.com/ops", "itm-1"), false, true},
		{"the metadata address", webhook("https://169.254.169.254/", "itm-1"), false, true},
		{"IPv6 loopback", webhook("https://[::1]:8443/", "itm-1"), false, true},
		{"a private address", webhook("https://10.0.0.5/hook", "itm-1"), false, true},
		{"localhost", webhook("https://localhost/hook", "itm-1"), false, true},
		{"user information", webhook("https://user:pw@hooks.example.com/", "itm-1"), false, true},
		{"a chat webhook", chat, true, false},
		{"an email address", email, true, false},
		{"an email address with a name", func() notify.Sink {
			s := email
			s.Target = "Ops <ops@example.com>"

			return s
		}(), false, false},
		{"an email sink with a secret", func() notify.Sink {
			s := email
			s.SecretRefs = []string{"itm-1"}

			return s
		}(), false, false},
		{"an unknown kind", func() notify.Sink {
			s := email
			s.Kind = "pager"

			return s
		}(), false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.sink.Validate()
			if tc.ok && err != nil {
				t.Fatalf("Validate = %v, want nil", err)
			}

			if !tc.ok && !errors.Is(err, notify.ErrInvalid) {
				t.Fatalf("Validate = %v, want ErrInvalid", err)
			}

			if tc.refused != errors.Is(err, egress.ErrRefused) {
				t.Fatalf("Validate = %v, egress.ErrRefused = %t, want %t", err, !tc.refused, tc.refused)
			}
		})
	}
}

func TestFilterMatch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		f    notify.Filter
		want bool
	}{
		{"an empty filter matches", notify.Filter{Types: nil, Objects: nil, MinSeverity: 0}, true},
		{"a type", notify.Filter{Types: []string{"job.failed"}, Objects: nil, MinSeverity: 0}, true},
		{"a type prefix", notify.Filter{Types: []string{"job.*"}, Objects: nil, MinSeverity: 0}, true},
		{"another prefix", notify.Filter{Types: []string{"freeze.*"}, Objects: nil, MinSeverity: 0}, false},
		{"a prefix is a whole word", notify.Filter{Types: []string{"jo.*"}, Objects: nil, MinSeverity: 0}, false},
		{"an object prefix", notify.Filter{Types: nil, Objects: []string{"opentofu:shop/*"}, MinSeverity: 0}, true},
		{"another object", notify.Filter{Types: nil, Objects: []string{"opentofu:shop/db"}, MinSeverity: 0}, false},
		{"a severity above", notify.Filter{Types: nil, Objects: nil, MinSeverity: notify.SeverityCritical}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := tc.f.Match(sample()); got != tc.want {
				t.Fatalf("Match = %t, want %t", got, tc.want)
			}
		})
	}

	err := notify.Filter{Types: []string{"nothing.*"}, Objects: nil, MinSeverity: 0}.Validate()
	if !errors.Is(err, notify.ErrInvalid) {
		t.Fatalf("a filter that names no type: err = %v, want ErrInvalid", err)
	}
}

// reader is a notify.Reader that lets the principals in can read, and
// fails for the principal "broken".
type reader map[string]bool

var errCheck = errors.New("check failed")

func (r reader) CanRead(_ context.Context, id string, _ notify.Notification) (bool, error) {
	if id == "broken" {
		return false, errCheck
	}

	return r[id], nil
}

func TestRoute(t *testing.T) {
	t.Parallel()

	sub := func(id, creator string, scope namespace.Name, state notify.SubscriptionState, types ...string) notify.Subscription {
		return notify.Subscription{
			ID: id, Scope: scope, Sink: "snk-1", State: state, Reason: "", CreatedBy: creator, Version: 1,
			Filter: notify.Filter{Types: types, Objects: nil, MinSeverity: 0},
		}
	}

	subs := []notify.Subscription{
		sub("sub-1", "alice", "shop", notify.SubscriptionStateActive, "job.*"),
		sub("sub-2", "alice", "shop", notify.SubscriptionStateSuspended, "job.*"),
		sub("sub-3", "alice", "shop", notify.SubscriptionStateActive, "freeze.*"),
		sub("sub-4", "alice", "other", notify.SubscriptionStateActive),
		sub("sub-5", "bob", "shop", notify.SubscriptionStateActive, "job.*"),
	}

	cases := []struct {
		name    string
		readers reader
		subs    []notify.Subscription
		want    []string
		err     error
	}{
		{"both creators can read", reader{"alice": true, "bob": true}, subs, []string{"sub-1", "sub-5"}, nil},
		{"a grant hides the unit from bob", reader{"alice": true}, subs, []string{"sub-1"}, nil},
		{"no creator can read", reader{}, subs, nil, nil},
		{"the check fails", reader{}, []notify.Subscription{
			sub("sub-9", "broken", "shop", notify.SubscriptionStateActive),
		}, nil, errCheck},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := notify.Route(t.Context(), sample(), tc.subs, tc.readers)
			if !errors.Is(err, tc.err) {
				t.Fatalf("Route err = %v, want %v", err, tc.err)
			}

			ids := make([]string, 0, len(got))
			for _, s := range got {
				ids = append(ids, s.ID)
			}

			if fmt.Sprint(ids) != fmt.Sprint(tc.want) && (len(ids) != 0 || len(tc.want) != 0) {
				t.Fatalf("Route = %v, want %v", ids, tc.want)
			}
		})
	}
}

func TestReaders(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ids  []string
		want string
		err  error
	}{
		{"only the readers get the notice", []string{"alice", "bob", "carol"}, "[alice carol]", nil},
		{"nobody can read", []string{"bob"}, "[]", nil},
		{"the check fails", []string{"alice", "broken"}, "[]", errCheck},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := notify.Readers(t.Context(), sample(), tc.ids, reader{"alice": true, "carol": true})
			if !errors.Is(err, tc.err) || fmt.Sprint(got) != tc.want {
				t.Fatalf("Readers = %v (%v), want %s (%v)", got, err, tc.want, tc.err)
			}
		})
	}
}

func TestRetryAt(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		n    int
		err  error
		wait time.Duration
		ok   bool
	}{
		{"first", 1, notify.ErrTemporary, 30 * time.Second, true},
		{"second", 2, notify.ErrTemporary, time.Minute, true},
		{"third", 3, notify.ErrTemporary, 2 * time.Minute, true},
		{"capped", 9, notify.ErrTemporary, time.Hour, true},
		{"last but one", 14, notify.ErrTemporary, time.Hour, true},
		{"after the last attempt", notify.MaxAttempts, notify.ErrTemporary, 0, false},
		{"a permanent error", 1, notify.ErrPermanent, 0, false},
		{"a refused address", 1, egress.ErrRefused, 0, false},
		{"a longer Retry-After", 1, &notify.RetryAfterError{Wait: 10 * time.Minute}, 10 * time.Minute, true},
		{"a shorter Retry-After", 3, &notify.RetryAfterError{Wait: time.Second}, 2 * time.Minute, true},
		{"a very long Retry-After", 1, &notify.RetryAfterError{Wait: 72 * time.Hour}, notify.SuspendAfter, true},
		{"a wrapped Retry-After", 1, fmt.Errorf("send: %w", &notify.RetryAfterError{Wait: time.Hour}), time.Hour, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := notify.RetryAt(tc.n, at, tc.err)
			if ok != tc.ok || (ok && got.Sub(at) != tc.wait) {
				t.Fatalf("RetryAt = %s, %t; want a wait of %s, %t", got.Sub(at), ok, tc.wait, tc.ok)
			}
		})
	}
}

func TestClass(t *testing.T) {
	t.Parallel()

	cases := []struct {
		err  error
		want string
	}{
		{fmt.Errorf("dial: %w", egress.ErrRefused), notify.ClassRefused},
		{fmt.Errorf("status 404: %w", notify.ErrPermanent), notify.ClassPermanent},
		{&notify.RetryAfterError{Wait: time.Minute}, notify.ClassTemporary},
		{fmt.Errorf("connection reset by 203.0.113.9: %w", errCheck), notify.ClassTemporary},
	}

	for _, tc := range cases {
		if got := notify.Class(tc.err); got != tc.want {
			t.Fatalf("Class(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}

func TestSignVerify(t *testing.T) {
	t.Parallel()

	oldKey, newKey, other := []byte("old-secret"), []byte("new-secret"), []byte("other-secret")
	body := []byte(`{"id":"ntf-1"}`)
	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)
	ts := "1792490400"

	sign := func(secrets ...[]byte) string {
		s, err := notify.Sign(secrets, "ntf-1", now.Unix(), body)
		if err != nil {
			t.Fatalf("Sign: %v", err)
		}

		return s
	}

	one, both := sign(oldKey), sign(oldKey, newKey)
	if strings.Count(both, "v1=") != 2 || !strings.Contains(both, ",") {
		t.Fatalf("Sign with two secrets = %q, want two v1= values", both)
	}

	cases := []struct {
		name    string
		secrets [][]byte
		id      string
		ts      string
		sig     string
		body    []byte
		at      time.Time
		want    error
	}{
		{"a good signature", [][]byte{oldKey}, "ntf-1", ts, one, body, now, nil},
		{"during a rotation, a receiver with the old key", [][]byte{oldKey}, "ntf-1", ts, both, body, now, nil},
		{"during a rotation, a receiver with the new key", [][]byte{newKey}, "ntf-1", ts, both, body, now, nil},
		{"a receiver with two keys", [][]byte{other, oldKey}, "ntf-1", ts, one, body, now, nil},
		{"another key", [][]byte{other}, "ntf-1", ts, both, body, now, notify.ErrSignature},
		{"another body", [][]byte{oldKey}, "ntf-1", ts, one, []byte(`{"id":"ntf-2"}`), now, notify.ErrSignature},
		{"another notification ID", [][]byte{oldKey}, "ntf-2", ts, one, body, now, notify.ErrSignature},
		{
			"a replay after the tolerance",
			[][]byte{oldKey},
			"ntf-1", ts, one, body,
			now.Add(notify.SignatureTolerance + time.Second), notify.ErrSignatureExpired,
		},
		{
			"a timestamp that is not a number",
			[][]byte{oldKey},
			"ntf-1",
			"soon",
			one,
			body,
			now,
			notify.ErrSignatureExpired,
		},
		{"no version prefix", [][]byte{oldKey}, "ntf-1", ts, one[3:], body, now, notify.ErrSignature},
		{"three signatures", [][]byte{oldKey}, "ntf-1", ts, both + "," + one, body, now, notify.ErrSignature},
		{"no secret", nil, "ntf-1", ts, one, body, now, notify.ErrSecrets},
		{"an empty secret", [][]byte{{}}, "ntf-1", ts, one, body, now, notify.ErrSecrets},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := notify.Headers{NotificationID: tc.id, Timestamp: tc.ts, Signature: tc.sig}

			err := notify.Verify(tc.secrets, h, tc.body, tc.at)
			if (tc.want == nil && err != nil) || (tc.want != nil && !errors.Is(err, tc.want)) {
				t.Fatalf("Verify = %v, want %v", err, tc.want)
			}
		})
	}

	if _, err := notify.Sign([][]byte{oldKey, newKey, other}, "ntf-1", 1, body); !errors.Is(err, notify.ErrSecrets) {
		t.Fatalf("Sign with three secrets: err = %v, want ErrSecrets", err)
	}
}

func TestQuiet(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 24, 3, 0, 0, 0, time.UTC)
	monday := time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)
	next := func(time.Time) (time.Time, error) { return monday, nil }

	if got, err := notify.Quiet(notify.SeverityCritical, now, next); err != nil || !got.Equal(now) {
		t.Fatalf("critical: %s (%v), want now", got, err)
	}

	if got, err := notify.Quiet(notify.SeverityWarning, now, next); err != nil || !got.Equal(monday) {
		t.Fatalf("warning: %s (%v), want Monday", got, err)
	}
}
