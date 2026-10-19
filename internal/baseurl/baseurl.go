// Package baseurl reports the external base URL of the server for one
// request. The operator sets it with --public-url; without the flag the
// server uses the scheme and host of the request. It never reads
// X-Forwarded-* headers.
package baseurl

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// ErrInvalid reports a public URL that is not an absolute http or https URL
// with a host, no query and no fragment.
var ErrInvalid = errors.New("invalid public URL")

// Resolver returns the external base URL for a request. Adapters receive it
// through their Config and use it for absolute URLs.
type Resolver struct {
	publicURL string
}

// Parse checks raw as an absolute http or https URL with a host, no query and
// no fragment, and returns its Resolver. An empty raw returns a Resolver without a
// public URL, which derives the base URL from each request.
func Parse(raw string) (Resolver, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Resolver{publicURL: ""}, nil
	}

	u, err := url.Parse(trimmed)
	if err != nil {
		return Resolver{}, fmt.Errorf("%w %q", ErrInvalid, raw)
	}

	if !u.IsAbs() || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(trimmed, "#") {
		return Resolver{}, fmt.Errorf(
			"%w %q: want an absolute http or https URL with a host, no query and no fragment",
			ErrInvalid,
			raw,
		)
	}

	return Resolver{publicURL: strings.TrimSuffix(trimmed, "/")}, nil
}

// BaseURL returns the external base URL for req: the public URL when one is
// set, else the scheme and host of the request (https when the request uses
// TLS). It ignores proxy headers such as X-Forwarded-Host.
func (r Resolver) BaseURL(req *http.Request) string {
	if r.publicURL != "" {
		return r.publicURL
	}

	scheme := "http"
	if req.TLS != nil {
		scheme = "https"
	}

	return scheme + "://" + req.Host
}

// String returns the public URL, or "" when none is set.
func (r Resolver) String() string {
	return r.publicURL
}
