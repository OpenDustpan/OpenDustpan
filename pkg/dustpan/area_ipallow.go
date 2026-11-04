package dustpan

import (
	"context"
	"fmt"
	"time"

	"opendustpan.org/opendustpan/pkg/identity"
	"opendustpan.org/opendustpan/pkg/ipallow"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// ipAllowlistSettings holds the IP allowlist feature switch, the bootstrap
// exemption switch and the list store (ADR 55, decision 8).
type ipAllowlistSettings struct {
	store            ipallow.Store
	enabled          bool
	includeBootstrap bool
}

// defaultIPAllowlistSettings returns the IP allowlist settings before the
// options: the feature off, the bootstrap admin token exempt and no store.
func defaultIPAllowlistSettings() *ipAllowlistSettings {
	return &ipAllowlistSettings{store: nil, enabled: false, includeBootstrap: false}
}

// WithIPAllowlists turns IP allowlists on when on is true. A namespace
// that has a list then refuses requests from addresses outside it
// (ADR 55, decision 8). Off by default.
func WithIPAllowlists(on bool) Option {
	return func(o *settings) { ipAllowlistConfig(o).enabled = on }
}

// WithIPAllowlistsIncludeBootstrap applies IP allowlists to the bootstrap
// admin token too when on is true. Off by default: the bootstrap admin token
// is exempt, so an administrator cannot lock themselves out.
func WithIPAllowlistsIncludeBootstrap(on bool) Option {
	return func(o *settings) { ipAllowlistConfig(o).includeBootstrap = on }
}

// WithIPAllowlistStore sets the IP allowlist persistence port. Required when
// IP allowlists are on: every SQL adapter serves it from the same database
// connection as the other ports.
func WithIPAllowlistStore(s ipallow.Store) Option {
	return func(o *settings) { ipAllowlistConfig(o).store = s }
}

// ipAllowlistConfig returns the mutable allowlist settings of o, creating
// them when an Option runs on zero settings.
func ipAllowlistConfig(o *settings) *ipAllowlistSettings {
	if o.ipAllowlists == nil {
		o.ipAllowlists = defaultIPAllowlistSettings()
	}

	return o.ipAllowlists
}

// newIPAllowlistService builds the IP allowlist service from validated
// settings, or nil when no allowlist store is configured: an SDK user who
// did not pass WithIPAllowlistStore gets no allowlist routes.
func newIPAllowlistService(
	allow *ipAllowlistSettings, namespaces *namespace.Service, now func() time.Time,
) (*ipallow.Service, error) {
	if allow == nil || allow.store == nil {
		return nil, nil //nolint:nilnil // No allowlist store is a valid setup: the routes stay unmounted.
	}

	svc, err := ipallow.NewService(ipallow.Config{
		Store: allow.store, Namespaces: namespaces, Now: now, Enabled: allow.enabled,
	})
	if err != nil {
		return nil, fmt.Errorf("ip allowlist service: %w", err)
	}

	return svc, nil
}

// allowlistGate wraps next in the IP allowlist gate when the feature is on.
// The gate is the outermost authorizer, so it runs in the authorization step
// of every adapter. When the feature is off it returns next with no
// decorator, so the default path has zero cost and zero risk.
//
//nolint:ireturn // The adapters take the port.
func allowlistGate(next identity.Authorizer, allow *ipAllowlistSettings) identity.Authorizer {
	if allow == nil || !allow.enabled {
		return next
	}

	return ipallow.Gate{
		Next: next, Rules: allow.store,
		Enabled: allow.enabled, IncludeBootstrap: allow.includeBootstrap,
	}
}

// allowlistAddressGate returns the address-only gate for the capability
// routes of the tool adapters when the feature is on, else nil. It holds
// only the list rules: capability routes have no principal, so there is no
// bootstrap exemption to carry. A nil interface (not a typed nil) reports
// off, so the adapters skip the check.
//
//nolint:ireturn // The adapters take the port.
func allowlistAddressGate(allow *ipAllowlistSettings) interface {
	CheckAddress(ctx context.Context, ns namespace.Name) error
} {
	if allow == nil || !allow.enabled {
		return nil
	}

	return ipallow.Gate{
		Next: nil, Rules: allow.store,
		Enabled: allow.enabled, IncludeBootstrap: allow.includeBootstrap,
	}
}
