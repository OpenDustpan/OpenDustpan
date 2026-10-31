//go:build e2e

package e2e

import (
	"opendustpan.org/opendustpan/pkg/dustpan"
	"opendustpan.org/opendustpan/pkg/store/postgres"
	"opendustpan.org/opendustpan/pkg/store/sqlite"
)

// trustRuleStoreOptions returns the option that gives the in-process server
// the trust rule store of st, as the binary gives it. With a fake issuer,
// the reads of keys may reach the loopback and trust the fake.
func trustRuleStoreOptions(st serverStore, issuer *OIDCIssuerFake) []dustpan.Option {
	var opts []dustpan.Option

	switch s := st.(type) {
	case *sqlite.Store:
		opts = append(opts, dustpan.WithTrustRules(s.TrustRules()))
	case *postgres.Store:
		opts = append(opts, dustpan.WithTrustRules(s.TrustRules()))
	default:
		return nil
	}

	if issuer != nil {
		opts = append(
			opts,
			dustpan.WithTrustRuleKeysLoopbackForTests(),
			dustpan.WithTrustRuleKeysRootCAs(issuer.certPool()),
		)
	}

	return opts
}
