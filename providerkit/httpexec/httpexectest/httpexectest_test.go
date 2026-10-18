package httpexectest_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"

	"opendustpan.org/opendustpan/providerkit/httpexec"
	"opendustpan.org/opendustpan/providerkit/httpexec/httpexectest"
)

// TestSuiteAgainstReferenceCaller runs the suite against a minimal caller on
// net/http, so the suite itself has a test. The HTTP engine brings its own
// adapter, which runs the same suite.
func TestSuiteAgainstReferenceCaller(t *testing.T) {
	t.Parallel()

	httpexectest.Run(t, func(t *testing.T, o httpexectest.Options) httpexec.Caller {
		t.Helper()

		client := &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
		t.Cleanup(client.CloseIdleConnections)

		return referenceCaller{origin: o.Origin, client: client, max: o.MaxReplyBytes, auth: o.Authorizer}
	})
}

type referenceCaller struct {
	origin httpexec.Origin
	client *http.Client
	max    int64
	auth   httpexec.Authorizer
}

func (c referenceCaller) Call(ctx context.Context, req httpexec.Request) (httpexec.Reply, error) {
	var none httpexec.Reply

	if err := c.origin.Check(req.URL); err != nil {
		return none, err
	}

	h := req.Header.Clone()
	if err := c.auth.Authorize(ctx, h); err != nil {
		return none, fmt.Errorf("authorize: %w", err)
	}

	r, err := http.NewRequestWithContext(ctx, req.Method, req.URL, bytes.NewReader(req.Body))
	if err != nil {
		return none, fmt.Errorf("build request: %w", err)
	}

	r.Header = h

	resp, err := c.client.Do(r)
	if err != nil {
		return none, fmt.Errorf("send: %w", err)
	}

	defer func() { _ = resp.Body.Close() }() //nolint:errcheck // A close error after a full read changes nothing.

	body, err := io.ReadAll(io.LimitReader(resp.Body, c.max+1))
	if err != nil {
		return none, fmt.Errorf("read reply: %w", err)
	}

	if int64(len(body)) > c.max {
		return none, httpexec.ErrTooLarge
	}

	return httpexec.Reply{Status: resp.StatusCode, Header: resp.Header, Body: body}, nil
}
