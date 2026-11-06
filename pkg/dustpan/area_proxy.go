package dustpan

import (
	"fmt"
	"net/netip"

	"opendustpan.org/opendustpan/internal/clientip"
)

// proxySettings holds the trusted proxy settings of the client address
// (internal/clientip): the proxies and the forwarding header they set.
type proxySettings struct {
	prefixes []netip.Prefix
	header   string
}

// defaultProxySettings returns the trusted proxy settings before the options:
// no trusted proxy and the default header.
func defaultProxySettings() proxySettings {
	return proxySettings{prefixes: nil, header: ""}
}

// WithTrustedProxies names the proxies whose forwarding headers the server
// trusts. Empty means none: every request reads as its TCP peer. List a proxy
// only when it always overwrites or appends the header named by
// WithTrustedProxyHeader, so a client cannot name any address. NewServer
// refuses a list that covers every address or holds host bits.
func WithTrustedProxies(prefixes ...netip.Prefix) Option {
	return func(o *settings) { o.proxies.prefixes = prefixes }
}

// WithTrustedProxyHeader names the one forwarding header the server reads
// from trusted proxies: "x-forwarded-for" (the default) or "forwarded". The
// match ignores case and "" means the default. The server never reads the
// other header. NewServer refuses an unknown value.
func WithTrustedProxyHeader(h string) Option {
	return func(o *settings) { o.proxies.header = h }
}

// newClientIPResolver builds the client address resolver of the server from
// the trusted proxy settings. An unknown header or a refused proxy list
// fails the build, so the server never starts with them.
func newClientIPResolver(proxies proxySettings) (*clientip.Resolver, error) {
	header, err := clientip.ParseHeader(proxies.header)
	if err != nil {
		return nil, fmt.Errorf("trusted proxy header: %w", err)
	}

	resolver, err := clientip.NewResolver(proxies.prefixes, header)
	if err != nil {
		return nil, fmt.Errorf("trusted proxies: %w", err)
	}

	return resolver, nil
}
