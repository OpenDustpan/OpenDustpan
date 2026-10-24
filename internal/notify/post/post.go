// Package post holds the rules that every notify sender applies to one
// outbound request: the checks of the URL of a sink, the class of a request
// that got no answer, and the class of an answer by its status. The webhook
// sender and the chat sender both use it, so the two classes of a failure and
// the rule that an error never holds the URL stay the same for every sink
// kind.
//
// An error from this package never holds the URL, the resolved address, the
// network error or the body of an answer. It keeps the class of the failure:
// egress.ErrRefused, notify.ErrPermanent, notify.ErrTemporary or a
// *notify.RetryAfterError.
package post

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/notify"
)

// CheckTarget checks the URL to of a sink before a send: the egress rules of
// an https URL, the port against ports, and that hostOf reads a host from to
// that is target, the target of the sink. op names the request in the error
// text, for example "post webhook". It returns an error that matches
// egress.ErrRefused, or nil.
//
// Defense in depth: the sink service checks the URL, the port and the host
// when a person saves a sink and before a test, and the dial guard checks the
// address of each connection. These checks stay so that a caller of a sender
// that skips the service, or a sink that a restore left behind, cannot send to
// a URL that the rules refuse, to a port that the operator did not allow, or
// to a host that is not the target of the sink.
func CheckTarget(op, to, target string, ports egress.Ports, hostOf func(string) (string, error)) error {
	// The text of these errors can repeat the address of the URL, so the check
	// keeps the class and drops the text.
	if egress.CheckURL(to, egress.HTTPSOnly) != nil {
		return fmt.Errorf("%s: %w: the URL is not allowed", op, egress.ErrRefused)
	}

	if egress.CheckPort(to, ports) != nil {
		return fmt.Errorf("%s: %w: the port is not allowed", op, egress.ErrRefused)
	}

	host, err := hostOf(to)
	if err != nil {
		return fmt.Errorf("%s: %w", op, egress.ErrRefused)
	}

	if host != target {
		return fmt.Errorf("%s: %w: the host of the URL is not the target of the sink", op, egress.ErrRefused)
	}

	return nil
}

// TransportError maps the error err of a request that got no answer. op names
// the request in the error text. It drops the text of err, which can repeat
// the URL and the resolved address, and keeps the class.
func TransportError(ctx context.Context, op string, err error) error {
	switch {
	case errors.Is(err, egresshttp.ErrDialRefused):
		// The dial guard refused the resolved address. Say it is a failed
		// connection: a refusal here would tell a name that resolves to a
		// private address from a name that does not resolve.
		return fmt.Errorf("%s: %w: the request failed", op, notify.ErrTemporary)
	case errors.Is(err, egress.ErrRefused):
		return fmt.Errorf("%s: %w", op, egress.ErrRefused)
	case ctx.Err() != nil:
		return fmt.Errorf("%s: %w: %w", op, notify.ErrTemporary, ctx.Err())
	default:
		return fmt.Errorf("%s: %w: the request failed", op, notify.ErrTemporary)
	}
}

// StatusError maps the status of the answer resp, and returns nil for 2xx. op
// names the request in the error text, and now is the time that a
// Retry-After date counts from. The caller reads the body of a 2xx answer
// when it must, and closes it.
//
// An answer 408, 429 or 5xx is temporary: with a Retry-After header that
// notify.ParseRetryAfter reads, the error is a *notify.RetryAfterError. Any
// other 4xx, and a 3xx that the client did not follow, is permanent, because
// it will not change on the next attempt.
func StatusError(op string, resp *http.Response, now time.Time) error {
	code := resp.StatusCode

	switch {
	case code >= http.StatusOK && code < http.StatusMultipleChoices:
		return nil
	case code == http.StatusRequestTimeout, code == http.StatusTooManyRequests, code >= http.StatusInternalServerError:
		if wait, ok := notify.ParseRetryAfter(resp.Header.Get("Retry-After"), now); ok {
			return fmt.Errorf("%s: status %d: %w", op, code, &notify.RetryAfterError{Wait: wait})
		}

		return fmt.Errorf("%s: status %d: %w", op, code, notify.ErrTemporary)
	case code >= http.StatusMultipleChoices:
		return fmt.Errorf("%s: status %d: %w", op, code, notify.ErrPermanent)
	default:
		return fmt.Errorf("%s: status %d: %w", op, code, notify.ErrTemporary)
	}
}
