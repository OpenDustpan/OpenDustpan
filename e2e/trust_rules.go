//go:build e2e

package e2e

import (
	"opendustpan.org/opendustpan/pkg/dustpan"
)

// trustRuleOptions returns the options that let the in-process server read the
// keys of the fake issuer on the loopback, or nil without an issuer.
func trustRuleOptions(issuer *OIDCIssuerFake) []dustpan.Option {
	if issuer == nil {
		return nil
	}

	return []dustpan.Option{
		dustpan.WithTrustRuleKeysLoopbackForTests(),
		dustpan.WithTrustRuleKeysRootCAs(issuer.certPool()),
	}
}
