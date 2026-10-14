package clientip

import (
	"context"
	"net/netip"
)

// FromContext returns the client address that WithAddr stored in ctx, or the
// zero Addr when ctx holds none. It never reads a header.
func FromContext(ctx context.Context) netip.Addr {
	stored, ok := ctx.Value(addrKey{}).(netip.Addr)
	if !ok {
		return netip.Addr{}
	}

	return stored
}
