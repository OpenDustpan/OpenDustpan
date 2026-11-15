package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/launcher/session"
	"opendustpan.org/opendustpan/pkg/runner"
	"opendustpan.org/opendustpan/pkg/runner/protocol"
)

// Errors of the transport.
var (
	// errRedirect reports an answer that redirects: the proof of a request
	// is for one path of one server.
	errRedirect = errors.New("the server answered a redirect")
	// errTransport reports a request or an answer that the transport
	// refuses.
	errTransport = errors.New("the launcher refused the call")
)

// Limits of a call.
const (
	callTimeout = 2 * time.Minute
	// maxAnswer is the largest body of an answer that the launcher reads.
	maxAnswer = protocol.MaxBody
)

// httpTransport sends the requests of the session over HTTPS.
type httpTransport struct {
	base   *url.URL
	client *http.Client
}

// newTransport returns the transport to the server at base, an https URL.
func newTransport(base string) (*httpTransport, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("%w: the server URL %q is not an https URL", errTransport, base)
	}

	tr, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("%w: the default HTTP transport has an unknown type", errTransport)
	}

	tr = tr.Clone()
	tr.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12}

	return &httpTransport{base: u, client: &http.Client{
		Transport: tr, Timeout: callTimeout, Jar: nil,
		CheckRedirect: func(*http.Request, []*http.Request) error { return errRedirect },
	}}, nil
}

// Do implements session.Transport.
func (t *httpTransport) Do(ctx context.Context, req *session.Request) (*session.Response, error) {
	// Defense in depth: the session client builds each path from the
	// protocol constants; this check stays so that a path never names
	// another host.
	if !strings.HasPrefix(req.Path, "/") || strings.HasPrefix(req.Path, "//") {
		return nil, fmt.Errorf("%w: the path %q is not a path of the server", errTransport, req.Path)
	}

	origin := url.URL{Scheme: t.base.Scheme, Host: t.base.Host}

	full, err := url.Parse(origin.String() + req.Path)
	if err != nil || full.Host != t.base.Host {
		return nil, fmt.Errorf("%w: the path %q is not a path of the server", errTransport, req.Path)
	}

	hr, err := http.NewRequestWithContext(ctx, req.Method, full.String(), bytes.NewReader(req.Body))
	if err != nil {
		return nil, fmt.Errorf("build the request: %w", err)
	}

	for k, v := range req.Header {
		hr.Header.Set(k, v)
	}

	resp, err := t.client.Do(hr)
	if err != nil {
		return nil, fmt.Errorf("send the request: %w", err)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxAnswer+1))
	if err = errors.Join(err, resp.Body.Close()); err != nil {
		return nil, fmt.Errorf("read the answer: %w", err)
	}

	if len(body) > maxAnswer {
		return nil, fmt.Errorf("%w: the answer has more than %d bytes", errTransport, maxAnswer)
	}

	return &session.Response{Status: resp.StatusCode, Body: body, RetryAfter: retryAfter(resp.Header)}, nil
}

// maxRetryAfter caps the wait that a Retry-After header asks for. It is the
// shortest claim lease, so one wait never outlasts a lease. Defense in depth:
// the retry of the launcher caps the wait too.
const maxRetryAfter = runner.MinLease

// retryAfter returns the wait of the header Retry-After in seconds, capped at
// maxRetryAfter. A missing or negative value gives 0. A value that is too large
// to parse gives the cap.
func retryAfter(h http.Header) time.Duration {
	s, err := strconv.ParseInt(h.Get("Retry-After"), 10, 64)
	switch {
	case errors.Is(err, strconv.ErrRange) && s < 0:
		// ParseInt returns the lowest int64 for a negative value that overflows.
		return 0
	case errors.Is(err, strconv.ErrRange):
		return maxRetryAfter
	case err != nil || s < 0:
		return 0
	}

	// Compare in whole seconds before the multiplication, so that the product
	// cannot overflow time.Duration.
	if s > int64(maxRetryAfter/time.Second) {
		return maxRetryAfter
	}

	return time.Duration(s) * time.Second
}
