package agent

import (
	"context"
	"fmt"
	"strings"

	"opendustpan.org/opendustpan/pkg/audit"
	"opendustpan.org/opendustpan/pkg/egress"
)

// The bounds of the egress allowlist.
const (
	// maxAllowlistEntries is the largest count of the hosts and of the CIDR
	// rules, each.
	maxAllowlistEntries = 64
	// maxAllowlistHostBytes is the largest length of one host entry.
	maxAllowlistHostBytes = 253
	// maxPort is the largest TCP port.
	maxPort = 65535
)

// EgressAllowlistStore is the persistence port for the egress allowlist of
// the installation: the host names and CIDR rules that a model URL may reach.
type EgressAllowlistStore interface {
	// Get returns the stored allowlist, or the empty allowlist when none was
	// stored. The order of the hosts and of the rules is the order of the
	// last Set.
	Get(ctx context.Context) (egress.Allowlist, error)
	// Set replaces the allowlist and appends e in the same transaction. It
	// returns ErrInvalid for an entry that is not in the scope audit.ServerScope.
	Set(ctx context.Context, a egress.Allowlist, e audit.Entry) error
}

// CheckAllowlist returns ErrInvalid unless a holds at most 64 hosts and 64
// CIDR rules, each host is 1 to 253 bytes with no control character or
// space, and each rule has a valid range and a port from 1 to 65535.
func CheckAllowlist(a egress.Allowlist) error {
	if len(a.Hosts) > maxAllowlistEntries || len(a.CIDRs) > maxAllowlistEntries {
		return fmt.Errorf("%w: the allowlist holds more than %d hosts or rules", ErrInvalid, maxAllowlistEntries)
	}

	for _, h := range a.Hosts {
		if h == "" || len(h) > maxAllowlistHostBytes || strings.ContainsFunc(h, isControlOrSpace) {
			return fmt.Errorf("%w: the allowlist host %q is not valid", ErrInvalid, h)
		}
	}

	for _, r := range a.CIDRs {
		if !r.CIDR.IsValid() {
			return fmt.Errorf("%w: an allowlist rule has no valid range", ErrInvalid)
		}

		if r.Port < 1 || r.Port > maxPort {
			return fmt.Errorf("%w: the port %d of the rule %s is not from 1 to %d", ErrInvalid, r.Port, r.CIDR, maxPort)
		}
	}

	return nil
}

func isControlOrSpace(r rune) bool {
	return isControlRune(r) || r == ' ' || r == 0x7f
}
