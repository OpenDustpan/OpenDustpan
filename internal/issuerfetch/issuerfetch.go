// Package issuerfetch is the HTTP adapter of the copy check of the OIDC issuer:
// it reads one document at an https URL through a client that applies the
// egress rules. The error text of a failure names no address that the rules
// refused.
package issuerfetch

import (
	"context"
	"fmt"
	"net/http"

	"opendustpan.org/opendustpan/internal/egress/egresshttp"
	"opendustpan.org/opendustpan/internal/issuersvc"
)

// Client reads documents for the copy check.
type Client struct {
	http *http.Client
}

var _ issuersvc.Fetcher = (*Client)(nil)

// New returns a Client over c. c must apply the egress rules
// (egresshttp.Client).
func New(c *http.Client) *Client {
	return &Client{http: c}
}

// Fetch implements issuersvc.Fetcher. It sends one GET and reads at most the
// body size that the egress rules allow.
func (c *Client) Fetch(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, issuersvc.ErrFetchURL
	}

	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		// The text does not tell a refused address from an unreachable one.
		return nil, issuersvc.ErrFetchConnection
	}

	body, readErr := egresshttp.ReadBody(resp)
	if closeErr := resp.Body.Close(); closeErr != nil && readErr == nil {
		readErr = closeErr
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: %d", issuersvc.ErrFetchStatus, resp.StatusCode)
	}

	if readErr != nil {
		return nil, issuersvc.ErrFetchSize
	}

	return body, nil
}
