package policy

import (
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"slices"
	"strings"
)

// ErrEndpoint reports a URL of the OPA or the webhook adapter that the
// installation does not allow: not HTTPS, a host that is not on the
// allowlist, or an address that is not public and that no allowed prefix
// holds.
var ErrEndpoint = errors.New("policy endpoint is not allowed")

// Allowlist is the installation allowlist of the endpoints of the OPA and
// the webhook adapters. An operator of the installation sets it. A
// namespace setting can name only a URL whose host is in Hosts, so a holder
// of namespace:manage-policy cannot point the server at any host that it
// wants.
//
// The server also refuses each address that is not public: loopback,
// private, link-local, unspecified, multicast and the other special ranges
// of the IANA registries. Private holds the prefixes of the addresses that
// the operator allows anyway, for example the network of an OPA server in
// the same cluster.
type Allowlist struct {
	// Hosts are the host names, in lowercase with no port, that a URL can
	// name. A host that is an IP literal must be in Hosts too.
	Hosts []string
	// Private are the prefixes of the addresses that are not public and
	// that the adapter can connect to.
	Private []netip.Prefix
}

// CheckURL parses raw, the URL of an OPA server or of a webhook, and
// returns it when it uses https, has no user info and no fragment, and
// names a host of a.Hosts. A host that is an IP literal must also pass
// CheckAddr. It returns an error that wraps ErrEndpoint otherwise. The
// error names the host, never the path or the query.
func (a *Allowlist) CheckURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: the URL does not parse", ErrEndpoint)
	}

	host := strings.ToLower(u.Hostname())

	switch {
	case u.Scheme != "https":
		return nil, fmt.Errorf("%w: use https", ErrEndpoint)
	case u.User != nil || u.Fragment != "":
		return nil, fmt.Errorf("%w: a URL has no user info and no fragment", ErrEndpoint)
	case host == "" || !slices.Contains(a.Hosts, host):
		return nil, fmt.Errorf("%w: the host %q is not on the allowlist of the installation", ErrEndpoint, host)
	default:
	}

	if addr, err := netip.ParseAddr(host); err == nil {
		if err := a.CheckAddr(addr); err != nil {
			return nil, err
		}
	}

	return u, nil
}

// CheckAddr reports whether an adapter can connect to addr. A public
// address passes. An address that is not public passes only when a prefix
// of a.Private holds it. It returns an error that wraps ErrEndpoint
// otherwise.
//
// The adapter calls CheckAddr in its dialer, for each address that it
// connects to after the name resolution, and follows no redirect. So a
// name whose DNS answer changes to an internal address after CheckURL
// still cannot reach that address.
func (a *Allowlist) CheckAddr(addr netip.Addr) error {
	addr = addr.Unmap()

	if !addr.IsValid() {
		return fmt.Errorf("%w: no address", ErrEndpoint)
	}

	if publicAddr(addr) {
		return nil
	}

	for _, p := range a.Private {
		if p.Contains(addr) {
			return nil
		}
	}

	return fmt.Errorf("%w: the address %s is not public, and no allowed prefix holds it", ErrEndpoint, addr)
}

// nonPublic are the special ranges that the methods of netip.Addr do not
// cover: this network, shared address space, IETF protocol assignments,
// benchmarking, reserved, the IPv4 broadcast, the NAT64 prefixes, which can
// map to a private IPv4 address, and the documentation ranges.
func nonPublic() []netip.Prefix {
	return []netip.Prefix{
		netip.MustParsePrefix("0.0.0.0/8"),
		netip.MustParsePrefix("100.64.0.0/10"),
		netip.MustParsePrefix("192.0.0.0/24"),
		netip.MustParsePrefix("192.0.2.0/24"),
		netip.MustParsePrefix("198.18.0.0/15"),
		netip.MustParsePrefix("198.51.100.0/24"),
		netip.MustParsePrefix("203.0.113.0/24"),
		netip.MustParsePrefix("240.0.0.0/4"),
		netip.MustParsePrefix("64:ff9b::/96"),
		netip.MustParsePrefix("64:ff9b:1::/48"),
		netip.MustParsePrefix("2001:db8::/32"),
	}
}

func publicAddr(addr netip.Addr) bool {
	if addr.IsLoopback() || addr.IsPrivate() || addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() ||
		addr.IsInterfaceLocalMulticast() || addr.IsMulticast() || addr.IsUnspecified() {
		return false
	}

	return !slices.ContainsFunc(nonPublic(), func(p netip.Prefix) bool { return p.Contains(addr) })
}
