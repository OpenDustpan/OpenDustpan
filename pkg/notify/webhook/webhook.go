// Package webhook is the notify.Sender for the sink kind notify.SinkWebhook.
// It posts a notification as JSON to the https URL of the sink and signs the
// request with the active secrets of the sink, so a receiver can check the
// sender, drop a second delivery of one notification and refuse a replay.
//
// A request carries three headers: notify.HeaderNotificationID,
// notify.HeaderTimestamp and notify.HeaderSignature. notify.Verify shows a
// receiver how to check them. The body has camelCase fields; see Encode.
//
// The sender posts through egresshttp.Client with no redirect and https
// only, so a private, loopback or link-local address fails also after a DNS
// answer that changed, and a redirect is never followed. It reads at most
// egress.MaxBodyBytes of an answer. An error never holds the URL, the
// network error, the body of an answer or a secret.
package webhook

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/egress/egresshttp"
	"opendustpan.org/opendustpan/pkg/notify"
	"opendustpan.org/opendustpan/pkg/notify/internal/post"
)

// contentType is the media type of the body.
const contentType = "application/json"

// op names a send in the error texts.
const op = "post webhook"

// Sender posts notifications to webhook sinks. It is safe for concurrent
// use. Call Close when done, to close the idle connections.
type Sender struct {
	client *http.Client
	now    func() time.Time
	ports  egress.Ports
}

var _ notify.Sender = (*Sender)(nil)

// Option changes a Sender.
type Option func(*Sender)

// WithPorts sets the ports that the sender sends to. Without it, the sender
// sends to the port 443 only.
func WithPorts(p egress.Ports) Option {
	return func(s *Sender) { s.ports = p }
}

// New returns a Sender with the egress rules for a webhook: https only,
// the port 443 unless WithPorts adds more, no redirect, and the dial guard
// on every connection.
func New(opts ...Option) *Sender {
	s := newSender(egresshttp.Client(egress.NoRedirect, egress.HTTPSOnly), time.Now)

	for _, o := range opts {
		o(s)
	}

	return s
}

func newSender(c *http.Client, now func() time.Time) *Sender {
	return &Sender{client: c, now: now, ports: egress.Ports{}}
}

// Close closes the idle connections of the sender.
func (s *Sender) Close() { s.client.CloseIdleConnections() }

// Kind returns notify.SinkWebhook.
func (s *Sender) Kind() notify.SinkKind { return notify.SinkWebhook }

// Send posts m.Notification to the URL m.To, signed with m.Secrets. A 2xx
// answer means the receiver holds the notification. Send returns an error
// that matches:
//   - egress.ErrRefused for a URL or a port that the egress rules refuse,
//     and for a redirect. A name that resolves to an address that the
//     rules refuse is not in this class: it is a failed connection, so that
//     an answer does not tell such a name from a name that does not
//     resolve;
//   - notify.ErrPermanent for a message that is not for a webhook sink, for
//     secrets that are not one or two non-empty keys, and for a 4xx answer
//     other than 408 and 429 or any other answer that will not change;
//   - a *notify.RetryAfterError for an answer 408, 429 or 5xx with a
//     Retry-After header that notify.ParseRetryAfter reads;
//   - notify.ErrTemporary for every other failure.
func (s *Sender) Send(ctx context.Context, m notify.Message) error {
	if m.Sink.Kind != notify.SinkWebhook {
		return fmt.Errorf("%w: the message is for a %q sink, not a webhook", notify.ErrPermanent, m.Sink.Kind)
	}

	body, err := Encode(m.Notification)
	if err != nil {
		return fmt.Errorf("%w: %w", notify.ErrPermanent, err)
	}

	// Defense in depth: the sink service and the dial guard check the target
	// too. CheckTarget names the layers it does not rely on.
	if refused := post.CheckTarget(op, m.To, m.Sink.Target, s.ports, notify.WebhookHost); refused != nil {
		return refused
	}

	req, err := s.request(ctx, m, body)
	if err != nil {
		return err
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return post.TransportError(ctx, op, err)
	}

	// The status decides the result. Read a bounded part of the body, so
	// the client can reuse the connection, and discard it.
	_, _ = egresshttp.ReadBody(resp) //nolint:errcheck // The status decides the result.
	_ = resp.Body.Close()            //nolint:errcheck // The status decides the result.

	return post.StatusError(op, resp, s.now())
}

// request builds the signed POST of m.
func (s *Sender) request(ctx context.Context, m notify.Message, body []byte) (*http.Request, error) {
	atSec := s.now().Unix()

	sig, err := notify.Sign(m.Secrets, m.Notification.ID, atSec, body)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", notify.ErrPermanent, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.To, bytes.NewReader(body))
	if err != nil {
		// A url.Error repeats the URL, which can hold a token. Say only
		// that the address does not parse.
		return nil, fmt.Errorf("%w: the address does not parse", egress.ErrRefused)
	}

	req.Header.Set("Content-Type", contentType)
	req.Header.Set(notify.HeaderNotificationID, m.Notification.ID)
	req.Header.Set(notify.HeaderTimestamp, strconv.FormatInt(atSec, 10))
	req.Header.Set(notify.HeaderSignature, sig)

	return req, nil
}
