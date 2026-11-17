package runner

import (
	"context"
	"fmt"
	"net/netip"
	"net/url"
	"strings"
	"time"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
	"opendustpan.org/opendustpan/pkg/namespace"
)

// MaxProxyRanges is the most private ranges that one pool may allow. A save
// with more ranges fails with ErrInvalid.
const MaxProxyRanges = 64

// ProxySettings are the settings of the job proxy of one pool: the private
// ranges that the proxy may open and the upstream proxy, if the pool names
// one. A value of this type is checked: NewProxySettings is the only way to
// make one with values. The zero value is the default of a pool with no row:
// no range and no upstream proxy.
type ProxySettings struct {
	ranges      []string
	upstreamURL string
}

// NewProxySettings checks the ranges and the upstream URL and returns the
// settings. An empty upstreamURL means no upstream proxy. It returns
// ErrInvalid for a range that egress.ParseAllowedRanges refuses, a range with
// a host bit, more than MaxProxyRanges ranges, and an upstream URL that is
// not an http or https URL with a host that the job proxy may open. The
// errors never repeat the upstream URL.
func NewProxySettings(ranges []string, upstreamURL string) (ProxySettings, error) {
	if len(ranges) > MaxProxyRanges {
		return ProxySettings{}, fmt.Errorf("%w: a pool allows at most %d ranges", ErrInvalid, MaxProxyRanges)
	}

	if _, err := egress.ParseAllowedRanges(ranges); err != nil {
		return ProxySettings{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}

	for _, s := range ranges {
		// Defense in depth: a range with a host bit is refused here. The
		// parser masks it, so the text is checked, not the parsed prefix, so
		// that a stored range is never wider than the operator wrote.
		p, err := netip.ParsePrefix(s)
		if err != nil || p != p.Masked() {
			return ProxySettings{}, fmt.Errorf("%w: range %s is not a network address", ErrInvalid, s)
		}
	}

	if err := checkUpstreamURL(upstreamURL); err != nil {
		return ProxySettings{}, err
	}

	return ProxySettings{ranges: append([]string(nil), ranges...), upstreamURL: upstreamURL}, nil
}

// AllowedRanges returns a copy of the private ranges of the settings.
func (s ProxySettings) AllowedRanges() []string {
	return append([]string(nil), s.ranges...)
}

// UpstreamURL returns the upstream proxy URL, or the empty string for none.
func (s ProxySettings) UpstreamURL() string {
	return s.upstreamURL
}

// checkUpstreamURL accepts an empty URL, and an http or https URL with a host
// and no user information or fragment. A literal host is checked with the
// same rule as a range: an address that no range opens is refused.
func checkUpstreamURL(raw string) error {
	if raw == "" {
		return nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%w: the upstream URL does not parse", ErrInvalid)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%w: the upstream URL scheme is http or https", ErrInvalid)
	}

	if u.User != nil {
		return fmt.Errorf("%w: the upstream URL holds no user information; use the credential", ErrInvalid)
	}

	if u.Fragment != "" || strings.Contains(raw, "#") {
		return fmt.Errorf("%w: the upstream URL holds no fragment", ErrInvalid)
	}

	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("%w: the upstream URL has no host", ErrInvalid)
	}

	if a, err := netip.ParseAddr(host); err == nil {
		// Defense in depth: the job proxy checks the resolved address too; this
		// check stops a literal that no range opens at the save.
		if err := egress.CheckAllowedRange(netip.PrefixFrom(a, a.BitLen())); err != nil {
			return fmt.Errorf("%w: the upstream host is an address that no range opens", ErrInvalid)
		}
	}

	return nil
}

// ProxySettingsRecord is the stored row of the proxy settings of one pool.
// Credential holds the sealed credential as the store keeps it, or nothing
// for none. The store never returns it unsealed. In a save, Version is the
// version that the caller read.
type ProxySettingsRecord struct {
	Settings   ProxySettings
	Credential []byte
	Version    int64
	UpdatedBy  string
	UpdatedAt  time.Time
}

// PoolProxyStore is the persistence port of the proxy settings of a pool.
type PoolProxyStore interface {
	// ProxySettings returns the record of the pool poolID. It returns
	// ErrNotFound when the pool has no row.
	ProxySettings(ctx context.Context, poolID string) (ProxySettingsRecord, error)
	// SetProxySettings replaces the row of the pool poolID when rec.Version is
	// the stored version (0 for a pool with no row). In one transaction it checks
	// the fence f, writes the row and writes the audit entry. It returns the
	// stored record with the new version. It returns ErrNotFound for a pool
	// that does not exist, ErrConflict for a stale version, namespace.ErrFenced
	// for a stale fence and ErrInvalid for a settings value that the
	// constructor did not make.
	SetProxySettings(
		ctx context.Context, f namespace.Fence, poolID string, rec ProxySettingsRecord, e audit.Entry,
	) (ProxySettingsRecord, error)
}
