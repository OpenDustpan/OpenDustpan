package client

import (
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"opendustpan.org/opendustpan/pkg/accesstoken"
)

// expiryWarner is an http.RoundTripper that prints one warning when an
// answer says that the access token of the command expires soon (ADR 53,
// decision 11). The server sets accesstoken.ExpiryHeader on each answer to
// such a token. The warning goes to out once for each command, before the
// output of the command, and never names the token.
type expiryWarner struct {
	next http.RoundTripper
	out  io.Writer
	now  func() time.Time
	once sync.Once
}

// warnOnExpiry returns client with a transport that warns on out before the
// token of the command expires.
func warnOnExpiry(client *http.Client, out io.Writer) *http.Client {
	next := client.Transport
	if next == nil {
		next = http.DefaultTransport
	}

	client.Transport = &expiryWarner{next: next, out: out, now: time.Now, once: sync.Once{}}

	return client
}

// RoundTrip implements http.RoundTripper.
func (e *expiryWarner) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := e.next.RoundTrip(req)
	if err != nil || resp == nil {
		return resp, err //nolint:wrapcheck // The warner is transparent: callers see the error of the transport.
	}

	raw := resp.Header.Get(accesstoken.ExpiryHeader)
	if raw == "" {
		return resp, nil
	}

	// A header that is not a time warns of nothing: the answer itself is
	// still the answer of the command.
	expiresAt, ok := parseExpiry(raw)
	if !ok {
		return resp, nil
	}

	e.once.Do(func() {
		days := accesstoken.DaysUntil(expiresAt, e.now())

		//nolint:errcheck // A lost warning on stderr does not change the result of the command.
		fmt.Fprintf(e.out,
			"warning: the access token expires at %s (in %d days). Make a new token or rotate this one "+
				"with POST /api/server/tokens/{id}/rotate before then.\n",
			expiresAt.UTC().Format(time.RFC3339), days)
	})

	return resp, nil
}

// parseExpiry reads the RFC 3339 time of the expiry header.
func parseExpiry(raw string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, raw)

	return t, err == nil
}
