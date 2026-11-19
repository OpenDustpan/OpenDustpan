package runner

import (
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// The schemes of an upstream proxy URL.
const (
	schemeHTTP  = "http"
	schemeHTTPS = "https"
)

// UpstreamEndpointChanged reports whether the upstream proxy URL next names
// another endpoint than the URL old. The endpoint is the scheme, the host and
// the port, read from the parsed URL, never from the text. The path, the query
// and the case of the scheme and the host do not change the endpoint. One
// trailing dot of the host, a default port that is written out, and the
// spelling of an IP address do not change it either. A host in Unicode and its
// punycode form are two endpoints: the rule has no IDN conversion, so it clears
// a credential in that case. A URL that does not parse counts as another
// endpoint, so the function fails closed. Two empty URLs are the same endpoint.
//
// A write-only credential is for one endpoint. A save that moves the upstream
// proxy to another endpoint must not keep it.
func UpstreamEndpointChanged(old, next string) bool {
	if old == "" && next == "" {
		return false
	}

	oldKey, oldOK := upstreamEndpointKey(old)
	nextKey, nextOK := upstreamEndpointKey(next)

	return !oldOK || !nextOK || oldKey != nextKey
}

// upstreamEndpointKey returns the key of the endpoint of raw: the scheme, the
// normalized host and the effective port. It returns false for a URL that does
// not parse, that is not http or https, or that has no host or no valid port.
func upstreamEndpointKey(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil {
		return "", false
	}

	if u.Scheme != schemeHTTP && u.Scheme != schemeHTTPS {
		return "", false
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return "", false
	}

	if a, addrErr := netip.ParseAddr(host); addrErr == nil {
		host = a.Unmap().String()
	} else {
		host = strings.TrimSuffix(host, ".")
	}

	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == schemeHTTPS {
			port = "443"
		}
	}

	if n, convErr := strconv.Atoi(port); convErr != nil || n < 1 || n > 65535 {
		return "", false
	}

	return u.Scheme + "://" + host + ":" + port, true
}
