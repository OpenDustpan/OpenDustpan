package sinks

import (
	"context"
	"errors"
	"fmt"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/idempotency"
	"opendustpan.org/opendustpan/pkg/namespace"
	"opendustpan.org/opendustpan/pkg/notify"
)

// Test result texts. They are fixed: a result never holds the URL, the
// network error, the body of the answer or a secret.
const (
	TextDelivered = "The receiver accepted the test message."
	TextRefused   = "The server does not send to this address. Check the URL against the outbound address rules."
	TextRejected  = "The receiver refused the test message. Check the URL and the signing key."
	TextFailed    = "The test message did not arrive. Try again later."
)

// TestResult is the result of the test of a sink. Class is empty when the
// receiver accepted the message, and else one of notify.ClassRefused,
// notify.ClassPermanent and notify.ClassTemporary. Message is one of the
// fixed texts above.
type TestResult struct {
	Delivered bool
	Class     string
	Message   string
}

// testResult sorts the error of a send. It never keeps the error text.
func testResult(err error) TestResult {
	if err == nil {
		return TestResult{Delivered: true, Class: "", Message: TextDelivered}
	}

	switch notify.Class(err) {
	case notify.ClassRefused:
		return TestResult{Delivered: false, Class: notify.ClassRefused, Message: TextRefused}
	case notify.ClassPermanent:
		return TestResult{Delivered: false, Class: notify.ClassPermanent, Message: TextRejected}
	default:
		return TestResult{Delivered: false, Class: notify.ClassTemporary, Message: TextFailed}
	}
}

// SendTest sends the test message of the sink id in ns and returns the result.
// It sends only after these steps, and a step that fails sends nothing: the
// sink and its sender exist; the namespace is primary on this installation; the
// limits take the test; the secrets open and the address passes the outbound
// rules, which refuses with a result and not an error; and the audit entry
// that says the test started is appended under the fence of the namespace.
// RecordTest appends the second entry, with the class of the result. An
// adapter that stores the answer of the request calls RecordTest after it
// knows the result.
func (s *Service) SendTest(ctx context.Context, c *Caller, ns namespace.Name, id string) (TestResult, error) {
	sink, err := s.Get(ctx, ns, id)
	if err != nil {
		return TestResult{}, err
	}

	sender, ok := s.senders[sink.Kind]
	if !ok || !serves(sender, sink) {
		return TestResult{}, fmt.Errorf("test sink %s: %w: %s", id, ErrNoSender, sink.Kind)
	}

	fence, err := s.fence(ctx, ns)
	if err != nil {
		return TestResult{}, err
	}

	// The limits come before the decryption, the address check and the first
	// write, so that a refused request leaves no entry and a request that the
	// address check refuses counts against the limit too: a caller cannot write
	// audit entries without bound.
	if wait := s.limiter.take(ns, id, s.now()); wait > 0 {
		return TestResult{}, &RateLimitedError{RetryAfter: wait}
	}

	to, dest, err := s.open(ctx, sink)
	if err != nil {
		return TestResult{}, err
	}

	defer dest.Clear()

	// Defense in depth: Sink.Validate and Service.Create checked the address
	// when the sink was made, and the sender checks it again and guards the
	// dial. This check stays so that a sink that a restore, a changed rule or
	// a changed port setting left behind never reaches the sender with an
	// address that the rules refuse now. The check opens no connection and
	// needs no audit entry before it.
	if checkErr := s.checkAddress(sink, to); checkErr != nil {
		return testResult(checkErr), nil
	}

	// The entry is appended without the idempotency completion of the request:
	// RecordTest stores the answer, when the result is known.
	started := s.entryWith(c, ns, id, "test", audit.OutcomeSuccess)
	started.Reason = "started"

	if err = s.store.RecordSinkTest(idempotency.WithCompletion(ctx, nil), id, fence, started); err != nil {
		return TestResult{}, fmt.Errorf("test sink: %w", err)
	}

	return s.send(ctx, sender, sink, to, dest.Keys), nil
}

// serves reports whether sender sends to sink. A sender that serves some
// platforms of its kind says which, with a method Serves. A sink of another
// platform has no sender, as a sink of a kind with no sender has. Any other
// sender serves each sink of its kind.
func serves(sender notify.Sender, sink notify.Sink) bool {
	p, ok := sender.(interface{ Serves(sink notify.Sink) bool })

	return !ok || p.Serves(sink)
}

// RecordTest appends the audit entry with the result of the test of the sink
// id in ns. The entry holds the class of the result and no URL, name, secret
// or answer. It returns notify.ErrNotFound, namespace.ErrNotFound,
// namespace.ErrNotHome and namespace.ErrFenced.
func (s *Service) RecordTest(ctx context.Context, c *Caller, ns namespace.Name, id string, r TestResult) error {
	fence, err := s.fence(ctx, ns)
	if err != nil {
		return err
	}

	outcome := audit.OutcomeSuccess
	if !r.Delivered {
		outcome = audit.OutcomeFailure
	}

	e := s.entryWith(c, ns, id, "test", outcome)
	e.Reason = "class: " + classOf(r)

	if err = s.store.RecordSinkTest(ctx, id, fence, e); err != nil {
		return fmt.Errorf("test sink: %w", err)
	}

	return nil
}

func classOf(r TestResult) string {
	if r.Delivered {
		return "delivered"
	}

	return r.Class
}

// open opens the secret items of the sink and returns the address that the
// sink sends to and the signing keys of a webhook sink. The caller clears the
// keys. The URL of a sink is opened here and in the dispatcher only, for the
// one send. A failure to open the secrets is an error: the server cannot say
// anything about the receiver. The error matches notify.ErrNoURL.
func (s *Service) open(ctx context.Context, sink notify.Sink) (string, notify.Destination, error) {
	sealed, err := s.store.SinkSecrets(ctx, sink.Scope, sink.ID)
	if err != nil {
		return "", notify.Destination{}, fmt.Errorf("test sink: read secrets: %w", err)
	}

	dest, err := s.sealer.OpenDestination(ctx, sink, sealed)
	if err != nil {
		return "", notify.Destination{}, fmt.Errorf("test sink: open secrets: %w", err)
	}

	if sink.Kind == notify.SinkEmail {
		return sink.Target, dest, nil
	}

	return dest.URL, dest, nil
}

// send sends the test message. A failure of the send is a result: notify.Class
// sorts it, and a timeout is temporary.
func (s *Service) send(
	ctx context.Context, sender notify.Sender, sink notify.Sink, to string, keys [][]byte,
) TestResult {
	ctx, cancel := context.WithTimeout(ctx, SendTimeout)
	defer cancel()

	n := notify.SinkTestNotification(sink, ident.NewID(notify.IDPrefix), s.now())

	err := sender.Send(ctx, notify.Message{
		Notification: n, Sink: sink, Secrets: keys, To: to, Actions: nil,
	})

	return testResult(err)
}

// checkAddress returns an error that matches egress.ErrRefused when the
// outbound rules refuse the address that the sink sends to: the scheme, the
// address ranges and the port of the URL, for each kind, and for a chat sink
// or a webhook sink also a host that is not the target of the sink.
func (s *Service) checkAddress(sink notify.Sink, to string) error {
	if err := egress.CheckURL(to, egress.HTTPSOnly); err != nil {
		return fmt.Errorf("check the address: %w", err)
	}

	if err := egress.CheckPort(to, s.ports); err != nil {
		return fmt.Errorf("check the address: %w", err)
	}

	// The test is a write-side check: it applies the host allowlist that a
	// stored sink from before the allowlist does not pass.
	if sink.Kind == notify.SinkEmail {
		return nil
	}

	hostOf := notify.ChatHost
	if sink.Kind == notify.SinkWebhook {
		hostOf = notify.WebhookHost
	}

	host, err := hostOf(to)
	if err != nil {
		// The test is a write-side check: a host that fails the allowlist is
		// a refused address, as one that the egress rules refuse.
		if !errors.Is(err, egress.ErrRefused) {
			err = fmt.Errorf("%w: %w", egress.ErrRefused, err)
		}

		return fmt.Errorf("check the address: %w", err)
	}

	if host != sink.Target {
		return fmt.Errorf("%w: the host of the URL is not the target of the sink", egress.ErrRefused)
	}

	return nil
}
