package cli

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// AllowPlainHTTPFlag is the name of the flag that lets serve listen with
// plain HTTP on an address that is not loopback.
const AllowPlainHTTPFlag = "allow-plain-http"

// ErrPlainHTTP reports a listen address that is not loopback, with no TLS,
// no https public URL and no opt-in. Serve refuses to start with it, and
// doctor fails its check with the same text. The text names the three fixes.
var ErrPlainHTTP = errors.New(
	"is not a loopback address and has no TLS: set --tls-cert and --tls-key (DUSTPAN_TLS_CERT, DUSTPAN_TLS_KEY), " +
		"or set --public-url (DUSTPAN_PUBLIC_URL) to the https URL of the proxy that ends TLS, " +
		"or set --" + AllowPlainHTTPFlag + " (DUSTPAN_ALLOW_PLAIN_HTTP) to serve plain HTTP",
)

// ListenReason says why serve may listen on an address. The zero value is
// ListenRefused, so a reason that no rule set refuses the address.
type ListenReason int

const (
	// ListenRefused means that no rule lets serve listen on the address.
	ListenRefused ListenReason = iota
	// ListenLoopback means that the address is a loopback address.
	ListenLoopback
	// ListenTLS means that the server has its own TLS.
	ListenTLS
	// ListenProxy means that an https public URL names a proxy that ends TLS.
	ListenProxy
	// ListenPlainHTTPAllowed means that the operator set the opt-in for
	// plain HTTP. Serve starts and logs a warning.
	ListenPlainHTTPAllowed
)

// ListenSettings holds the settings that decide whether serve may listen
// on Address with plain HTTP.
type ListenSettings struct {
	Address        string
	TLSCert        string
	TLSKey         string
	PublicURL      string
	AllowPlainHTTP bool
}

// CheckListen answers why serve may listen on s.Address, or an error that
// wraps ErrPlainHTTP. Serve checks the configured address with it before it
// opens the database, and checks the bound address with it again. Doctor
// runs the same rule, so the check of doctor and the start of serve agree.
// The rules come in this order: a loopback address, the TLS pair, an https
// public URL, the opt-in. Any other case is refused.
func CheckListen(s ListenSettings) (ListenReason, error) {
	switch {
	case IsLoopbackListen(s.Address):
		return ListenLoopback, nil
	case strings.TrimSpace(s.TLSCert) != "" && strings.TrimSpace(s.TLSKey) != "":
		return ListenTLS, nil
	case IsHTTPSURL(s.PublicURL):
		return ListenProxy, nil
	case s.AllowPlainHTTP:
		return ListenPlainHTTPAllowed, nil
	default:
		return ListenRefused, fmt.Errorf("--listen %q (DUSTPAN_LISTEN) %w", s.Address, ErrPlainHTTP)
	}
}

// IsLoopbackListen reports whether the listen address names loopback only:
// the name localhost or a loopback IP address. An address with no host,
// like :8080, listens on every interface. An address that it cannot split,
// or a host that is another name, is not loopback.
func IsLoopbackListen(listen string) bool {
	host, _, err := net.SplitHostPort(listen)
	if err != nil || host == "" {
		return false
	}

	if host == "localhost" {
		return true
	}

	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}

// IsHTTPSURL reports whether rawURL is an absolute https URL with a host.
// A value that does not parse, another scheme or an empty host is not.
func IsHTTPSURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed == nil {
		return false
	}

	return parsed.Scheme == "https" && parsed.Hostname() != ""
}
