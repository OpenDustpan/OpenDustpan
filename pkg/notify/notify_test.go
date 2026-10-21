package notify_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"opendustpan.org/opendustpan/pkg/audit"
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
			n.Recipients = []notify.Recipient{{Principal: "a", Group: "b", Admins: false}}
		}, false},
		{"a recipient of a principal and the administrators", func(n *notify.Notification) {
			n.Recipients = []notify.Recipient{{Principal: "a", Group: "", Admins: true}}
		}, false},
		{"the administrators of the scope", func(n *notify.Notification) {
			n.Recipients = []notify.Recipient{{Principal: "", Group: "", Admins: true}}
		}, true},
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
		{
			"a dedupe key at the bound stays",
			func(n *notify.Notification) { n.DedupeKey = strings.Repeat("k", notify.MaxAttributeLen) },
			func(n notify.Notification) bool { return n.DedupeKey == strings.Repeat("k", notify.MaxAttributeLen) },
		},
		{"two long dedupe keys that differ after the bound stay apart", func(n *notify.Notification) {
			n.DedupeKey = longKey("a")
		}, func(n notify.Notification) bool {
			b := sample()
			b.DedupeKey = longKey("b")

			return n.DedupeKey != b.Fit().DedupeKey && len(n.DedupeKey) <= notify.MaxAttributeLen &&
				strings.HasPrefix(n.DedupeKey, "bgr-1/") && utf8.ValidString(n.DedupeKey)
		}},
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
			n.Recipients = []notify.Recipient{
				{Principal: "", Group: "", Admins: false}, {Principal: "alice", Group: "", Admins: false},
			}
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

// longKey returns a dedupe key above the bound that ends in tail, with
// runes of two bytes across the bound.
func longKey(tail string) string {
	return "bgr-1/" + strings.Repeat("é", notify.MaxAttributeLen) + tail
}

func TestSinkValidate(t *testing.T) {
	t.Parallel()

	webhook := func(target string, refs ...string) notify.Sink {
		return notify.Sink{
			ID: "snk-1", Scope: "shop", Kind: notify.SinkWebhook, Name: "Ops", Platform: "", Target: target,
			SecretRefs: refs, CreatedBy: "alice", Version: 0,
		}
	}

	email := webhook("ops@example.com")
	email.Kind = notify.SinkEmail

	chat := webhook("chat.example.com", "itm-1")
	chat.Kind, chat.Platform = notify.SinkChat, "slack"

	withPlatform := func(s notify.Sink, platform string) notify.Sink {
		s.Platform = platform

		return s
	}

	chatWith := func(target string, refs ...string) notify.Sink {
		s := chat
		s.Target, s.SecretRefs = target, refs

		return s
	}

	cases := []struct {
		name    string
		sink    notify.Sink
		ok      bool
		refused bool
	}{
		{"a webhook", webhook("hooks.example.com", "itm-url", "itm-1"), true, false},
		{"a webhook with a port", webhook("hooks.example.com:8443", "itm-url", "itm-1"), true, false},
		{"a webhook with two keys", webhook("hooks.example.com", "itm-url", "itm-1", "itm-2"), true, false},
		{"a webhook with three keys", webhook("hooks.example.com", "u", "a", "b", "c"), false, false},
		{"a webhook with no secret", webhook("hooks.example.com"), false, false},
		{"a webhook with a URL item and no key", webhook("hooks.example.com", "itm-url"), false, false},
		{"a webhook with the URL as target", webhook("https://hooks.example.com/ops", "u", "k"), false, false},
		{"a webhook host with a path", webhook("hooks.example.com/ops", "u", "k"), false, false},
		{"a webhook host in upper case", webhook("Hooks.example.com", "u", "k"), false, false},
		{"a ref with a zero byte", webhook("hooks.example.com", "u", "itm-1\x00"), false, false},
		{"a ref with a slash", webhook("hooks.example.com", "u", "itm/1"), false, false},
		{"an empty ref", webhook("hooks.example.com", "u", ""), false, false},
		{"a ref above the bound", webhook("hooks.example.com", "u", strings.Repeat("r", 101)), false, false},
		{"a ref at the bound", webhook("hooks.example.com", "u", strings.Repeat("r", 100)), true, false},
		{"the metadata address", webhook("169.254.169.254", "u", "k"), false, true},
		{"IPv6 loopback", webhook("[::1]:8443", "u", "k"), false, true},
		{"a private address", webhook("10.0.0.5", "u", "k"), false, true},
		{"localhost", webhook("localhost", "u", "k"), false, true},
		{"user information", webhook("user:pw@hooks.example.com", "u", "k"), false, true},
		{"a chat webhook", chat, true, false},
		{"a chat webhook with a port", chatWith("chat.example.com:8443", "itm-1"), true, false},
		{"a chat webhook with the URL as target", chatWith("https://chat.example.com/hook/1", "itm-1"), false, false},
		{"a chat host with a path", chatWith("chat.example.com/hook/1", "itm-1"), false, false},
		{"a chat host in upper case", chatWith("Chat.example.com", "itm-1"), false, false},
		{"a chat webhook with no secret item", chatWith("chat.example.com"), false, false},
		{"a chat webhook with two secret items", chatWith("chat.example.com", "itm-1", "itm-2"), false, false},
		{"a chat host on localhost", chatWith("localhost", "itm-1"), false, true},
		{"a chat host on a private address", chatWith("10.0.0.5", "itm-1"), false, true},
		{"a chat webhook of each platform", withPlatform(chat, "matrix"), true, false},
		{"a chat webhook with no platform", withPlatform(chat, ""), false, false},
		{"a chat webhook with an unknown platform", withPlatform(chat, "irc"), false, false},
		{"a chat platform in upper case", withPlatform(chat, "Slack"), false, false},
		{
			"a webhook with a platform",
			withPlatform(webhook("hooks.example.com", "u", "itm-1"), "slack"),
			false,
			false,
		},
		{"an email sink with a platform", withPlatform(email, "slack"), false, false},
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

func TestChatHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		url  string
		want string
		err  error
	}{
		{"https://hooks.example.com/services/T0/B0/secret", "hooks.example.com", nil},
		{"https://Hooks.Example.com:8443/x", "hooks.example.com:8443", nil},
		{"http://hooks.example.com/x", "", egress.ErrRefused},
		{"https://user:pw@hooks.example.com/x", "", egress.ErrRefused},
		{"https://169.254.169.254/x", "", egress.ErrRefused},
		{"https://[2606:4700::1111]:8443/x", "[2606:4700::1111]:8443", nil},
		{"https://hooks%2eexample.com/x", "", egress.ErrRefused},
		{"https://h\u00f6\u00f6ks.example.com/x", "", notify.ErrInvalid},
		{"https://hooks.example.com\u202e/x", "", notify.ErrInvalid},
		{"https://hooks.example.com;secret/x", "", notify.ErrInvalid},
		{"https://hooks.example.com:/x", "", notify.ErrInvalid},
		{"https://a_b!$&'()*+,=.example.com/x", "", notify.ErrInvalid},
		{"https://hooks.example.com:0443/x", "", notify.ErrInvalid},
		{"https://[::ffff:8.8.8.8]/x", "[::ffff:8.8.8.8]", nil},
		// The hosts of the chat platforms.
		{"https://hooks.slack.com/services/T0/B0/x", "hooks.slack.com", nil},
		{"https://hooks.slack-gov.com/services/T0/B0/x", "hooks.slack-gov.com", nil},
		{"https://contoso.webhook.office.com/webhookb2/x", "contoso.webhook.office.com", nil},
		{"https://prod-27.westus.logic.azure.com:443/workflows/x", "prod-27.westus.logic.azure.com:443", nil},
		{"https://discord.com/api/webhooks/1/x", "discord.com", nil},
		{"https://chat.googleapis.com/v1/spaces/S/messages?key=k", "chat.googleapis.com", nil},
		{"https://mattermost.example.com/hooks/x", "mattermost.example.com", nil},
		{"https://matrix.example.com:8448/_matrix/x", "matrix.example.com:8448", nil},
	}

	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			t.Parallel()

			got, err := notify.ChatHost(tc.url)
			if got != tc.want || !errors.Is(err, tc.err) {
				t.Fatalf("ChatHost = %q (%v), want %q (%v)", got, err, tc.want, tc.err)
			}
		})
	}
}

func TestScopeActions(t *testing.T) {
	t.Parallel()

	withType := func(typ notify.Type, scope namespace.Name) notify.Notification {
		n := sample()
		n.Type, n.Scope = typ, scope

		return n
	}
	withObject := func(object string, scope namespace.Name) notify.Notification {
		n := withType(notify.SubscriptionSuspended, scope)
		n.Object = object

		return n
	}

	cases := []struct {
		name string
		n    notify.Notification
		want string
	}{
		{"a job notification", withType(notify.JobFailed, "shop"), "[namespace:read]"},
		{"a cost estimate", withType(notify.CostEstimated, "shop"), "[namespace:read namespace:read-cost]"},
		{"a budget threshold", withType(notify.BudgetThreshold, "shop"), "[namespace:read namespace:read-cost]"},
		{"a notification of the server", withType(notify.SubscriptionSuspended, "@server"), "[server:observe]"},
		{
			"a notice about the notifications of a namespace", withObject(notify.NotificationsObject("shop"), "shop"),
			"[namespace:read namespace:manage-notifications]",
		},
		{
			"a notice about the notifications of the server",
			withObject(notify.NotificationsObject("@server"), "@server"),
			"[server:observe server:manage-notifications]",
		},
		{
			"a notice about the notifications of another namespace",
			withObject(notify.NotificationsObject("other"), "shop"), "[namespace:read]",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := fmt.Sprint(notify.ScopeActions(tc.n)); got != tc.want {
				t.Fatalf("ScopeActions = %s, want %s", got, tc.want)
			}
		})
	}
}

// contacts is a notify.Contacts that holds the addresses of each
// principal, and fails for the principal "lost".
type contacts map[string][]string

func (c contacts) ContactsOf(_ context.Context, id string) ([]notify.Contact, error) {
	if id == "lost" {
		return nil, errCheck
	}

	out := make([]notify.Contact, 0, len(c[id]))
	for _, a := range c[id] {
		out = append(out, notify.Contact{Kind: notify.SinkEmail, Address: a})
	}

	return out, nil
}

func (contacts) Members(context.Context, string) ([]string, error) { return nil, nil }

func (contacts) Admins(context.Context, namespace.Name) ([]string, error) { return nil, nil }

func TestBeforeAttempt(t *testing.T) {
	t.Parallel()

	d := func(principal, contact string) notify.Delivery {
		return notify.Delivery{
			ID: "dlv-1", Notification: "ntf-1", Scope: "shop", Subscription: "sub-1", Sink: "snk-1",
			Contact: contact, Principal: principal, State: notify.DeliveryPending, Attempts: 1,
			NextAt: time.Time{}, LastError: "", DeliveredAt: time.Time{}, Owner: "i-1", LeaseUntil: time.Time{},
			Version: 2,
		}
	}

	access := struct {
		reader
		contacts
	}{reader{"alice": true, "lost": true}, contacts{"alice": {"alice@example.com"}}}

	var gone notify.Subscription

	// sub returns the subscription of a delivery of principal, changed by
	// change.
	sub := func(principal string, change func(s *notify.Subscription)) notify.Subscription {
		s := notify.Subscription{
			ID: "sub-1", Scope: "shop", Sink: "snk-1", Filter: notify.Filter{Types: nil, Objects: nil, MinSeverity: 0},
			State: notify.SubscriptionStateActive, Reason: "", CreatedBy: principal, Version: 3,
		}
		if change != nil {
			change(&s)
		}

		return s
	}

	notice := d("alice", "alice@example.com")
	notice.Subscription = ""

	cases := []struct {
		name string
		d    notify.Delivery
		sub  notify.Subscription
		want error
	}{
		{"the creator can still read", d("alice", ""), sub("alice", nil), nil},
		{"the creator lost access", d("bob", ""), sub("bob", nil), notify.ErrNoAccess},
		{"the contact is still a contact", d("alice", "alice@example.com"), sub("alice", nil), nil},
		{"the contact is gone", d("alice", "old@example.com"), sub("alice", nil), notify.ErrNoAccess},
		{"the read check fails", d("broken", ""), sub("broken", nil), errCheck},
		{"the contact check fails", d("lost", "lost@example.com"), sub("lost", nil), errCheck},
		{
			"a subscription that a sink change suspended", d("alice", ""),
			sub("alice", func(s *notify.Subscription) {
				s.State, s.Reason = notify.SubscriptionStateSuspended, notify.ReasonSinkChanged
			}),
			notify.ErrNoAccess,
		},
		{
			"a subscription that another person resumed", d("alice", ""),
			sub("bob", nil), notify.ErrNoAccess,
		},
		{
			"a subscription that sends to another sink now", d("alice", ""),
			sub("alice", func(s *notify.Subscription) { s.Sink = "snk-2" }), notify.ErrNoAccess,
		},
		{"a subscription that is gone", d("alice", ""), gone, notify.ErrNoAccess},
		{
			"another subscription", d("alice", ""),
			sub("alice", func(s *notify.Subscription) { s.ID = "sub-2" }), notify.ErrNoAccess,
		},
		{"a notice with no subscription", notice, gone, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := notify.BeforeAttempt(t.Context(), tc.d, tc.sub, sample(), access)
			if !errors.Is(err, tc.want) || (tc.want == nil && err != nil) {
				t.Fatalf("BeforeAttempt = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC)

	cases := []struct {
		name string
		h    string
		want time.Duration
		ok   bool
	}{
		{"seconds", "120", 2 * time.Minute, true},
		{"zero", "0", 0, true},
		{"a huge count", "99999999999999999999999", notify.SuspendAfter, true},
		{"more than a day", "172800", notify.SuspendAfter, true},
		{"a negative count", "-5", 0, false},
		{"a signed count", "+5", 0, false},
		{"a date", "Tue, 20 Oct 2026 10:05:00 GMT", 5 * time.Minute, true},
		{"a past date", "Mon, 19 Oct 2026 10:00:00 GMT", 0, true},
		{"a date far ahead", "Fri, 20 Oct 2028 10:00:00 GMT", notify.SuspendAfter, true},
		{"text", "soon", 0, false},
		{"empty", "", 0, false},
		{"a fraction", "1.5", 0, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, ok := notify.ParseRetryAfter(tc.h, now)
			if got != tc.want || ok != tc.ok {
				t.Fatalf("ParseRetryAfter(%q) = %s, %t, want %s, %t", tc.h, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestAttemptValidate(t *testing.T) {
	t.Parallel()

	ok := notify.Attempt{
		ID: "dlv-1", Owner: "i-1", Version: 2, Now: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC),
		State: notify.DeliveryAbandoned, Attempts: 1, NextAt: time.Time{}, LastError: notify.ClassPermanent,
		DeliveredAt: time.Time{},
	}

	cases := []struct {
		name   string
		change func(a *notify.Attempt)
		valid  bool
	}{
		{"a class", func(*notify.Attempt) {}, true},
		{
			"the text of an error",
			func(a *notify.Attempt) { a.LastError = "dial tcp 10.0.0.1: https://user:pw@x" },
			false,
		},
		{"an unknown state", func(a *notify.Attempt) { a.State = "bogus" }, false},
		{"no owner", func(a *notify.Attempt) { a.Owner = "" }, false},
		{"no time", func(a *notify.Attempt) { a.Now = time.Time{} }, false},
		{"negative attempts", func(a *notify.Attempt) { a.Attempts = -1 }, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := ok
			tc.change(&a)

			if err := a.Validate(); tc.valid != (err == nil) || (!tc.valid && !errors.Is(err, notify.ErrInvalid)) {
				t.Fatalf("Validate = %v, want valid %t", err, tc.valid)
			}
		})
	}
}

func TestValidateSecrets(t *testing.T) {
	t.Parallel()

	s := notify.Sink{
		ID:         "snk-1",
		Scope:      "shop",
		Kind:       notify.SinkWebhook,
		Name:       "Ops",
		Platform:   "",
		Target:     "hooks.example.com",
		SecretRefs: []string{"itm-1", "itm-2"},
		CreatedBy:  "alice",
		Version:    0,
	}
	key := func(ref string, n int) notify.SinkSecret {
		return notify.SinkSecret{Ref: ref, Value: []byte(strings.Repeat("k", n))}
	}

	cases := []struct {
		name    string
		secrets []notify.SinkSecret
		ok      bool
	}{
		{"no new secret", nil, true},
		{"one value for each ref", []notify.SinkSecret{key("itm-1", 32), key("itm-2", 32)}, true},
		{"a value at the bound", []notify.SinkSecret{key("itm-1", notify.MaxSecretLen)}, true},
		{"a ref that the sink does not name", []notify.SinkSecret{key("itm-3", 32)}, false},
		{"two values for one ref", []notify.SinkSecret{key("itm-1", 32), key("itm-1", 32)}, false},
		{"an empty ref", []notify.SinkSecret{key("", 32)}, false},
		{"an empty value", []notify.SinkSecret{key("itm-1", 0)}, false},
		{"a value above the bound", []notify.SinkSecret{key("itm-1", notify.MaxSecretLen+1)}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := s.ValidateSecrets(tc.secrets)
			if tc.ok != (err == nil) || (!tc.ok && !errors.Is(err, notify.ErrInvalid)) {
				t.Fatalf("ValidateSecrets = %v, want valid %t", err, tc.ok)
			}
		})
	}
}

func TestSinkChangedNotice(t *testing.T) {
	t.Parallel()

	sub := notify.Subscription{
		ID: "sub-1", Scope: "shop", Sink: "snk-1", Filter: notify.Filter{Types: nil, Objects: nil, MinSeverity: 0},
		State: notify.SubscriptionStateActive, Reason: "", CreatedBy: "alice", Version: 3,
	}
	e := audit.Entry{
		At: time.Date(2026, 10, 20, 10, 0, 0, 0, time.UTC), Namespace: "shop", Subject: "bob", IDPSubject: "",
		TokenID: "", Action: "namespace:manage-notifications", Object: "shop", Version: "", ValuePath: "",
		ValueCount: nil, Changes: nil, SourceIP: "", UserAgent: "", Channel: audit.ChannelAPI,
		Kind: audit.KindChange, Outcome: audit.OutcomeSuccess, RequestID: "", ObjectID: "", PrevHash: "", Hash: "",
		Sequence: 0, FenceVersion: 0,
		Reason:        "",
		PrincipalKind: "",
		JobID:         "",
	}

	n := notify.SinkChangedNotice(sub, 4, e)
	if err := n.Validate(); err != nil {
		t.Fatalf("Validate = %v, want a valid notice", err)
	}

	want := []notify.Recipient{
		{Principal: "", Group: "", Admins: true},
		{Principal: "alice", Group: "", Admins: false},
	}
	if n.Type != notify.SubscriptionSuspended || n.ObjectID != "sub-1" || n.Subject != "bob" ||
		!n.At.Equal(e.At) || !slices.Equal(n.Recipients, want) || n.DedupeKey != "sub-1/sink-changed/4" {
		t.Fatalf("SinkChangedNotice = %+v, want the suspend of sub-1 by bob at sink version 4, "+
			"to the administrators and alice", n)
	}

	checkNoticeKeys(t, sub, e, n)
}

// checkNoticeKeys checks the object and the dedupe key of n, the notice
// of sub at the sink version 4: only a manager reads it, a repeat has the
// same dedupe key and a new ID, and a later change has its own key.
func checkNoticeKeys(t *testing.T, sub notify.Subscription, e audit.Entry, n notify.Notification) {
	t.Helper()

	if n.Object != notify.NotificationsObject("shop") ||
		!slices.Contains(notify.ScopeActions(n), "namespace:manage-notifications") {
		t.Fatalf("SinkChangedNotice object = %s, want the notifications of shop, which only a manager reads", n.Object)
	}

	again := notify.SinkChangedNotice(sub, 4, e)
	if again.ID == n.ID || again.DedupeKey != n.DedupeKey {
		t.Fatalf("a repeat of the notice = %s %s, want a new ID and the dedupe key %s", again.ID, again.DedupeKey,
			n.DedupeKey)
	}

	if later := notify.SinkChangedNotice(sub, 5, e); later.DedupeKey == n.DedupeKey {
		t.Fatalf("a later change has the dedupe key %s, want its own key", later.DedupeKey)
	}
}

func TestWebhookHost(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, url, want string
		err             error
	}{
		{"a path and a query", "https://Hooks.Example.com/a/SECRET?token=SECRET", "hooks.example.com", nil},
		{"a port", "https://hooks.example.com:8443/SECRET", "hooks.example.com:8443", nil},
		{"user information", "https://user:SECRET@hooks.example.com/x", "", egress.ErrRefused},
		{"a fragment", "https://hooks.example.com/x#SECRET", "", egress.ErrRefused},
		{"no path", "https://hooks.example.com", "hooks.example.com", nil},
		{"http", "http://hooks.example.com/SECRET", "", egress.ErrRefused},
		{"a URL that does not parse", "https://hooks.example.com/%zz SECRET", "", egress.ErrRefused},
		{"an empty value", "", "", egress.ErrRefused},
		{"an IPv6 literal", "https://[2606:4700:4700::1111]/SECRET", "[2606:4700:4700::1111]", nil},
		{"an IPv6 literal and a port", "https://[2606:4700:4700::1111]:8443/SECRET", "[2606:4700:4700::1111]:8443", nil},
		{"an IPv4 address", "https://8.8.8.8:443/SECRET", "8.8.8.8:443", nil},
		{"a zone in an IPv6 literal", "https://[fe80::1%25eth0]/SECRET", "", egress.ErrRefused},
		{"an IPv6 literal with text after it", "https://[2606:4700:4700::1111]x/SECRET", "", egress.ErrRefused},
		{"an IPv4 address in brackets", "https://[8.8.8.8]/SECRET", "", egress.ErrRefused},
		{"percent-encoding in the host", "https://hooks%2eexample.com/SECRET", "", egress.ErrRefused},
		{"a Unicode host", "https://h\u00f6\u00f6ks.example.com/SECRET", "", notify.ErrInvalid},
		{"the punycode form of a Unicode host", "https://xn--hks-pka.example.com/SECRET", "xn--hks-pka.example.com", nil},
		{"a right-to-left override", "https://hooks.example.com\u202e/SECRET", "", notify.ErrInvalid},
		{"a control character", "https://hooks.example.com\x00/SECRET", "", egress.ErrRefused},
		{"a semicolon", "https://hooks.example.com;SECRET/x", "", notify.ErrInvalid},
		{"sub-delimiters", "https://a_b!$&'()*+,=.example.com/x", "", notify.ErrInvalid},
		{"an empty port", "https://hooks.example.com:/x", "", notify.ErrInvalid},
		{"a port that is not a number", "https://hooks.example.com:80a/x", "", egress.ErrRefused},
		{"a port over 65535", "https://hooks.example.com:65536/x", "", notify.ErrInvalid},
		{"a port of 6 digits", "https://hooks.example.com:000080/x", "", notify.ErrInvalid},
		{"a port of zero", "https://hooks.example.com:0/x", "", notify.ErrInvalid},
		{"a port with a leading zero", "https://hooks.example.com:0443/x", "", notify.ErrInvalid},
		{"an IPv4-mapped IPv6 literal", "https://[::ffff:8.8.8.8]/x", "[::ffff:8.8.8.8]", nil},
		{"Slack", "https://hooks.slack.com/services/T0/B0/SECRET", "hooks.slack.com", nil},
		{"GovSlack", "https://hooks.slack-gov.com/services/T0/B0/SECRET", "hooks.slack-gov.com", nil},
		{"Teams", "https://contoso.webhook.office.com/webhookb2/SECRET", "contoso.webhook.office.com", nil},
		{"Teams workflow", "https://prod-27.westus.logic.azure.com:443/SECRET", "prod-27.westus.logic.azure.com:443", nil},
		{"Discord", "https://discord.com/api/webhooks/1/SECRET", "discord.com", nil},
		{"Google Chat", "https://chat.googleapis.com/v1/spaces/S/messages?key=SECRET", "chat.googleapis.com", nil},
		{"Mattermost", "https://mattermost.example.com/hooks/SECRET", "mattermost.example.com", nil},
		{"Matrix", "https://matrix.example.com:8448/_matrix/SECRET", "matrix.example.com:8448", nil},
		{"a label that starts with a hyphen", "https://-hooks.example.com/x", "", notify.ErrInvalid},
		{"an empty label", "https://hooks..example.com/x", "", notify.ErrInvalid},
		{"a trailing dot", "https://hooks.example.com./x", "", notify.ErrInvalid},
		{"no scheme", "//hooks.example.com/SECRET", "", egress.ErrRefused},
		{"a scheme that is not https", "http://hooks.example.com/SECRET", "", egress.ErrRefused},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := notify.WebhookHost(tc.url)
			if got != tc.want || !errors.Is(err, tc.err) || err != nil && strings.Contains(err.Error(), "SECRET") {
				t.Errorf("WebhookHost(%q) = %q, %v, want %q, %v with no secret in the error", tc.url, got, err, tc.want,
					tc.err)
			}
		})
	}
}

// legacySink returns a sink whose host passed the checks before the host
// allowlist of a write.
func legacySink(kind notify.SinkKind, target string) notify.Sink {
	s := notify.Sink{
		ID: "snk-1", Scope: "acme", Kind: kind, Name: "Ops", Platform: "", Target: target,
		SecretRefs: []string{"itm-1"}, CreatedBy: "ana", Version: 1,
	}
	if kind == notify.SinkChat {
		s.Platform = "mattermost"
	}

	if kind == notify.SinkWebhook {
		s.SecretRefs = []string{"itm-1", "itm-2"}
	}

	return s
}

func TestStoredHostFromBeforeTheAllowlist(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		sink notify.Sink
	}{
		{"a chat host in raw Unicode", legacySink(notify.SinkChat, "chat.müller.de")},
		{"a chat host with a trailing dot", legacySink(notify.SinkChat, "mm.example.com.")},
		{"a chat host with an underscore", legacySink(notify.SinkChat, "mm_chat.example.com")},
		{"a chat host with an empty port", legacySink(notify.SinkChat, "hooks.example.com:")},
		{"a chat host that ends a label with a hyphen", legacySink(notify.SinkChat, "a.b-.com")},
		{"a webhook host with an underscore", legacySink(notify.SinkWebhook, "hk_1.example.com")},
		{"a webhook host with a port of leading zeros", legacySink(notify.SinkWebhook, "hk.example.com:0443")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if err := tc.sink.Validate(); err != nil {
				t.Errorf("Validate = %v, want nil: a stored sink still lists, sends and restores", err)
			}

			if !tc.sink.HostNeedsUpdate() {
				t.Error("HostNeedsUpdate = false, want true")
			}

			err := tc.sink.ValidateWrite()
			if !errors.Is(err, notify.ErrInvalid) {
				t.Errorf("ValidateWrite = %v, want ErrInvalid", err)
			}

			if err != nil && strings.Count(err.Error(), notify.ErrInvalid.Error()) != 1 {
				t.Errorf("ValidateWrite = %q, want the sentinel once", err)
			}
		})
	}
}

func TestStoredHostThatTheEgressRulesRefuse(t *testing.T) {
	t.Parallel()

	for _, s := range []notify.Sink{
		legacySink(notify.SinkChat, "127.0.0.1"),
		legacySink(notify.SinkWebhook, "http://hooks.example.com"),
	} {
		if err := s.Validate(); !errors.Is(err, notify.ErrInvalid) {
			t.Errorf("Validate(%q) = %v, want ErrInvalid", s.Target, err)
		}
	}
}

func TestHostThatPassesTheAllowlist(t *testing.T) {
	t.Parallel()

	for _, s := range []notify.Sink{
		legacySink(notify.SinkChat, "mm.example.com:8443"),
		legacySink(notify.SinkWebhook, "hooks.example.com"),
	} {
		if err := s.ValidateWrite(); err != nil || s.HostNeedsUpdate() {
			t.Errorf("ValidateWrite(%q) = %v, flag = %v; want nil, false", s.Target, err, s.HostNeedsUpdate())
		}
	}
}
