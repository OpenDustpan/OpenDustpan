package proxy

import (
	"errors"
	"net/netip"
	"strconv"
	"strings"

	"opendustpan.org/opendustpan/pkg/jobproxy"
)

// ErrBadTarget is the error for a CONNECT target that is not a valid host
// and port.
var ErrBadTarget = errors.New("the CONNECT target is not a valid host and port")

// Bounds of a target.
const (
	maxTargetBytes = 253 + 1 + 5
	maxLabelBytes  = 63
	maxPort        = 65535
	maxPortDigits  = 5
	minHostLabels  = 2
)

// ParseConnectTarget parses the target of a CONNECT: a host and a port. It
// accepts only a lowercase DNS name of at least two labels, with no dot at
// the end, and a port from 1 to 65535 with no sign and no leading zero. It
// refuses an IP literal. It never changes its input to make it valid.
func ParseConnectTarget(s string) (string, int, error) {
	if s == "" || len(s) > maxTargetBytes {
		return "", 0, ErrBadTarget
	}

	host, portText, found := strings.Cut(s, ":")
	// A second colon makes the port text invalid, and an IPv6 literal fails
	// the host check, so Cut at the first colon is enough.
	if !found {
		return "", 0, ErrBadTarget
	}

	port, ok := parsePort(portText)
	if !ok || !validHostName(host) {
		return "", 0, ErrBadTarget
	}

	return host, port, nil
}

// parsePort parses a decimal port with no sign and no leading zero.
func parsePort(s string) (int, bool) {
	if s == "" || len(s) > maxPortDigits || s[0] == '0' {
		return 0, false
	}

	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}

	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > maxPort {
		return 0, false
	}

	return n, true
}

// validHostName reports whether host is a lowercase DNS name of at least two
// labels, and not an IP address.
func validHostName(host string) bool {
	if host == "" || len(host) > jobproxy.MaxHostBytes {
		return false
	}

	if _, err := netip.ParseAddr(host); err == nil {
		return false
	}

	labels := strings.Split(host, ".")
	if len(labels) < minHostLabels {
		return false
	}

	for _, l := range labels {
		if !validLabel(l) {
			return false
		}
	}

	// A name whose last label is only digits is an IPv4 address in some
	// spellings, for example "127.1", so it is never a host name.
	return !allDigits(labels[len(labels)-1])
}

// validLabel reports whether l is a lowercase DNS label.
func validLabel(l string) bool {
	if l == "" || len(l) > maxLabelBytes || l[0] == '-' || l[len(l)-1] == '-' {
		return false
	}

	for i := range len(l) {
		c := l[i]
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}

	return true
}

// allDigits reports whether s holds only digits.
func allDigits(s string) bool {
	for i := range len(s) {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}

	return s != ""
}
