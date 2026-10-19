package notifytest

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/notify"
)

// Behavior is what the receiver of a SenderHarness does with the next
// attempt.
type Behavior int

// The behaviors of a receiver.
const (
	// ReceiverAccepts takes the message.
	ReceiverAccepts Behavior = iota
	// ReceiverRefusesForGood refuses the message with an answer that will
	// not change, for example an unknown address.
	ReceiverRefusesForGood
	// ReceiverFailsForNow fails with an answer that may change, for example
	// a server error.
	ReceiverFailsForNow
	// ReceiverAsksToWait fails and asks the sender to wait AskedWait before
	// the next attempt.
	ReceiverAsksToWait
)

// AskedWait is the wait that a receiver asks for under ReceiverAsksToWait.
const AskedWait = 90 * time.Second

// SenderHarness is one sender with a receiver that the case controls.
type SenderHarness struct {
	// Sender is the adapter under test.
	Sender notify.Sender
	// Message is a valid message for Sender that the receiver can take. Its
	// secrets and its address are the values that no error may repeat.
	Message notify.Message
	// Behave sets what the receiver does with each attempt after the call.
	// A receiver accepts until the first call.
	Behave func(Behavior)
	// Received returns how many attempts reached the receiver.
	Received func() int
}

// SenderFactory returns a fresh harness for one case.
type SenderFactory func(t *testing.T) SenderHarness

// RunSender executes the conformance suite of notify.Sender against
// harnesses from newHarness. An adapter runs it with a receiver that
// speaks its protocol:
//
//	func TestSenderConformance(t *testing.T) {
//		t.Parallel()
//		notifytest.RunSender(t, func(t *testing.T) notifytest.SenderHarness { return newHarness(t) })
//	}
func RunSender(t *testing.T, newHarness SenderFactory) {
	t.Helper()

	cases := []struct {
		name string
		run  func(t *testing.T, h SenderHarness)
	}{
		{"the kind is the kind of the message", testSenderKind},
		{"a receiver that accepts gets one attempt and no error", testSenderAccepts},
		{"an answer that will not change is ErrPermanent", testSenderPermanent},
		{"an answer that may change is ErrTemporary", testSenderTemporary},
		{"a request to wait is a RetryAfterError with that wait", testSenderRetryAfter},
		{"a canceled context sends nothing and is not permanent", testSenderCanceled},
		{"no error repeats an address or a secret", testSenderNoLeak},
		{"a message of another kind is ErrPermanent and sends nothing", testSenderWrongKind},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			tc.run(t, newHarness(t))
		})
	}
}

func testSenderKind(t *testing.T, h SenderHarness) {
	t.Helper()

	if got, want := h.Sender.Kind(), h.Message.Sink.Kind; got != want {
		t.Fatalf("Kind() = %q, want %q", got, want)
	}
}

func testSenderAccepts(t *testing.T, h SenderHarness) {
	t.Helper()

	if err := h.Sender.Send(t.Context(), h.Message); err != nil {
		t.Fatalf("Send to a receiver that accepts: %v", err)
	}

	if got := h.Received(); got != 1 {
		t.Fatalf("attempts at the receiver = %d, want 1", got)
	}
}

func testSenderPermanent(t *testing.T, h SenderHarness) {
	t.Helper()
	h.Behave(ReceiverRefusesForGood)

	err := h.Sender.Send(t.Context(), h.Message)
	if !errors.Is(err, notify.ErrPermanent) {
		t.Fatalf("Send = %v, want ErrPermanent", err)
	}

	if errors.Is(err, notify.ErrTemporary) {
		t.Fatalf("Send = %v, also matches ErrTemporary", err)
	}

	if got := notify.Class(err); got != notify.ClassPermanent {
		t.Fatalf("Class = %q, want %q", got, notify.ClassPermanent)
	}
}

func testSenderTemporary(t *testing.T, h SenderHarness) {
	t.Helper()
	h.Behave(ReceiverFailsForNow)

	err := h.Sender.Send(t.Context(), h.Message)
	if !errors.Is(err, notify.ErrTemporary) {
		t.Fatalf("Send = %v, want ErrTemporary", err)
	}

	if errors.Is(err, notify.ErrPermanent) || errors.Is(err, egress.ErrRefused) {
		t.Fatalf("Send = %v, also matches a class that stops the delivery", err)
	}
}

func testSenderRetryAfter(t *testing.T, h SenderHarness) {
	t.Helper()
	h.Behave(ReceiverAsksToWait)

	err := h.Sender.Send(t.Context(), h.Message)
	if !errors.Is(err, notify.ErrTemporary) {
		t.Fatalf("Send = %v, want ErrTemporary", err)
	}

	var ra *notify.RetryAfterError
	if !errors.As(err, &ra) {
		t.Fatalf("Send = %v, want a RetryAfterError", err)
	}

	if ra.Wait != AskedWait {
		t.Fatalf("RetryAfterError.Wait = %s, want %s", ra.Wait, AskedWait)
	}
}

func testSenderCanceled(t *testing.T, h SenderHarness) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := h.Sender.Send(ctx, h.Message)
	if err == nil {
		t.Fatal("Send with a canceled context = nil error")
	}

	if errors.Is(err, notify.ErrPermanent) || errors.Is(err, egress.ErrRefused) {
		t.Fatalf("Send with a canceled context = %v, want an error that allows a retry", err)
	}

	if got := h.Received(); got != 0 {
		t.Fatalf("attempts at the receiver = %d, want 0", got)
	}
}

func testSenderNoLeak(t *testing.T, h SenderHarness) {
	t.Helper()

	secrets := make([]string, 0, len(h.Message.Secrets)+1)
	for _, s := range h.Message.Secrets {
		secrets = append(secrets, string(s))
	}

	secrets = append(secrets, h.Message.To)

	for _, b := range []Behavior{ReceiverRefusesForGood, ReceiverFailsForNow, ReceiverAsksToWait} {
		h.Behave(b)

		err := h.Sender.Send(t.Context(), h.Message)
		if err == nil {
			t.Fatalf("Send under behavior %d = nil error", b)
		}

		for _, s := range secrets {
			if s != "" && strings.Contains(err.Error(), s) {
				t.Fatalf("Send under behavior %d: the error text repeats a secret or an address", b)
			}
		}
	}
}

func testSenderWrongKind(t *testing.T, h SenderHarness) {
	t.Helper()

	m := h.Message
	if m.Sink.Kind == notify.SinkEmail {
		m.Sink.Kind = notify.SinkChat
	} else {
		m.Sink.Kind = notify.SinkEmail
	}

	if err := h.Sender.Send(t.Context(), m); !errors.Is(err, notify.ErrPermanent) {
		t.Fatalf("Send of another kind = %v, want ErrPermanent", err)
	}

	if got := h.Received(); got != 0 {
		t.Fatalf("attempts at the receiver = %d, want 0", got)
	}
}
