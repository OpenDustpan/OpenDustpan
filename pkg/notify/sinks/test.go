package sinks

import (
	"context"
	"fmt"

	"opendustpan.org/opendustpan/internal/ident"
	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
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

// Test sends one fixed message through the sender of the kind of the sink id
// in ns, and appends the audit entry of the test. The message says that it is
// a test. The result never holds the URL, the network error or the answer of
// the receiver. It returns ErrNoSender for a kind with no sender,
// notify.ErrNotFound, namespace.ErrNotFound and namespace.ErrFenced.
func (s *Service) Test(ctx context.Context, c *Caller, ns namespace.Name, id string) (TestResult, error) {
	result, err := s.SendTest(ctx, ns, id)
	if err != nil {
		return TestResult{}, err
	}

	if err = s.RecordTest(ctx, c, ns, id, result); err != nil {
		return TestResult{}, err
	}

	return result, nil
}

// SendTest sends the test message of the sink id in ns and returns the result.
// It writes nothing: RecordTest appends the audit entry. An adapter that
// stores the answer of the request calls RecordTest after it knows the result.
func (s *Service) SendTest(ctx context.Context, ns namespace.Name, id string) (TestResult, error) {
	sink, err := s.Get(ctx, ns, id)
	if err != nil {
		return TestResult{}, err
	}

	sender, ok := s.senders[sink.Kind]
	if !ok {
		return TestResult{}, fmt.Errorf("test sink %s: %w: %s", id, ErrNoSender, sink.Kind)
	}

	return s.send(ctx, sender, sink)
}

// RecordTest appends the audit entry of the test of the sink id in ns. The
// entry holds the class of the result and no URL, name, secret or answer. It
// returns notify.ErrNotFound, namespace.ErrNotFound and namespace.ErrFenced.
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

// send opens the secrets of the sink, sends the test message, and clears the
// plaintext. A failure of the send is a result. A failure to open the secrets
// is an error: the server cannot say anything about the receiver.
func (s *Service) send(ctx context.Context, sender notify.Sender, sink notify.Sink) (TestResult, error) {
	sealed, err := s.store.SinkSecrets(ctx, sink.Scope, sink.ID)
	if err != nil {
		return TestResult{}, fmt.Errorf("test sink: read secrets: %w", err)
	}

	values, err := s.sealer.Open(ctx, sink, sealed)
	if err != nil {
		return TestResult{}, fmt.Errorf("test sink: open secrets: %w", err)
	}

	defer func() {
		for _, v := range values {
			clear(v)
		}
	}()

	to := sink.Target
	if sink.Kind == notify.SinkChat {
		to = string(values[0])
	}

	// Defense in depth: Sink.Validate checked the address when the sink was
	// made, and the sender checks it again and guards the dial. This check
	// stays so that a sink that a restore or a changed rule left behind never
	// reaches the sender with an address that the rules refuse now.
	if checkErr := checkAddress(sink, to); checkErr != nil {
		return testResult(checkErr), nil
	}

	ctx, cancel := context.WithTimeout(ctx, SendTimeout)
	defer cancel()

	n := notify.SinkTestNotification(sink, ident.NewID(notify.IDPrefix), s.now())

	err = sender.Send(ctx, notify.Message{
		Notification: n, Sink: sink, Secrets: secretsOf(sink, values), To: to, Actions: nil,
	})

	// A failure of the send is a result. notify.Class sorts it, and a timeout
	// is temporary.
	return testResult(err), nil
}

// secretsOf returns the signing keys that the sender gets: the opened values
// of a webhook sink, and none for any other kind.
func secretsOf(sink notify.Sink, values [][]byte) [][]byte {
	if sink.Kind != notify.SinkWebhook {
		return nil
	}

	return values
}

// checkAddress returns an error that matches egress.ErrRefused when the
// outbound rules refuse the address that the sink sends to.
func checkAddress(sink notify.Sink, to string) error {
	switch sink.Kind {
	case notify.SinkChat:
		host, err := notify.ChatHost(to)
		if err != nil {
			return err //nolint:wrapcheck // The error already matches egress.ErrRefused.
		}

		if host != sink.Target {
			return fmt.Errorf("%w: the host of the URL is not the target of the sink", egress.ErrRefused)
		}

		return nil
	default:
		if err := egress.CheckURL(to, egress.HTTPSOnly); err != nil {
			return err //nolint:wrapcheck // The error already matches egress.ErrRefused.
		}

		return nil
	}
}
