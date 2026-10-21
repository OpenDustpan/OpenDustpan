package sinks_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
)

func TestTestSendsOneFixedMessage(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	created := makeHook(t, f)

	res, err := f.svc.Test(t.Context(), f.caller, shop, created.Sink.ID)
	if err != nil || !res.Delivered || res.Class != "" || res.Message != sinks.TextDelivered {
		t.Fatalf("Test = %+v, %v, want a delivered result", res, err)
	}

	if len(f.hook.got) != 1 {
		t.Fatalf("sender got %d messages, want 1", len(f.hook.got))
	}

	m := f.hook.got[0]

	if m.To != "https://hooks.example.com/ops" || len(m.Secrets) != 1 || string(m.Secrets[0]) != created.SigningKey {
		t.Fatalf("message to %q with %d secrets, want the target and the signing key", m.To, len(m.Secrets))
	}

	n := m.Notification
	if n.Type != notify.SinkTest || n.Scope != shop || n.ObjectID != created.Sink.ID || n.Severity != notify.SeverityInfo ||
		!strings.Contains(n.Summary, "test") || n.Validate() != nil {
		t.Fatalf("notification = %+v, want a valid sink.test notification that says it is a test", n)
	}

	if f.hook.left <= 0 || f.hook.left > sinks.SendTimeout {
		t.Fatalf("time left on the send = %s, want a deadline of at most %s", f.hook.left, sinks.SendTimeout)
	}

	e := f.store.entries[len(f.store.entries)-1]
	if e.Action != "sink:test" || e.ObjectID != created.Sink.ID || e.Outcome != audit.OutcomeSuccess ||
		e.Reason != "class: delivered" || e.Validate() != nil {
		t.Fatalf("audit entry = %+v, want a valid sink:test entry with the outcome success", e)
	}
}

func TestTestChatSendsToTheURL(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	chatSender := &fakeSender{kind: notify.SinkChat, err: nil, mu: sync.Mutex{}, got: nil, left: 0}

	svc, err := sinks.NewService(sinks.Config{
		Store: f.store, Sealer: f.svcSealer(), Namespaces: fakeNamespaces{}, Senders: []notify.Sender{chatSender},
		Now: func() time.Time { return time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	f.svc = svc
	chat := makeChat(t, f)

	res, err := svc.Test(t.Context(), f.caller, shop, chat.ID)
	if err != nil || !res.Delivered {
		t.Fatalf("Test = %+v, %v, want delivered", res, err)
	}

	m := chatSender.got[0]
	if m.To != "https://hooks.slack.com/services/T0/B0/"+canary || len(m.Secrets) != 0 || m.Sink.Platform != "slack" {
		t.Fatalf("message to %q with %d secrets, want the opened URL and no signing key", m.To, len(m.Secrets))
	}
}

func TestTestResultClasses(t *testing.T) {
	t.Parallel()

	leaky := "dial tcp 203.0.113.9:443 " + canary

	cases := []struct {
		name      string
		err       error
		delivered bool
		class     string
		message   string
		outcome   audit.Outcome
	}{
		{"an accepted message", nil, true, "", sinks.TextDelivered, audit.OutcomeSuccess},
		{
			"an address that the rules refuse", fmt.Errorf("%w: %s", egress.ErrRefused, leaky), false,
			notify.ClassRefused, sinks.TextRefused, audit.OutcomeFailure,
		},
		{
			"an answer for good", fmt.Errorf("%w: status 404 %s", notify.ErrPermanent, leaky), false,
			notify.ClassPermanent, sinks.TextRejected, audit.OutcomeFailure,
		},
		{
			"an answer for now", fmt.Errorf("%w: %s", notify.ErrTemporary, leaky), false,
			notify.ClassTemporary, sinks.TextFailed, audit.OutcomeFailure,
		},
		{
			"an answer with Retry-After", &notify.RetryAfterError{Wait: time.Hour}, false,
			notify.ClassTemporary, sinks.TextFailed, audit.OutcomeFailure,
		},
		{"an error of the network", errors.New(leaky), false, notify.ClassTemporary, sinks.TextFailed, audit.OutcomeFailure},
		{
			"a timeout", fmt.Errorf("post: %w %s", context.DeadlineExceeded, leaky), false,
			notify.ClassTemporary, sinks.TextFailed, audit.OutcomeFailure,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, tc.err)
			created := makeHook(t, f)

			res, err := f.svc.Test(t.Context(), f.caller, shop, created.Sink.ID)
			if err != nil {
				t.Fatalf("Test = %v, want a result", err)
			}

			if res.Delivered != tc.delivered || res.Class != tc.class || res.Message != tc.message {
				t.Fatalf("Test = %+v, want delivered %t, class %q, message %q", res, tc.delivered, tc.class, tc.message)
			}

			e := f.store.entries[len(f.store.entries)-1]
			if e.Outcome != tc.outcome || e.Action != "sink:test" || e.Validate() != nil {
				t.Fatalf("audit entry = %+v, want a valid sink:test entry with the outcome %s", e, tc.outcome)
			}

			// The error of the sender holds the address and a canary. Neither
			// reaches the result or the audit entry.
			for _, shown := range []string{fmt.Sprintf("%+v", res), fmt.Sprintf("%+v", e)} {
				for _, secret := range []string{"203.0.113.9", canary, created.SigningKey} {
					if strings.Contains(shown, secret) {
						t.Fatalf("%q holds %q", shown, secret)
					}
				}
			}
		})
	}
}

func TestTestRefusals(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		run  func(t *testing.T, f fixture) error
		want error
	}{
		{"an unknown sink", func(t *testing.T, f fixture) error {
			t.Helper()

			_, err := f.svc.Test(t.Context(), f.caller, shop, "snk-nothing")

			return err
		}, notify.ErrNotFound},
		{"a chat sink with no chat sender", func(t *testing.T, f fixture) error {
			t.Helper()

			s := makeChat(t, f)
			_, err := f.svc.Test(t.Context(), f.caller, shop, s.ID)

			return err
		}, sinks.ErrNoSender},
		{"an email sink with no email sender", func(t *testing.T, f fixture) error {
			t.Helper()

			got, err := f.svc.Create(t.Context(), f.caller, shop, sinks.New{
				Kind: notify.SinkEmail, Platform: "", Name: "Mail", Target: "ops@example.com", URL: "",
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}

			_, err = f.svc.Test(t.Context(), f.caller, shop, got.Sink.ID)

			return err
		}, sinks.ErrNoSender},
		{"an unknown namespace", func(t *testing.T, f fixture) error {
			t.Helper()

			_, err := f.svc.Test(t.Context(), f.caller, "nowhere", "snk-1")

			return err
		}, notify.ErrNotFound},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)
			before := len(f.store.entries)

			err := tc.run(t, f)
			if tc.name != "an unknown namespace" && !errors.Is(err, tc.want) {
				t.Fatalf("Test = %v, want %v", err, tc.want)
			}

			if tc.name == "an unknown namespace" && err == nil {
				t.Fatal("Test = nil, want an error")
			}

			if len(f.hook.got) != 0 {
				t.Fatalf("sender got %d messages, want none", len(f.hook.got))
			}

			if len(f.store.entries) > before+1 {
				t.Fatalf("audit entries grew by %d, want at most the create", len(f.store.entries)-before)
			}
		})
	}
}

// TestTestChecksTheAddressAgain proves that a sink that the store holds with
// an address the rules refuse now never reaches the sender.
func TestTestChecksTheAddressAgain(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		change func(s *notify.Sink)
	}{
		{"a webhook on loopback", func(s *notify.Sink) { s.Target = "https://127.0.0.1/hook" }},
		{"a webhook on the metadata address", func(s *notify.Sink) { s.Target = "https://169.254.169.254/" }},
		{"a webhook over http", func(s *notify.Sink) { s.Target = "http://hooks.example.com/" }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)
			created := makeHook(t, f)

			s := f.store.sinks[created.Sink.ID]
			tc.change(&s)
			f.store.sinks[s.ID] = s

			res, err := f.svc.Test(t.Context(), f.caller, shop, s.ID)
			if err != nil || res.Delivered || res.Class != notify.ClassRefused || res.Message != sinks.TextRefused {
				t.Fatalf("Test = %+v, %v, want the class refused", res, err)
			}

			if len(f.hook.got) != 0 {
				t.Fatalf("sender got %d messages, want none", len(f.hook.got))
			}
		})
	}
}

// TestTestChatHostMustBeTheTarget proves that a chat sink whose stored URL has
// another host than its target never reaches the sender.
func TestTestChatHostMustBeTheTarget(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	chatSender := &fakeSender{kind: notify.SinkChat, err: nil, mu: sync.Mutex{}, got: nil, left: 0}

	svc, err := sinks.NewService(sinks.Config{
		Store: f.store, Sealer: f.svcSealer(), Namespaces: fakeNamespaces{}, Senders: []notify.Sender{chatSender}, Now: nil,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	f.svc = svc
	chat := makeChat(t, f)

	s := f.store.sinks[chat.ID]
	s.Target = "other.example.com"
	f.store.sinks[s.ID] = s

	res, err := svc.Test(t.Context(), f.caller, shop, chat.ID)
	if err != nil || res.Class != notify.ClassRefused {
		t.Fatalf("Test = %+v, %v, want the class refused", res, err)
	}

	if len(chatSender.got) != 0 {
		t.Fatalf("sender got %d messages, want none", len(chatSender.got))
	}
}

// TestTestSecretOfAnotherSink proves that a secret item sealed for another
// sink never opens, and then no message goes out and no entry is written.
func TestTestSecretOfAnotherSink(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	one := makeHook(t, f)
	two := makeHook(t, f)

	f.store.secrets[one.Sink.ID] = f.store.secrets[two.Sink.ID]
	before := len(f.store.entries)

	res, err := f.svc.Test(t.Context(), f.caller, shop, one.Sink.ID)
	if err == nil {
		t.Fatalf("Test = %+v, nil, want an error", res)
	}

	if len(f.hook.got) != 0 || len(f.store.entries) != before {
		t.Fatalf("sender got %d messages and %d entries were written, want none", len(f.hook.got), len(f.store.entries)-before)
	}

	if strings.Contains(err.Error(), one.SigningKey) || strings.Contains(err.Error(), two.SigningKey) {
		t.Fatalf("error %q holds a signing key", err)
	}
}

// keeper keeps the slice of secrets that it got, to check that the service
// clears the plaintext after the send.
type keeper struct {
	secrets [][]byte
}

func (*keeper) Kind() notify.SinkKind { return notify.SinkWebhook }

func (k *keeper) Send(_ context.Context, m notify.Message) error {
	k.secrets = m.Secrets

	return nil
}

func TestTestClearsThePlaintext(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	k := &keeper{secrets: nil}

	svc, err := sinks.NewService(sinks.Config{
		Store: f.store, Sealer: f.svcSealer(), Namespaces: fakeNamespaces{}, Senders: []notify.Sender{k}, Now: nil,
	})
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	f.svc = svc
	created := makeHook(t, f)

	if _, err = svc.Test(t.Context(), f.caller, shop, created.Sink.ID); err != nil {
		t.Fatalf("Test: %v", err)
	}

	if len(k.secrets) != 1 {
		t.Fatalf("sender got %d secrets, want 1", len(k.secrets))
	}

	for _, b := range k.secrets[0] {
		if b != 0 {
			t.Fatal("the plaintext of the signing key is still in memory after the test")
		}
	}
}
