package declaration

import (
	"fmt"
	"net/url"
	"strings"

	"opendustpan.org/providerkit/lifecycle"
)

// CheckEndpoint checks the base URL or the token URL of the HTTP engine after
// the engine expands its references, when the provider is configured.
// Validate checks a URL without references in the same way. The rules: an
// absolute https URL, or http to a loopback host, with no user information,
// no query and no fragment. A credential then goes only to the host that the
// URL names, over TLS, and a URL cannot hide a second host or a parameter.
// Errors: lifecycle.ErrInvalidConfig.
func CheckEndpoint(raw string) error {
	if problem := endpointProblem(raw); problem != "" {
		return fmt.Errorf("%w: URL %q %s", lifecycle.ErrInvalidConfig, raw, problem)
	}

	return nil
}

// endpointProblem returns why raw is not an allowed endpoint URL, or "".
func endpointProblem(raw string) string {
	u, err := url.Parse(raw)

	switch {
	case err != nil || u.Host == "":
		return "is not an absolute URL"
	case u.Scheme != "https" && !loopback(u):
		return "must be https, or http to a loopback host"
	case u.User != nil:
		return "must not hold user information"
	case u.RawQuery != "" || u.ForceQuery:
		return "must not hold a query"
	case strings.ContainsRune(raw, '#'):
		return "must not hold a fragment"
	default:
		return ""
	}
}

func loopback(u *url.URL) bool {
	host := u.Hostname()

	return u.Scheme == "http" && (host == "localhost" || host == "127.0.0.1" || host == "::1")
}

// PathSegment returns the value of a reference as one escaped path segment.
// A value that is empty, "." or ".." would remove a segment or move up the
// path, so it is lifecycle.ErrInvalidConfig.
func PathSegment(v string) (string, error) {
	if v == "" || v == "." || v == ".." {
		return "", fmt.Errorf("%w: %q cannot be a path segment", lifecycle.ErrInvalidConfig, v)
	}

	return url.PathEscape(v), nil
}
