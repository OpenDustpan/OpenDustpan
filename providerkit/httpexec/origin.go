package httpexec

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// ErrOtherOrigin reports a request to a URL outside the origin of the
// caller. The caller sends nothing, so a credential never goes to a host that
// a reply named, for example in a link to the next page.
var ErrOtherOrigin = errors.New("request to another origin")

// ErrInvalidURL reports a URL that is not an absolute http or https URL.
var ErrInvalidURL = errors.New("not an absolute http or https URL")

// Origin is the scheme, host and port of the base URL of a provider. A caller
// sends requests only to its origin.
type Origin struct {
	// Scheme is "https" or "http". OriginOf accepts both for any host; the
	// declaration check allows "http" only for a loopback host.
	Scheme string
	// Host is the lowercase host name or IP address, without brackets.
	Host string
	// Port is the port, with the default port of the scheme filled in.
	Port string
}

// OriginOf returns the origin of an absolute URL. Errors: ErrInvalidURL.
func OriginOf(raw string) (Origin, error) {
	var none Origin

	u, err := url.Parse(raw)
	if err != nil {
		return none, fmt.Errorf("%w: %w", ErrInvalidURL, err)
	}

	if u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return none, fmt.Errorf("%w: %q", ErrInvalidURL, raw)
	}

	port := u.Port()
	if port == "" {
		port = map[string]string{"https": "443", "http": "80"}[u.Scheme]
	}

	return Origin{Scheme: u.Scheme, Host: strings.ToLower(u.Hostname()), Port: port}, nil
}

// String returns the origin as a URL with no path.
func (o Origin) String() string {
	return o.Scheme + "://" + net.JoinHostPort(o.Host, o.Port)
}

// Check returns nil when raw is an absolute URL of the origin. Errors:
// ErrOtherOrigin.
func (o Origin) Check(raw string) error {
	got, err := OriginOf(raw)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrOtherOrigin, err)
	}

	if got != o {
		return fmt.Errorf("%w: %s is not %s", ErrOtherOrigin, got, o)
	}

	return nil
}

// Resolve resolves ref, for example the target of a link header, against
// the URL of the request that returned it, and checks that the result is in
// the origin. Errors: ErrOtherOrigin.
func (o Origin) Resolve(requestURL, ref string) (string, error) {
	base, err := url.Parse(requestURL)
	if err != nil {
		return "", fmt.Errorf("%w: parse request URL: %w", ErrOtherOrigin, err)
	}

	r, err := url.Parse(ref)
	if err != nil {
		return "", fmt.Errorf("%w: parse link: %w", ErrOtherOrigin, err)
	}

	next := base.ResolveReference(r).String()
	if err := o.Check(next); err != nil {
		return "", err
	}

	return next, nil
}
