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
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/sinks"
)

// errNetwork stands for an error of the network that the sender returns. The
// tests wrap it with the address and the canary that the result must hide.
var errNetwork = errors.New("network error")

func TestTestSendsOneFixedMessage(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	created := makeHook(t, f)

	res, err := runTest(t.Context(), f, shop, created.Sink.ID)
	if err != nil || !res.Delivered || res.Class != "" || res.Message != sinks.TextDelivered {
		t.Fatalf("Test = %+v, %v, want a delivered result", res, err)
	}

	if len(f.hook.got) != 1 {
		t.Fatalf("sender got %d messages, want 1", len(f.hook.got))
	}

	checkTestMessage(t, f.hook.got[0], created)

	checkTestEntry(t, f, created)
}

// checkTestEntry checks the deadline of the send and the audit entry of a
// delivered test.
func checkTestEntry(t *testing.T, f fixture, created sinks.Created) {
	t.Helper()

	if f.hook.left <= 0 || f.hook.left > sinks.SendTimeout {
		t.Fatalf("time left on the send = %s, want a deadline of at most %s", f.hook.left, sinks.SendTimeout)
	}

	e := f.store.entries[len(f.store.entries)-1]
	if e.Action != "sink:test" || e.ObjectID != created.Sink.ID || e.Outcome != audit.OutcomeSuccess ||
		e.Reason != "class: delivered" || e.Validate() != nil {
		t.Fatalf("audit entry = %+v, want a valid sink:test entry with the outcome success", e)
	}
}

// checkTestMessage checks the message that the sender got for a test of the
// webhook sink created.
func checkTestMessage(t *testing.T, m notify.Message, created sinks.Created) {
	t.Helper()

	if m.To != "https://hooks.example.com/ops" || len(m.Secrets) != 1 || string(m.Secrets[0]) != created.SigningKey {
		t.Fatalf("message to %q with %d secrets, want the target and the signing key", m.To, len(m.Secrets))
	}

	n := m.Notification
	if n.Type != notify.SinkTest || n.Scope != shop || n.ObjectID != created.Sink.ID ||
		n.Severity != notify.SeverityInfo ||
		!strings.Contains(n.Summary, "test") ||
		n.Validate() != nil {
		t.Fatalf("notification = %+v, want a valid sink.test notification that says it is a test", n)
	}
}

// chatFixture returns a fixture whose service has a sender for chat sinks.
func chatFixture(t *testing.T) (fixture, *fakeSender) {
	t.Helper()

	f := newFixture(t, nil)
	chatSender := &fakeSender{kind: notify.SinkChat, err: nil, mu: sync.Mutex{}, got: nil, left: 0}

	svc, err := sinks.NewService(config(f.store, fakeNamespaces{notHome: false}, chatSender))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	f.svc = svc

	return f, chatSender
}

func TestTestChatSendsToTheURL(t *testing.T) {
	t.Parallel()

	f, chatSender := chatFixture(t)
	chat := makeChat(t, f)

	res, err := runTest(t.Context(), f, shop, chat.ID)
	if err != nil || !res.Delivered {
		t.Fatalf("Test = %+v, %v, want delivered", res, err)
	}

	m := chatSender.got[0]
	if m.To != "https://hooks.slack.com/services/T0/B0/"+canary || len(m.Secrets) != 0 || m.Sink.Platform != "slack" {
		t.Fatalf("message to %q with %d secrets, want the opened URL and no signing key", m.To, len(m.Secrets))
	}
}

// classCase is one error of the sender and the result that it must give.
type classCase struct {
	name      string
	err       error
	delivered bool
	class     string
	message   string
}

func classCases() []classCase {
	leaky := "dial tcp 203.0.113.9:443 " + canary
	network := fmt.Errorf("%w: %s", errNetwork, leaky)

	return []classCase{
		{"an accepted message", nil, true, "", sinks.TextDelivered},
		{
			"an address that the rules refuse", fmt.Errorf("%w: %s", egress.ErrRefused, leaky), false,
			notify.ClassRefused, sinks.TextRefused,
		},
		{
			"an answer for good", fmt.Errorf("%w: status 404 %s", notify.ErrPermanent, leaky), false,
			notify.ClassPermanent, sinks.TextRejected,
		},
		{
			"an answer for now", fmt.Errorf("%w: %s", notify.ErrTemporary, leaky), false,
			notify.ClassTemporary, sinks.TextFailed,
		},
		{
			"an answer with Retry-After", &notify.RetryAfterError{Wait: time.Hour}, false,
			notify.ClassTemporary, sinks.TextFailed,
		},
		{"an error of the network", network, false, notify.ClassTemporary, sinks.TextFailed},
		{
			"a timeout", fmt.Errorf("post: %w %s", context.DeadlineExceeded, leaky), false,
			notify.ClassTemporary, sinks.TextFailed,
		},
	}
}

func TestTestResultClasses(t *testing.T) {
	t.Parallel()

	for _, tc := range classCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, tc.err)
			created := makeHook(t, f)

			res, err := runTest(t.Context(), f, shop, created.Sink.ID)
			if err != nil {
				t.Fatalf("Test = %v, want a result", err)
			}

			if res.Delivered != tc.delivered || res.Class != tc.class || res.Message != tc.message {
				t.Fatalf("Test = %+v, want delivered %t, class %q, message %q", res, tc.delivered, tc.class, tc.message)
			}

			e := f.store.entries[len(f.store.entries)-1]
			if e.Action != "sink:test" || e.Validate() != nil || (e.Outcome == audit.OutcomeSuccess) != tc.delivered {
				t.Fatalf("audit entry = %+v, want a valid sink:test entry for delivered %t", e, tc.delivered)
			}

			// The error of the sender holds the address and a canary. Neither
			// reaches the result or the audit entry.
			assertHidden(t, created.SigningKey, fmt.Sprintf("%+v", res), fmt.Sprintf("%+v", e))
		})
	}
}

// assertHidden fails when one of the texts holds the address or the canary of
// the error of the sender, or the signing key.
func assertHidden(t *testing.T, key string, shown ...string) {
	t.Helper()

	for _, text := range shown {
		for _, secret := range []string{"203.0.113.9", canary, key} {
			if strings.Contains(text, secret) {
				t.Fatalf("%q holds %q", text, secret)
			}
		}
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

			_, err := runTest(t.Context(), f, shop, "snk-nothing")

			return err
		}, notify.ErrNotFound},
		{"a chat sink with no chat sender", func(t *testing.T, f fixture) error {
			t.Helper()

			_, err := runTest(t.Context(), f, shop, makeChat(t, f).ID)

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

			_, err = runTest(t.Context(), f, shop, got.Sink.ID)

			return err
		}, sinks.ErrNoSender},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)
			before := len(f.store.entries)

			if err := tc.run(t, f); !errors.Is(err, tc.want) {
				t.Fatalf("Test = %v, want %v", err, tc.want)
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

func TestTestInAnUnknownNamespace(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)

	if _, err := runTest(t.Context(), f, "nowhere", "snk-1"); err == nil {
		t.Fatal("Test = nil, want an error")
	}

	if len(f.hook.got) != 0 || len(f.store.entries) != 0 {
		t.Fatalf("sender got %d messages and %d entries were written, want none", len(f.hook.got), len(f.store.entries))
	}
}

// TestTestChecksTheAddressAgain proves that a sink that the store holds with
// an address the rules refuse now never reaches the sender.
func TestTestChecksTheAddressAgain(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		target string
	}{
		{"a webhook on loopback", "https://127.0.0.1/hook"},
		{"a webhook on the metadata address", "https://169.254.169.254/"},
		{"a webhook over http", "http://hooks.example.com/"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFixture(t, nil)
			created := makeHook(t, f)

			s := f.store.sinks[created.Sink.ID]
			s.Target = tc.target
			f.store.sinks[s.ID] = s

			res, err := runTest(t.Context(), f, shop, s.ID)
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

	f, chatSender := chatFixture(t)
	chat := makeChat(t, f)

	s := f.store.sinks[chat.ID]
	s.Target = "other.example.com"
	f.store.sinks[s.ID] = s

	res, err := runTest(t.Context(), f, shop, chat.ID)
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

	res, err := runTest(t.Context(), f, shop, one.Sink.ID)
	if err == nil {
		t.Fatalf("Test = %+v, nil, want an error", res)
	}

	if len(f.hook.got) != 0 || len(f.store.entries) != before {
		t.Fatalf("sender got %d messages and %d entries were written, want none", len(f.hook.got),
			len(f.store.entries)-before)
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

	svc, err := sinks.NewService(config(f.store, fakeNamespaces{notHome: false}, k))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	f.svc = svc
	created := makeHook(t, f)

	if _, err = runTest(t.Context(), f, shop, created.Sink.ID); err != nil {
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

// runTest sends the test message of the sink and records its result, as the
// API adapter does.
func runTest(ctx context.Context, f fixture, ns namespace.Name, id string) (sinks.TestResult, error) {
	return runTestWith(ctx, f.svc, f.caller, ns, id)
}

// runTestWith is runTest for a service and a caller of its own.
func runTestWith(
	ctx context.Context, svc *sinks.Service, c *sinks.Caller, ns namespace.Name, id string,
) (sinks.TestResult, error) {
	res, err := svc.SendTest(ctx, c, ns, id)
	if err != nil {
		return sinks.TestResult{}, fmt.Errorf("send test: %w", err)
	}

	if err = svc.RecordTest(ctx, c, ns, id, res); err != nil {
		return sinks.TestResult{}, fmt.Errorf("record test: %w", err)
	}

	return res, nil
}

// platformSender is a chat sender that serves one platform only.
type platformSender struct {
	fakeSender

	platform string
}

func (p *platformSender) Serves(s notify.Sink) bool { return s.Platform == p.platform }

func TestTestChatSenderThatServesOtherPlatformsOnly(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	other := &platformSender{
		kind: notify.SinkChat, err: nil, mu: sync.Mutex{}, got: nil, left: 0,
		platform: "teams",
	}

	svc, err := sinks.NewService(config(f.store, fakeNamespaces{notHome: false}, other))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	f.svc = svc
	id := makeChat(t, f).ID
	before := len(f.store.entries)

	// The sink is a Slack sink and the sender serves Teams: the answer is that
	// the server has no sender, and nothing is sent or audited.
	if _, err = runTest(t.Context(), f, shop, id); !errors.Is(err, sinks.ErrNoSender) {
		t.Fatalf("Test = %v, want ErrNoSender", err)
	}

	if len(other.got) != 0 || len(f.store.entries) != before {
		t.Errorf("sent %d messages, audit entries %d -> %d, want none of both", len(other.got), before,
			len(f.store.entries))
	}
}

// hostSender is a chat sender that accepts one host for Slack only.
type hostSender struct {
	fakeSender
}

func (*hostSender) CheckHost(platform, host string) error {
	if platform == "slack" && host != "hooks.slack.com" {
		return notify.ErrInvalid
	}

	return nil
}

func TestChatHostIsCheckedWhenAPersonSavesTheSink(t *testing.T) {
	t.Parallel()

	f := newFixture(t, nil)
	sender := &hostSender{kind: notify.SinkChat, err: nil, mu: sync.Mutex{}, got: nil, left: 0}

	svc, err := sinks.NewService(config(f.store, fakeNamespaces{notHome: false}, sender))
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}

	f.svc = svc

	for name, tc := range map[string]struct {
		platform, url string
		ok            bool
	}{
		"the host of the platform":           {"slack", "https://hooks.slack.com/services/T0/B0/" + canary, true},
		"another host for the platform":      {"slack", "https://hooks.example.com/services/T0/B0/" + canary, false},
		"another platform has no host rule":  {"teams", "https://hooks.example.com/x/" + canary, true},
		"a host that ends with the platform": {"slack", "https://hooks.slack.com.evil.example/x/" + canary, false},
	} {
		_, createErr := f.svc.Create(t.Context(), f.caller, shop, sinks.New{
			Kind: notify.SinkChat, Platform: tc.platform, Name: "Chat", Target: "", URL: tc.url,
		})
		if tc.ok != (createErr == nil) || (!tc.ok && !errors.Is(createErr, notify.ErrInvalid)) {
			t.Errorf("%s: Create = %v, want ok %v", name, createErr, tc.ok)
		}

		if createErr != nil && strings.Contains(createErr.Error(), canary) {
			t.Errorf("%s: the error holds the token", name)
		}
	}

	// A replace with a URL of another host fails the same way.
	made := makeChat(t, f)

	url := "https://hooks.example.com/services/T0/B0/" + canary
	_, err = f.svc.Replace(t.Context(), f.caller, shop, made.ID,
		sinks.Change{Version: made.Version, Name: "Chat", Target: nil, URL: &url})
	if !errors.Is(err, notify.ErrInvalid) {
		t.Errorf("Replace with another host = %v, want ErrInvalid", err)
	}
}
